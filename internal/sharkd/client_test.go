package sharkd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeSharkd 假 sharkd：按请求序逐条 handler 应答（行协议）。
type fakeSharkd struct {
	ln       net.Listener
	mu       sync.Mutex
	handlers []func(req map[string]any) any
}

func newFakeSharkd(t *testing.T, handlers ...func(req map[string]any) any) *fakeSharkd {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "sharkd.sock"))
	require.NoError(t, err)
	f := &fakeSharkd{ln: ln, handlers: handlers}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSharkd) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeSharkd) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		var req map[string]any
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			return
		}
		f.mu.Lock()
		var h func(map[string]any) any
		if id, ok := req["id"].(float64); ok {
			if i := int(id) - 1; i >= 0 && i < len(f.handlers) {
				h = f.handlers[i]
			}
		}
		f.mu.Unlock()
		if h == nil {
			continue
		}
		resp := h(req)
		if resp == nil {
			continue
		}
		b, err := json.Marshal(resp)
		if err != nil {
			return
		}
		_, _ = fmt.Fprintf(conn, "%s\n", b)
	}
}

func TestCallOKAndIncrementingID(t *testing.T) {
	f := newFakeSharkd(t,
		func(req map[string]any) any {
			return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"n": 1}}
		},
		func(req map[string]any) any {
			return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": map[string]any{"n": 2}}
		},
	)
	c := NewClient(f.ln.Addr().String())
	for i, want := range []int{1, 2} {
		res, err := c.Call("test", nil)
		require.NoError(t, err, i)
		require.JSONEq(t, fmt.Sprintf(`{"n":%d}`, want), string(res))
	}
	require.Equal(t, 2, c.nextID) // id 自增
}

func TestCallEnvelopeError(t *testing.T) {
	f := newFakeSharkd(t,
		func(map[string]any) any {
			return map[string]any{"jsonrpc": "2.0", "id": 1, "error": map[string]any{"code": -32600, "message": "dfilter 语法错误"}}
		},
	)
	c := NewClient(f.ln.Addr().String())
	_, err := c.Call("load", nil)
	require.Error(t, err)
	var se *Error
	require.ErrorAs(t, err, &se)
	require.Equal(t, -32600, se.Code)
	require.Contains(t, se.Error(), "dfilter")
	require.Contains(t, se.Error(), "-32600")
}

func TestMultiLineResponse(t *testing.T) {
	// 专用服务端：响应拆成多个不完整 JSON 行发送，客户端须累积到完整值
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "sharkd.sock"))
	require.NoError(t, err)
	defer func() { _ = ln.Close() }()
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for _, p := range []string{`{"jsonrpc":`, `"2.0","id":1,`, `"result":{"ok":true}}`} {
			_, _ = fmt.Fprintln(conn, p)
		}
	}()
	c := NewClient(ln.Addr().String())
	res, err := c.Call("test", nil)
	require.NoError(t, err)
	require.JSONEq(t, `{"ok":true}`, string(res))
}

func TestReconnectAfterDrop(t *testing.T) {
	f := newFakeSharkd(t,
		func(req map[string]any) any {
			return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": "a"}
		},
		func(req map[string]any) any {
			return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": "b"}
		},
	)
	c := NewClient(f.ln.Addr().String())
	res, err := c.Call("t1", nil)
	require.NoError(t, err)
	require.JSONEq(t, `"a"`, string(res))
	require.NoError(t, c.Close()) // 断线
	res, err = c.Call("t2", nil)  // 自动重拨, id 继续自增 → 命中 handler[1]
	require.NoError(t, err)
	require.JSONEq(t, `"b"`, string(res))
	require.Equal(t, 2, c.nextID)
}

func TestProbe(t *testing.T) {
	require.False(t, Probe(filepath.Join(t.TempDir(), "nope.sock"), 200*time.Millisecond))
	f := newFakeSharkd(t)
	require.True(t, Probe(f.ln.Addr().String(), 200*time.Millisecond))
}

func TestRequestShape(t *testing.T) {
	var mu sync.Mutex
	var raw map[string]any
	f := newFakeSharkd(t, func(req map[string]any) any {
		mu.Lock()
		raw = req
		mu.Unlock()
		return map[string]any{"jsonrpc": "2.0", "id": req["id"], "result": nil}
	})
	c := NewClient(f.ln.Addr().String())
	_, err := c.Call("status", map[string]any{"x": 1})
	require.NoError(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "2.0", raw["jsonrpc"])
	require.Equal(t, float64(1), raw["id"])
	require.Equal(t, "status", raw["method"])
	require.Equal(t, map[string]any{"x": float64(1)}, raw["params"])
}
