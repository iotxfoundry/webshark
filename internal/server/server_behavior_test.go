// server_behavior_test.go - HTTP 服务层行为锁定回归网：经公开路由与进程内假
// sharkd（UDS，load=会话快照语义）驱动，只锁可观察行为（status 清单排序 /
// download 拼接 / subscribe / frame 路由与 LRU / follow·tap 透传 / 轻查询 /
// WS 门禁 / 鉴权 / 静态回落），不引用内部实现细节。
package server

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// --- 假 sharkd ---

// memSharkd 模拟 sharkd 会话语义：load 切换当前文件（不存在即 envelope error）；
// frame 回单帧详情；check 含 "bad" 即语法错；complete 回固定候选；load 次数
// 可查（LRU 断言）。
type memSharkd struct {
	ln     net.Listener
	mu     sync.Mutex
	loaded string
	loads  map[string]int // 分片 basename → load 次数
}

func startFakeSharkd(t *testing.T) *memSharkd {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "sharkd.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	m := &memSharkd{ln: ln, loads: map[string]int{}}
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

type fakeReq struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type fakeParams struct {
	File   string         `json:"file"`
	Frame  int            `json:"frame"`
	Filter string         `json:"filter"`
	Field  string         `json:"field"`
	Pref   string         `json:"pref"`
	Prefix string         `json:"prefix"`
	Extra  map[string]any `json:"-"`
}

func (m *memSharkd) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	buf := make([]byte, 0, 64<<10)
	scanned := make([]byte, 1<<16)
	var snap string
	for {
		n, err := c.Read(scanned)
		if err != nil {
			return
		}
		buf = append(buf, scanned[:n]...)
		for {
			i := indexByte(buf, '\n')
			if i < 0 {
				break
			}
			line := buf[:i]
			buf = buf[i+1:]
			var req fakeReq
			if json.Unmarshal(line, &req) != nil {
				continue
			}
			m.mu.Lock()
			var p fakeParams
			_ = json.Unmarshal(req.Params, &p)
			resp := m.dispatch(req, p, &snap)
			m.mu.Unlock()
			if _, werr := c.Write(append(resp, '\n')); werr != nil {
				return
			}
		}
	}
}

// indexByte 小助手（避免 strings 依赖扰动）。
func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// dispatch 响应生成（持 mu 调用）。
func (m *memSharkd) dispatch(req fakeReq, p fakeParams, snap *string) []byte {
	resp := func(result string) []byte {
		return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"result":%s}`, req.ID, result)
	}
	respErr := func(code int, msg string) []byte {
		return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"error":{"code":%d,"message":%q}}`, req.ID, code, msg)
	}
	switch req.Method {
	case "load":
		if _, err := os.Stat(p.File); err != nil {
			return respErr(-2, "open "+p.File+": no such file")
		}
		base := filepath.Base(p.File)
		m.loaded = base
		m.loads[base]++
		*snap = base
		return resp(`{"err":0}`)
	case "frame":
		return resp(fmt.Sprintf(`{"err":0,"num":%d,"loaded":%q,"tree":"detail-%d"}`, p.Frame, *snap, p.Frame))
	case "check":
		if strings.Contains(p.Filter, "bad") {
			return respErr(-1, "filter syntax error")
		}
		return resp(`{"err":0}`)
	case "complete":
		out := map[string]any{}
		if p.Field != "" {
			out["field"] = []string{p.Field + ".x"}
		}
		if p.Pref != "" {
			out["pref"] = []string{p.Pref + ".proto"}
		}
		b, _ := json.Marshal(out)
		return resp(string(b))
	case "follow":
		return resp(`{"follow":"...text...","err":0}`)
	case "tap":
		return resp(`{"tap":[],"err":0}`)
	}
	return respErr(-32601, "no such method")
}

func (m *memSharkd) loadCount(shard string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loads[shard]
}

// --- harness ---

// newServer 装配测试服务并起 httptest。
func newServer(t *testing.T, mutate func(*Config)) (*Server, *httptest.Server, string, *memSharkd) {
	t.Helper()
	sock := startFakeSharkd(t)
	cfg := Config{SpoolDir: t.TempDir(), SharkdSock: sock.ln.Addr().String(), Log: discardLog()}
	if mutate != nil {
		mutate(&cfg)
	}
	s := New(cfg)
	t.Cleanup(func() { _ = s.Close() })
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts, cfg.SpoolDir, sock
}

// discardLog 静默 logger。
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeShard 写分片并指定 mtime（规避文件系统时间戳精度）。
func writeShard(t *testing.T, dir, name string, mtime time.Time, content []byte) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, content, 0o644))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
}

