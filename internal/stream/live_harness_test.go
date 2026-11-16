// live_harness_test.go - 实时流测试 harness：进程内状态化假 sharkd（UDS，
// load=会话内存快照语义——load 后行集增长对本会话不可见直到重 load，这是
// 追帧 reload 链路的关键断言点）与 WS/队列测试小工具。
package stream

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// memSharkd 模拟 sharkd 会话语义：load 切换当前文件（文件不存在即 envelope
// error，与真实 sharkd 打不开文件同形态）；frames 按 skip/limit 在**会话快照**
// 行集上切片（真 sharkd load=内存快照，load 后 setRows 增长不可见直到重新
// load）；check 校验 filter（含 "bad" 即语法错）；frame 回单帧详情。skip≤0 回
// -32600（真 sharkd UINTEGER 约束：skip 须 >0，省略=从第 1 行）。
type memSharkd struct {
	ln     net.Listener
	mu     sync.Mutex
	rows   map[string]int // 分片 basename → 帧行数（可运行期增长，模拟落盘）
	loaded string         // 诊断：最近一次 load 的分片
	recv   []string       // 收到的请求行（上游断言 skip 游标/filter 透传/load 次数）

	slowFrames time.Duration // frames 响应延迟（轻查询并行不互扰验证用）
}

func startMemSharkd(t *testing.T) *memSharkd {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "sharkd.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	m := &memSharkd{ln: ln, rows: map[string]int{}}
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			go m.serve(c)
		}
	}()
	return m
}

// setRows 运行期改写分片行数（增长/新片）。
func (m *memSharkd) setRows(shard string, n int) {
	m.mu.Lock()
	m.rows[shard] = n
	m.mu.Unlock()
}

// written 已收到请求行快照。
func (m *memSharkd) written() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.recv...)
}

type memParams struct {
	File   string `json:"file"`
	Skip   int    `json:"skip"`
	Limit  int    `json:"limit"`
	Filter string `json:"filter"`
	Frame  int    `json:"frame"`
}

// serve 单会话：逐行解析 JSON-RPC 请求，按 sharkd 语义回响应（恒单行）。snap
// 为本会话 load 时的行数快照。
func (m *memSharkd) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	snap := 0
	for sc.Scan() {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		if req.Method == "frames" { // 慢拉延迟在锁外睡：不阻塞其他连接
			m.mu.Lock()
			slow := m.slowFrames
			m.mu.Unlock()
			if slow > 0 {
				time.Sleep(slow)
			}
		}
		m.mu.Lock()
		m.recv = append(m.recv, sc.Text())
		var p memParams
		_ = json.Unmarshal(req.Params, &p)
		resp := m.dispatch(req.ID, req.Method, p, req.Params, &snap)
		m.mu.Unlock()
		_, _ = c.Write(append(resp, '\n'))
	}
	_ = sc.Err() // 测试 harness：客户端断开即 EOF，读错无断言意义
}

// dispatch 响应生成（持 mu 调用）。
func (m *memSharkd) dispatch(id int, method string, p memParams, rawParams json.RawMessage, snap *int) []byte {
	switch method {
	case "load":
		if _, err := os.Stat(p.File); err != nil {
			return fmt.Appendf(nil,
				`{"jsonrpc":"2.0","id":%d,"error":{"code":-2,"message":"open %s: no such file"}}`, id, p.File)
		}
		m.loaded = filepath.Base(p.File)
		*snap = m.rows[m.loaded]
		return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"result":{"err":0}}`, id)
	case "frames":
		var rp map[string]any
		if json.Unmarshal(rawParams, &rp) == nil {
			if v, ok := rp["skip"]; ok {
				if f, _ := v.(float64); f <= 0 {
					return fmt.Appendf(nil,
						`{"jsonrpc":"2.0","id":%d,"error":{"code":-32600,"message":"The value for skip must be a positive integer"}}`, id)
				}
			}
		}
		total := *snap // 会话快照：load 后行集增长不可见直到重 load
		rows := make([]string, 0, p.Limit)
		for i := p.Skip; i < total && i < p.Skip+p.Limit; i++ {
			rows = append(rows, fmt.Sprintf(`{"num":%d,"time":"1700000000.%06d"}`, i+1, i))
		}
		return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"result":[%s]}`, id, strings.Join(rows, ","))
	case "check":
		if strings.Contains(p.Filter, "bad") {
			return fmt.Appendf(nil,
				`{"jsonrpc":"2.0","id":%d,"error":{"code":-1,"message":"filter syntax error"}}`, id)
		}
		return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"result":{"err":0}}`, id)
	case "frame":
		return fmt.Appendf(nil,
			`{"jsonrpc":"2.0","id":%d,"result":{"err":0,"num":%d,"tree":"detail-%d"}}`, id, p.Frame, p.Frame)
	}
	return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"no such method"}}`, id)
}

