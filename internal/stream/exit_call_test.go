// exit_call_test.go - 会话关闭后的 Call 拒拨：shutdown 后 Call 不得懒建并
// 拨号一条无人再关闭的 sharkd 连接（每条泄漏一个 sharkd 子进程）。
package stream

import (
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCallAfterShutdown shutdown 之后 Call 必须直接拒绝，不再新建并拨号
// sharkd 连接（worker 退出即 closeShark 置 nil，此后新建无人关闭）。
func TestCallAfterShutdown(t *testing.T) {
	var accepts atomic.Int64
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "s.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			accepts.Add(1)
			_ = c.Close()
		}
	}()

	mgr := newManager(t, ln.Addr().String(), t.TempDir())
	sess, err := mgr.GetOrCreate("h")
	require.NoError(t, err)

	sess.shutdown()

	_, err = sess.CallCtx(sess.ctx, "info", nil)
	require.Error(t, err, "shutdown 后 Call 应返回错误")

	time.Sleep(100 * time.Millisecond)
	require.EqualValues(t, 0, accepts.Load(), "shutdown 后 Call 不应拨号 sharkd")
	sess.sharkMu.Lock()
	gone := sess.shark == nil
	sess.sharkMu.Unlock()
	require.True(t, gone, "shutdown 后不应留下 shark client")
}