// getJSON GET 并解析 JSON 应答。
func getJSON(t *testing.T, ts *httptest.Server, path string) (int, map[string]any) {
	t.Helper()
	resp, err := ts.Client().Get(ts.URL + path)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return resp.StatusCode, body
}

// postJSON POST JSON 并解析应答。
func postJSON(t *testing.T, ts *httptest.Server, path, body string) (int, map[string]any) {
	t.Helper()
	resp, err := ts.Client().Post(ts.URL+path, "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	return resp.StatusCode, out
}

// dialWS 升级 /api/ws（extra 为额外查询串，如 "handle=st-1&token=x"）。
func dialWS(t *testing.T, ts *httptest.Server, header http.Header, extra ...string) (*websocket.Conn, error) {
	t.Helper()
	q := strings.Join(extra, "&")
	if q != "" {
		q = "?" + q
	}
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws" + q
	ws, _, err := websocket.DefaultDialer.Dial(u, header)
	if err == nil {
		t.Cleanup(func() { _ = ws.Close() })
	}
	return ws, err
}

// readFrame 带超时读一条 WS text 帧。
func readFrame(t *testing.T, ws *websocket.Conn) string {
	t.Helper()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, data, err := ws.ReadMessage()
	require.NoError(t, err)
	return string(data)
}

// subscribe 建流并断言成功。
func subscribe(t *testing.T, ts *httptest.Server, handle string) map[string]any {
	t.Helper()
	code, body := postJSON(t, ts, "/api/subscribe", fmt.Sprintf(`{"handle":%q}`, handle))
	require.Equal(t, http.StatusOK, code, "subscribe 失败: %v", body)
	return body
}

// --- status ---

func TestStatus(t *testing.T) {
	base := time.Unix(1700000000, 0)

	t.Run("sharkd不可达报reason含socket路径", func(t *testing.T) {
		sock := filepath.Join(t.TempDir(), "absent.sock")
		_, ts, _, _ := newServer(t, func(c *Config) { c.SharkdSock = sock })
		code, body := getJSON(t, ts, "/api/status")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, false, body["connected"])
		require.Contains(t, body["reason"], sock)
	})

	t.Run("分片按mtime降序同mtime名字降序", func(t *testing.T) {
		_, ts, spoolDir, _ := newServer(t, nil)
		writeShard(t, spoolDir, "b.pcapng", base, []byte("1234"))
		writeShard(t, spoolDir, "a.pcapng", base.Add(2*time.Minute), []byte("12"))
		writeShard(t, spoolDir, "c.pcapng", base.Add(time.Minute), []byte("123"))
		writeShard(t, spoolDir, "noise.txt", base.Add(3*time.Minute), []byte("忽略"))
		require.NoError(t, os.Mkdir(filepath.Join(spoolDir, "sub"), 0o755))
		writeShard(t, spoolDir, "t_a.pcapng", base, []byte("x"))
		writeShard(t, spoolDir, "t_b.pcapng", base, []byte("x"))
		_, body := getJSON(t, ts, "/api/status")
		require.Equal(t, true, body["connected"])
		var names []string
		for _, it := range body["files"].([]any) {
			names = append(names, it.(map[string]any)["name"].(string))
		}
		require.Equal(t, []string{"a.pcapng", "c.pcapng", "t_b.pcapng", "t_a.pcapng", "b.pcapng"}, names)
	})

	t.Run("spool目录缺失=空清单", func(t *testing.T) {
		_, ts, _, _ := newServer(t, func(c *Config) { c.SpoolDir = filepath.Join(t.TempDir(), "absent") })
		_, body := getJSON(t, ts, "/api/status")
		require.Equal(t, true, body["connected"])
		require.Empty(t, body["files"])
	})
}

// --- download ---