// loaded 记录的当前 load 分片（诊断用）。
func (m *memSharkd) currentLoaded() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loaded
}

// countLoadOf 统计请求行中对指定分片的 load 次数。
func countLoadOf(lines []string, shard string) int {
	n := 0
	for _, l := range lines {
		if strings.Contains(l, `"method":"load"`) && strings.Contains(l, shard) {
			n++
		}
	}
	return n
}

// hasLine 报告请求行快照中是否有一行包含子串。
func hasLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// --- 测试小工具 ---

// newManager 测试用管理器（自动收尾）。
func newManager(t *testing.T, sock, spoolDir string) *Manager {
	t.Helper()
	m := NewManager(Config{Sock: sock, SpoolDir: spoolDir})
	t.Cleanup(m.Close)
	return m
}

// writeShard 在 spool 写分片并以 os.Chtimes 指定 mtime（规避文件系统时间戳精度）。
func writeShard(t *testing.T, dir, name string, mtime time.Time, content []byte) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, content, 0o644))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
}

// appendShard 向分片追加字节（模拟落盘增长；load=内存快照语义下，增长可见须
// 配合活动片 reload——测试用例随 append 调 setRows 增行）。
func appendShard(t *testing.T, dir, name string, bytes int) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(strings.Repeat("x", bytes))
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// fakeWS 假 WS 连接对（net.Pipe）：Attach 侧消费，测试侧收发帧。
type fakeWS struct {
	c *websocket.Conn
}

// newFakeWS 建一对 WS 连接：测试侧 client + 会话侧 server（真 HTTP 升级握手；
// server 端 handler 挂起保活，读写由 writerLoop/测试侧承担）。
func newFakeWS(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	srvCh := make(chan *websocket.Conn, 1)
	l := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Upgrade(w, r, nil, 4096, 4096)
		if err != nil {
			return
		}
		srvCh <- ws
		select {} // 保活至连接关闭（测试收尾 Cleanup Close）
	}))
	t.Cleanup(l.Close)
	wsURL := "ws" + strings.TrimPrefix(l.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ws.Close() })
	srv := <-srvCh
	t.Cleanup(func() { _ = srv.Close() })
	return ws, srv
}

func marshalFrame(t *testing.T, m map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	require.NoError(t, err)
	return b
}

// readUntilType 顺序读帧直至遇到指定 type（跳过其余帧），返回该帧 JSON 对象。
func readUntilType(t *testing.T, ws *websocket.Conn, typ string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		require.NoError(t, ws.SetReadDeadline(deadline))
		_, data, err := ws.ReadMessage()
		require.NoError(t, err, "等待 %s 帧超时", typ)
		var m map[string]any
		require.NoError(t, json.Unmarshal(data, &m))
		if m["type"] == typ {
			return m
		}
	}
}

// readFrameString 带超时读一条 WS text 帧原文。
func readFrameString(t *testing.T, ws *websocket.Conn) string {
	t.Helper()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, data, err := ws.ReadMessage()
	require.NoError(t, err)
	return string(data)
}

