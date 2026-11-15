// ws.go - GET /api/ws：实时推送流 WebSocket 入口。升级前同源校验（Origin 非空
// 须与 Host 一致，否则 403）；升级成功后绑定/接管流会话，hello 后单写 goroutine
// 出队下发，读循环承接 _ctl 流控制；断开 Detach 进宽限（位置随会话保留待重连）。
package server

import (
	"net/http"

	"github.com/gorilla/websocket"

	"github.com/iotxfoundry/webshark/internal/stream"
)

// maxCtlMsg WS 上行单条消息上限（仅 _ctl 流控制，量级极小）。
const maxCtlMsg = 32 << 10

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
	sess.Enqueue(stream.HelloFrame("live"))
	ws.SetReadLimit(maxCtlMsg)

	go func() { // 单写 goroutine：出队逐帧下发（被顶替/写失败即退出，读循环随之收尾）
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