func TestDownload(t *testing.T) {
	base := time.Unix(1700000000, 0)

	t.Run("多分片按mtime升序拼接", func(t *testing.T) {
		_, ts, spoolDir, _ := newServer(t, nil)
		writeShard(t, spoolDir, "net_00002.pcapng", base.Add(time.Minute), []byte("BBB"))
		writeShard(t, spoolDir, "net_00001.pcapng", base, []byte("AAA"))
		writeShard(t, spoolDir, "net_00003.pcapng", base.Add(2*time.Minute), []byte("CCC"))
		resp, err := ts.Client().Get(ts.URL + "/api/download")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "AAABBBCCC", string(body))
		require.Equal(t, "application/octet-stream", resp.Header.Get("Content-Type"))
		require.Contains(t, resp.Header.Get("Content-Disposition"), "webshark-")
	})

	t.Run("无分片404", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		resp, err := ts.Client().Get(ts.URL + "/api/download")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})

	t.Run("轮换中文件消失被跳过", func(t *testing.T) {
		_, ts, spoolDir, _ := newServer(t, nil)
		writeShard(t, spoolDir, "keep.pcapng", base, []byte("KEEP"))
		writeShard(t, spoolDir, "gone.pcapng", base.Add(time.Minute), []byte("GONE"))
		require.NoError(t, os.Remove(filepath.Join(spoolDir, "gone.pcapng"))) // 打开后打开前被删：清单已含、打开失败
		resp, err := ts.Client().Get(ts.URL + "/api/download")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, "KEEP", string(body))
	})
}

// --- subscribe ---

func TestSubscribe(t *testing.T) {
	t.Run("缺handle400", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		code, _ := postJSON(t, ts, "/api/subscribe", `{}`)
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("正常应答字段", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		body := subscribe(t, ts, "st-1")
		require.Equal(t, true, body["ok"])
		require.Equal(t, "st-1", body["handle"])
		require.Contains(t, body, "paused")
		require.Contains(t, body, "filter")
		require.Contains(t, body, "shard")
	})

	t.Run("流上限429", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		for i := 0; i < 16; i++ {
			subscribe(t, ts, fmt.Sprintf("st-%d", i))
		}
		code, _ := postJSON(t, ts, "/api/subscribe", `{"handle":"st-overflow"}`)
		require.Equal(t, http.StatusTooManyRequests, code)
	})
}

// --- frame ---

func TestFrame(t *testing.T) {
	base := time.Unix(1700000000, 0)

	t.Run("shard路径穿越400", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		subscribe(t, ts, "st-x")
		code, _ := getJSON(t, ts, "/api/frame?handle=st-x&shard=../evil.pcapng&num=1")
		require.Equal(t, http.StatusBadRequest, code)
		code, _ = getJSON(t, ts, fmt.Sprintf("/api/frame?handle=st-x&shard=%s&num=1", `a\b.pcapng`))
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("num非整数400", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		subscribe(t, ts, "st-x")
		code, _ := getJSON(t, ts, "/api/frame?handle=st-x&num=abc")
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("未知handle404", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		code, _ := getJSON(t, ts, "/api/frame?handle=nope&num=1")
		require.Equal(t, http.StatusNotFound, code)
	})

	t.Run("proto非法400", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		subscribe(t, ts, "st-x")
		code, _ := getJSON(t, ts, "/api/frame?handle=st-x&num=1&proto=maybe")
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("实时分片走会话实例且proto透传", func(t *testing.T) {
		_, ts, spoolDir, _ := newServer(t, nil)
		writeShard(t, spoolDir, "live.pcapng", base, []byte("pcap"))
		// WS 接入（建流并解锁 worker 追帧：无连接会话冻结不 load）
		ws, err := dialWS(t, ts, nil, "handle=st-live")
		require.NoError(t, err)
		require.Equal(t, `{"type":"hello","mode":"live"}`, readFrame(t, ws))
		// 实例就绪：轮询 subscribe 应答 shard 字段（worker load 成功后非空）
		require.Eventually(t, func() bool {
			_, body := postJSON(t, ts, "/api/subscribe", `{"handle":"st-live"}`)
			return body["shard"] == "live.pcapng"
		}, 10*time.Second, 50*time.Millisecond, "实时实例未就绪")
		code, body := getJSON(t, ts, "/api/frame?handle=st-live&num=7&proto=true")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, float64(7), body["num"])
		require.Equal(t, "live.pcapng", body["loaded"])
	})

	t.Run("历史分片走缓存实例且LRU", func(t *testing.T) {
		_, ts, spoolDir, sock := newServer(t, nil)
		subscribe(t, ts, "st-h")
		// 写 13 片历史（未 load 进会话：均早于会话冷启动锚点的最新片不存在——
		// spool 为空时会话无分片，shard 显式给出即路由缓存实例）
		for i := 0; i < 13; i++ {
			name := fmt.Sprintf("hist_%02d.pcapng", i)
			writeShard(t, spoolDir, name, base.Add(time.Duration(i)*time.Minute), []byte("pcap"))
		}
		// 逐片查一次：13 次 load（容量 12，首个被淘汰）
		for i := 0; i < 13; i++ {
			code, body := getJSON(t, ts, fmt.Sprintf("/api/frame?handle=st-h&shard=hist_%02d.pcapng&num=%d", i, i))
			require.Equal(t, http.StatusOK, code, "帧查询失败: %v", body)
			require.Equal(t, fmt.Sprintf("hist_%02d.pcapng", i), body["loaded"])
		}
		require.Equal(t, 1, sock.loadCount("hist_00.pcapng"))
		require.Equal(t, 1, sock.loadCount("hist_01.pcapng"))
		// hist_00 已被 LRU 淘汰：再查重 load
		code, _ := getJSON(t, ts, "/api/frame?handle=st-h&shard=hist_00.pcapng&num=0")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, 2, sock.loadCount("hist_00.pcapng"))
		// hist_12 仍在窗内：命中不重复 load
		require.Equal(t, 1, sock.loadCount("hist_12.pcapng"))
		// LRU 命中提位后再查仍不重 load
		_, _ = getJSON(t, ts, "/api/frame?handle=st-h&shard=hist_05.pcapng&num=5")
		_, _ = getJSON(t, ts, "/api/frame?handle=st-h&shard=hist_05.pcapng&num=5")
		require.Equal(t, 1, sock.loadCount("hist_05.pcapng"))
	})

	t.Run("历史分片不存在404", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		subscribe(t, ts, "st-h2")
		code, _ := getJSON(t, ts, "/api/frame?handle=st-h2&shard=absent.pcapng&num=1")
		require.Equal(t, http.StatusNotFound, code)
	})

	t.Run("shard缺省取会话当前分片未就绪409", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil) // spool 空：实时实例永远不就绪
		subscribe(t, ts, "st-empty")
		code, _ := getJSON(t, ts, "/api/frame?handle=st-empty&num=1")
		require.Equal(t, http.StatusConflict, code)
	})
}