// readNoFrame 断言时限内无更多帧。注意：gorilla 连接读超时后内部 readErr 固化，
// 连接不可复用——调用后须弃用该连接（出窗/静默断言走重连）。
func readNoFrame(t *testing.T, ws *websocket.Conn) {
	t.Helper()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(300*time.Millisecond)))
	_, _, err := ws.ReadMessage()
	if err == nil {
		t.Fatal("期望静默(读超时), 实际收到一帧")
	}
}

// rowsNums 提取 rows 帧各行 ref 复合键中的 num 序列（断言无重无漏）。
func rowsNums(t *testing.T, m map[string]any) []float64 {
	t.Helper()
	raws, ok := m["rows"].([]any)
	require.True(t, ok, "rows 帧缺 rows 数组: %v", m)
	nums := make([]float64, 0, len(raws))
	for _, it := range raws {
		row := it.(map[string]any)
		ref, ok := row["ref"].(map[string]any)
		require.True(t, ok, "行缺 ref 复合键: %v", row)
		nums = append(nums, ref["num"].(float64))
		require.Equal(t, ref["num"], row["num"], "ref.num 与行内 num 不一致")
	}
	return nums
}

// rowsShards 提取 rows 帧各行 ref.shard 集合。
func rowsShards(t *testing.T, m map[string]any) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, it := range m["rows"].([]any) {
		ref := it.(map[string]any)["ref"].(map[string]any)
		out[ref["shard"].(string)] = true
	}
	return out
}

// writerLoop 模拟服务端写循环：出队逐帧下发（被顶替/会话关闭即退出）。
func writerLoop(s *Session, ws *websocket.Conn) {
	for {
		msg, ok := s.PopFor(ws)
		if !ok {
			return
		}
		if err := ws.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

// mustSession 取流会话（存在即取，不存在建）。
func mustSession(t *testing.T, m *Manager, handle string) *Session {
	t.Helper()
	s, err := m.GetOrCreate(handle)
	require.NoError(t, err)
	return s
}

// wsOf 会话当前持有的连接的**客户端侧对端**（writerLoop 写 server 端，测试读
// client 端；dialSession/attachRaw 时登记映射）。
func wsOf(t *testing.T, s *Session) *websocket.Conn {
	t.Helper()
	s.wsMu.Lock()
	srv := s.ws
	s.wsMu.Unlock()
	wsClientMu.Lock()
	defer wsClientMu.Unlock()
	return wsClient[srv]
}

// srvOf 会话当前持有的 server 端连接（Detach 用——Detach 只认当前持有者）。
func srvOf(t *testing.T, s *Session) *websocket.Conn {
	t.Helper()
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return s.ws
}

// wsClient server 端连接 → 测试侧 client 连接映射（harness 登记）。
var (
	wsClientMu sync.Mutex
	wsClient   = map[*websocket.Conn]*websocket.Conn{}
)

// attachRaw 直接 Attach 一条新 WS（不经 dialSession），回被顶替的旧连接
// （由调用方断言非 nil / 关闭语义）。
func attachRaw(t *testing.T, s *Session) *websocket.Conn {
	t.Helper()
	test, srv := newFakeWS(t)
	old := s.Attach(srv)
	s.Enqueue(HelloFrame())
	go writerLoop(s, srv)
	wsClientMu.Lock()
	wsClient[srv] = test
	wsClientMu.Unlock()
	t.Cleanup(func() { _ = test.Close() })
	return old
}

// dialSession attach 一条新 WS 并启动写循环，回该连接的客户端侧对端。
func dialSession(t *testing.T, s *Session) *websocket.Conn {
	t.Helper()
	test, srv := newFakeWS(t)
	if old := s.Attach(srv); old != nil {
		_ = old.Close()
	}
	s.Enqueue(HelloFrame())
	go writerLoop(s, srv)
	wsClientMu.Lock()
	wsClient[srv] = test
	wsClientMu.Unlock()
	t.Cleanup(func() { _ = test.Close() })
	return test
}
