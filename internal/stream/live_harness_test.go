// live_harness_test.go - 实时流测试 harness：进程内状态化假 sharkd（UDS，
// load=会话内存快照语义——load 后行集增长对本会话不可见直到重 load，这是
// 追帧 reload 链路的关键断言点）与 WS/队列测试小工具。
package stream

import (
	"encoding/json"
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

	"github.com/iotxfoundry/webshark/internal/testutil"
)

// startMemSharkd 行数表模型假 sharkd（load=会话内存快照：快照行数取 SetRows
// 行数表，文件字节数无关——load 后行集增长对本会话不可见直到重 load，这是
// 追帧 reload 链路的关键断言点）。
func startMemSharkd(t *testing.T) *testutil.Sharkd {
	t.Helper()
	m := testutil.StartSharkd(t)
	m.RowsByTable = true
	return m
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

// appendShard 向分片追加字节（模拟落盘增长；load=内存快照语义下，增长可见须
// 配合活动片 reload——测试用例随 append 调 SetRows 增行）。
func appendShard(t *testing.T, dir, name string, bytes int) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(strings.Repeat("x", bytes))
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

// newFakeWS 建一对 WS 连接：测试侧 client + 会话侧 server（真 HTTP 升级握手；
// server 端 handler 挂起保活，读写由 writerLoop/测试侧承担）。
func newFakeWS(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	srvCh := make(chan *websocket.Conn, 1)
	up := websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096}
	l := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := up.Upgrade(w, r, nil)
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
// client 端；dialSession 时登记映射）。
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

// dialSession 一条新 WS 接入会话并启动写循环，回 (客户端侧对端, 被顶替旧连接)。
// takeover 顶替语义即关闭旧连接（重连接管）；false 保留旧连接回给调用方，
// 供接管/顶替语义断言。
func dialSession(t *testing.T, s *Session, takeover bool) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	test, srv := newFakeWS(t)
	old := s.Attach(srv)
	if takeover && old != nil {
		_ = old.Close()
	}
	s.Enqueue(HelloFrame())
	go writerLoop(s, srv)
	wsClientMu.Lock()
	wsClient[srv] = test
	wsClientMu.Unlock()
	t.Cleanup(func() { _ = test.Close() })
	return test, old
}
