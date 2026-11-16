// ws.go - GET /api/ws：实时推送流 WebSocket 入口。升级前同源校验（Origin 非空
// 须与 Host 一致，否则 403）；升级成功后绑定/接管流会话，hello 后单写 goroutine
// 出队下发，读循环承接 _ctl 流控制；断开 Detach 进宽限（位置随会话保留待重连）。
package server

import (
	"net/http"
	"time"

	"github.com/gorilla/websocket"

	"github.com/iotxfoundry/webshark/internal/stream"
)

// maxCtlMsg WS 上行单条消息上限（仅 _ctl 流控制，量级极小）。
const maxCtlMsg = 32 << 10

// pongWait/pingPeriod 读超时与 ping 周期（测试可覆盖，生产不得改小）。
var (
	pongWait   = 90 * time.Second
	pingPeriod = 30 * time.Second
)

// handleWS GET /api/ws?handle=<handle>。
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	handle := r.URL.Query().Get("handle")
	if handle == "" {
		writeErr(w, http.StatusBadRequest, http.StatusBadRequest, "须携带 handle")
		return
	}
	ws, err := s.upgr.Upgrade(w, r, nil)
	if err != nil {
		return // 升级失败（含同源校验拒绝 403）：响应已由 upgrader 写出
	}
	sess, err := s.mgr.GetOrCreate(handle)
	if err != nil { // 流数上限等建流失败：发错误帧后关闭
		_ = ws.WriteMessage(websocket.TextMessage, stream.ErrorFrame(err.Error()))
		_ = ws.Close()
		return
	}
	if old := sess.Attach(ws); old != nil {
		_ = old.Close() // 同 handle 二次接入：接管流，旧连接关闭
	}
	ws.SetReadLimit(maxCtlMsg)
	// 死连接回收：读超时 + pong 续期（浏览器自动回 pong），超时读循环退出 →
	// Detach → 宽限回收。包级参数仅此处读一次入快照（协程用快照，测试覆盖包级
	// 变量时不与尚存协程竞态）。
	pw, pp := pongWait, pingPeriod
	if err := ws.SetReadDeadline(time.Now().Add(pw)); err != nil {
		sess.Detach(ws) // 已 Attach：失败必须解绑，否则会话带着死连接永不回收
		_ = ws.Close()
		return
	}
	ws.SetPongHandler(func(string) error { return ws.SetReadDeadline(time.Now().Add(pw)) })

	done := make(chan struct{})
	defer close(done)
	go func() { // ping 保活：WriteControl 与写协程的 WriteMessage 并发安全（gorilla）
		t := time.NewTicker(pp)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(pw)); err != nil {
					return
				}
			}
		}
	}()

	go func() { // 单写 goroutine：出队逐帧下发（被顶替/写失败即退出，读循环随之收尾）。
		// 先于 Enqueue 启动：队列已满时 Enqueue 背压阻塞，唯一消费者必须先就位
		// （先入队后启动即重连死锁——满队 hello 永远发不出去且 janitor 不回收）。
		for {
			msg, ok := sess.PopFor(ws)
			if !ok {
				return
			}
			if werr := ws.WriteMessage(websocket.TextMessage, msg); werr != nil {
				return
			}
		}
	}()

	sess.Enqueue(stream.HelloFrame("live"))

	for { // 读循环：_ctl 流控制（其余上行忽略）
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		stream.HandleCtl(sess, data)
	}
	sess.Detach(ws) // 断线：进入宽限，追帧位置随会话保留待重连续推
	_ = ws.Close()
}