// --- follow/tap ---

func TestFollowTap(t *testing.T) {
	t.Run("follow透传", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		subscribe(t, ts, "st-f")
		code, body := postJSON(t, ts, "/api/follow", `{"handle":"st-f","follow":"TCP","filter":"ip"}`)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, "...text...", body["follow"])
	})

	t.Run("tap透传", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		subscribe(t, ts, "st-t")
		code, body := postJSON(t, ts, "/api/tap", `{"handle":"st-t","tap":"expert"}`)
		require.Equal(t, http.StatusOK, code)
		require.Contains(t, body, "tap")
	})

	t.Run("缺handle400", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		code, _ := postJSON(t, ts, "/api/follow", `{"follow":"TCP"}`)
		require.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("流不存在404", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		code, _ := postJSON(t, ts, "/api/tap", `{"handle":"nope","tap":"expert"}`)
		require.Equal(t, http.StatusNotFound, code)
	})
}

// --- check/complete ---

func TestLightQuery(t *testing.T) {
	t.Run("check GET与POST", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		code, body := getJSON(t, ts, "/api/check?filter=ip")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, float64(0), body["err"])
		code, body = postJSON(t, ts, "/api/check", `{"filter":"tcp.port==80"}`)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, float64(0), body["err"])
	})

	t.Run("check语法错400", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		code, body := postJSON(t, ts, "/api/check", `{"filter":"bad filter"}`)
		require.Equal(t, http.StatusBadRequest, code)
		require.Equal(t, float64(-1), body["code"])
		require.Equal(t, "filter syntax error", body["message"])
	})

	t.Run("complete前缀展开为field与pref", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		// prefix 同时进 field 与 pref 两个补全域（sharkd 无 prefix 成员）
		code, body := getJSON(t, ts, "/api/complete?prefix=ip")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, []any{"ip.x"}, body["field"])
		require.Equal(t, []any{"ip.proto"}, body["pref"])
		// POST body 形态: 显式 field 覆盖字段域, pref 仍取 prefix
		code, body = postJSON(t, ts, "/api/complete", `{"prefix":"tcp","field":"tcp.a"}`)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, []any{"tcp.a.x"}, body["field"])
		require.Equal(t, []any{"tcp.proto"}, body["pref"])
		// query 单键 field 形态: 不携 pref（空值不占位）
		code, body = getJSON(t, ts, "/api/complete?field=tcp.b")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, []any{"tcp.b.x"}, body["field"])
		require.NotContains(t, body, "pref")
	})
}

// --- WS ---

