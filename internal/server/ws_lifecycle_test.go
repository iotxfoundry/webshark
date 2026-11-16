// ws_lifecycle_test.go - WS 生命周期回归：满队列重连不死锁（写协程先于 Enqueue
// 启动）+ 死连接回收（pongWait 读超时 → 服务端主动断开 → 宽限回收）。
package server

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestWSHelloAfterFullQueue 锁定重连不死锁：出站队列已满 256 帧时接入，hello
// 须在 2s 内送达——修复前 Enqueue(hello) 在唯一消费者启动前永久阻塞。
func TestWSHelloAfterFullQueue(t *testing.T) {
	srv, ts, _, _ := newServer(t, nil)
	sess, err := srv.mgr.GetOrCreate("h-full")
	require.NoError(t, err)
	for range 256 { // 灌满出站队列（恰 maxOutQueue 边界，本次入队不阻塞）
		sess.Enqueue([]byte(`{"type":"x"}`))
	}
	ws, err := dialWS(t, ts, nil, "handle=h-full")
	require.NoError(t, err)
	deadline := time.Now().Add(2 * time.Second)
	for {
		require.NoError(t, ws.SetReadDeadline(deadline))
		_, data, err := ws.ReadMessage()
		if err != nil {
			t.Fatalf("2s 内未收到 hello（满队列重连死锁）: %v", err)
		}
		if strings.Contains(string(data), "hello") {
			return
		}
	}
}

// TestWSDeadConnReclaimed 锁定死连接回收：接入消费 hello 后不再读不回 pong，
// 服务端经 pongWait 读超时主动断开——客户端后续读须返回非 timeout 错误（连接
// 已被服务端关闭）。
func TestWSDeadConnReclaimed(t *testing.T) {
	oldWait, oldPeriod := pongWait, pingPeriod
	pongWait, pingPeriod = 200*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { pongWait, pingPeriod = oldWait, oldPeriod })

	_, ts, _, _ := newServer(t, nil)
	ws, err := dialWS(t, ts, nil, "handle=h-dead")
	require.NoError(t, err)
	_ = readFrame(t, ws) // 消费 hello（此后不读不 pong）
	time.Sleep(700 * time.Millisecond)
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
	_, _, err = ws.ReadMessage()
	require.Error(t, err, "服务端应已关连接, 不应静默")
	var netErr net.Error
	require.Falsef(t, errors.As(err, &netErr) && netErr.Timeout(),
		"客户端读超时而非服务端断开: %v", err)
}