func TestWS(t *testing.T) {
	t.Run("缺handle400", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		resp, err := ts.Client().Get(ts.URL + "/api/ws")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("异源Origin403", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		_, err := dialWS(t, ts, http.Header{"Origin": {"http://evil.example.com"}})
		require.ErrorIs(t, err, websocket.ErrBadHandshake)
	})

	t.Run("同源放行并收hello", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		host := strings.TrimPrefix(ts.URL, "http://")
		ws, err := dialWS(t, ts, http.Header{"Origin": {"http://" + host}}, "handle=st-ws")
		require.NoError(t, err)
		require.Equal(t, `{"type":"hello","mode":"live"}`, readFrame(t, ws))
	})

	t.Run("无Origin非浏览器放行", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		ws, err := dialWS(t, ts, nil, "handle=st-ws2")
		require.NoError(t, err)
		require.Equal(t, `{"type":"hello","mode":"live"}`, readFrame(t, ws))
	})

	t.Run("建流上限发error帧后关闭", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		for i := 0; i < 16; i++ {
			subscribe(t, ts, fmt.Sprintf("st-%d", i))
		}
		ws, err := dialWS(t, ts, nil, "handle=st-overflow")
		require.NoError(t, err)
		frame := readFrame(t, ws)
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(frame), &m))
		require.Equal(t, "error", m["type"])
	})
}

// --- 鉴权 ---

func TestAuth(t *testing.T) {
	newAuthed := func(t *testing.T) (*httptest.Server, string) {
		t.Helper()
		_, ts, _, _ := newServer(t, func(c *Config) { c.Token = "sekret" })
		return ts, "sekret"
	}

	t.Run("未配token放行", func(t *testing.T) {
		_, ts, _, _ := newServer(t, nil)
		code, _ := getJSON(t, ts, "/api/status")
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("无凭证401", func(t *testing.T) {
		ts, _ := newAuthed(t)
		code, body := getJSON(t, ts, "/api/status")
		require.Equal(t, http.StatusUnauthorized, code)
		require.Equal(t, float64(401), body["code"])
		require.Equal(t, "未认证", body["message"])
	})

	t.Run("Bearer通过", func(t *testing.T) {
		ts, tok := newAuthed(t)
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/status", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("查询参数token通过", func(t *testing.T) {
		ts, tok := newAuthed(t)
		code, _ := getJSON(t, ts, "/api/status?token="+tok)
		require.Equal(t, http.StatusOK, code)
	})

	t.Run("错误token401", func(t *testing.T) {
		ts, _ := newAuthed(t)
		code, _ := getJSON(t, ts, "/api/status?token=wrong")
		require.Equal(t, http.StatusUnauthorized, code)
	})

	t.Run("静态资源始终可取", func(t *testing.T) {
		ts, _ := newAuthed(t)
		resp, err := ts.Client().Get(ts.URL + "/")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
	})
}

// --- 静态 ---

func TestStatic(t *testing.T) {
	t.Run("未构建提示页200", func(t *testing.T) {
		_, ts, _, _ := newServer(t, func(c *Config) { c.Web = fstest.MapFS{} })
		resp, err := ts.Client().Get(ts.URL + "/")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Contains(t, resp.Header.Get("Content-Type"), "text/html")
		body, _ := io.ReadAll(resp.Body)
		require.Contains(t, string(body), "make web")
	})

	t.Run("MapFS命中与回落", func(t *testing.T) {
		site := fstest.MapFS{
			"index.html":    &fstest.MapFile{Data: []byte("<html>SPA index</html>")},
			"assets/app.js": &fstest.MapFile{Data: []byte("console.log(1)")},
		}
		_, ts, _, _ := newServer(t, func(c *Config) { c.Web = site })
		// index 命中
		resp, err := ts.Client().Get(ts.URL + "/")
		require.NoError(t, err)
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "<html>SPA index</html>", string(b))
		// 静态文件命中
		resp, err = ts.Client().Get(ts.URL + "/assets/app.js")
		require.NoError(t, err)
		b, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, "console.log(1)", string(b))
		// 未知路径回落 index
		resp, err = ts.Client().Get(ts.URL + "/some/route")
		require.NoError(t, err)
		b, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, "<html>SPA index</html>", string(b))
		// /api/ 未知路径 404 JSON
		code, body := getJSON(t, ts, "/api/unknown")
		require.Equal(t, http.StatusNotFound, code)
		require.Contains(t, body, "message")
	})

	t.Run("目录列表禁用", func(t *testing.T) {
		site := fstest.MapFS{
			"index.html":   &fstest.MapFile{Data: []byte("idx")},
			"sub/kept.txt": &fstest.MapFile{Data: []byte("k")},
		}
		_, ts, _, _ := newServer(t, func(c *Config) { c.Web = site })
		resp, err := ts.Client().Get(ts.URL + "/sub/")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}
