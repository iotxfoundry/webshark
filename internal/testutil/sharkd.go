// Package testutil 进程内测试基建：状态化假 sharkd（UDS，JSON-RPC 行协议，
// load=会话内存快照语义）与跨包复用的测试小工具（分片写入、WS 帧读取）。
package testutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Sharkd 进程内状态化假 sharkd：UDS accept 循环 + JSON-RPC 行解析 + 会话语义
// dispatch。语义：load 切换当前文件（文件不存在即 envelope error，与真实
// sharkd 打不开文件同形态）；frames 按 skip/limit 在**本会话 load 快照**的行集
// 上切片（真 sharkd load=内存快照，load 后行集增长对本会话不可见直到重 load；
// skip≤0 回 -32600，真 sharkd UINTEGER 约束：skip 须 >0，省略=从第 1 行）；
// frame 回单帧详情（越界 -8003）；check 校验 filter（含 "bad" 即语法错）；
// complete/follow/tap 回固定形态。观测与注入点：loads 计数（LRU 断言）、conns
// 计数（每连接一条，sharkd 每连接 fork 一子进程）、recv 请求行记录（上游游标/
// filter 透传断言）、failLoads 注入 load 失败（退避验证）、SlowFrames 延迟
// （轻查询并行不互扰验证）、TapDetails 生成专家明细条数。
type Sharkd struct {
	// ln UDS 监听句柄。
	ln net.Listener
	mu sync.Mutex

	loaded string         // 诊断：最近一次 load 的分片
	loads  map[string]int // 分片 basename → load 次数（LRU 断言）
	conns  int            // 已接受的连接数
	recv   []string       // 收到的请求行（各连接共享快照）

	// RowN 行数表（SetRows 运行期改写分片行数，模拟落盘增长/新片）。
	RowN map[string]int
	// RowsByTable load 快照行数模型：true=取 RowN 行数表（实时流侧会话内存
	// 快照模型，文件字节数无关）；false=简化模型「每字节一帧」（HTTP 侧帧越界
	// 模拟，load 快照=文件尺寸，增长需重 load）。
	RowsByTable bool

	// SlowFrames frames 响应延迟（锁外睡：不阻塞其他连接）。
	SlowFrames time.Duration
	// FailLoads 测试注入：前 N 次 load 失败（0=不失败），验证退避。
	FailLoads atomic.Int32
	// TapDetails tap 响应生成的专家明细条数（0=空列表）。
	TapDetails int
}

// StartSharkd 起 UDS 假 sharkd（测试收尾自动关听）。
func StartSharkd(t *testing.T) *Sharkd {
	// UDS 落在 t.TempDir()，测试收尾随目录一并清理。
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "sharkd.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	m := &Sharkd{ln: ln, loads: map[string]int{}, RowN: map[string]int{}}
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			m.mu.Lock()
			m.conns++
			m.mu.Unlock()
			go m.serve(c)
		}
	}()
	return m
}

// Addr 假 sharkd 的 UDS 地址。
func (m *Sharkd) Addr() string { return m.ln.Addr().String() }

// SetRows 运行期改写分片行数（增长/新片）。
func (m *Sharkd) SetRows(shard string, n int) {
	m.mu.Lock()
	m.RowN[shard] = n
	m.mu.Unlock()
}

// Written 已收到请求行快照。
func (m *Sharkd) Written() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.recv...)
}

// LoadCount 指定分片的 load 次数。
func (m *Sharkd) LoadCount(shard string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loads[shard]
}

// Conns 已接受的连接数（sharkd 每连接 fork 一子进程）。
func (m *Sharkd) Conns() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.conns
}

// rowsFor load 快照行数（持 mu 调用）。
func (m *Sharkd) rowsFor(base string, size int64) int {
	if m.RowsByTable {
		return m.RowN[base]
	}
	return int(size)
}

type sharkdReq struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type sharkdParams struct {
	File   string `json:"file"`
	Skip   int    `json:"skip"`
	Limit  int    `json:"limit"`
	Filter string `json:"filter"`
	Frame  int    `json:"frame"`
	Field  string `json:"field"`
	Pref   string `json:"pref"`
	Prefix string `json:"prefix"`
}

// snapshot 单会话 load 快照（真 sharkd load=内存快照，跨请求保持直到重 load）。
type snapshot struct {
	file string // load 的分片 basename
	rows int    // 快照行数
}

// serve 单会话：逐行解析 JSON-RPC 请求，按 sharkd 语义回响应（恒单行）。
func (m *Sharkd) serve(c net.Conn) {
	defer func() { _ = c.Close() }()
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	var snap snapshot
	for sc.Scan() {
		var req sharkdReq
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			continue
		}
		if req.Method == "frames" { // 慢拉延迟在锁外睡：不阻塞其他连接
			m.mu.Lock()
			slow := m.SlowFrames
			m.mu.Unlock()
			if slow > 0 {
				time.Sleep(slow)
			}
		}
		m.mu.Lock()
		m.recv = append(m.recv, sc.Text())
		var p sharkdParams
		_ = json.Unmarshal(req.Params, &p)
		resp := m.dispatch(req.ID, req.Method, p, req.Params, &snap)
		m.mu.Unlock()
		_, _ = c.Write(append(resp, '\n'))
	}
	_ = sc.Err() // 测试 harness：客户端断开即 EOF，读错无断言意义
}

// dispatch 响应生成（持 mu 调用）。
func (m *Sharkd) dispatch(id int, method string, p sharkdParams, rawParams json.RawMessage, snap *snapshot) []byte {
	resp := func(result string) []byte {
		return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result)
	}
	respErr := func(code int, msg string) []byte {
		return fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"error":{"code":%d,"message":%q}}`, id, code, msg)
	}
	switch method {
	case "load":
		if n := m.FailLoads.Add(-1); n >= 0 { // 配额未尽：注入 load 失败（模拟慢盘/分片被覆盖）
			return respErr(-2, "open "+p.File+": no such file")
		}
		if _, err := os.Stat(p.File); err != nil {
			return respErr(-2, "open "+p.File+": no such file")
		}
		base := filepath.Base(p.File)
		m.loaded = base
		m.loads[base]++
		snap.file = base
		snap.rows = m.rowsFor(base, sizeOf(p.File))
		return resp(`{"err":0}`)
	case "frames":
		var rp map[string]any
		if json.Unmarshal(rawParams, &rp) == nil {
			if v, ok := rp["skip"]; ok {
				if f, _ := v.(float64); f <= 0 {
					return respErr(-32600, "The value for skip must be a positive integer")
				}
			}
		}
		total := snap.rows // 会话快照：load 后行集增长不可见直到重 load
		rows := make([]string, 0, p.Limit)
		for i := p.Skip; i < total && i < p.Skip+p.Limit; i++ {
			rows = append(rows, fmt.Sprintf(`{"num":%d,"time":"1700000000.%06d"}`, i+1, i))
		}
		return resp("[" + strings.Join(rows, ",") + "]")
	case "frame":
		if int64(p.Frame) > int64(snap.rows) {
			return respErr(-8003, "Invalid frame - The frame number requested is out of range")
		}
		return resp(fmt.Sprintf(`{"err":0,"num":%d,"loaded":%q,"tree":"detail-%d"}`, p.Frame, snap.file, p.Frame))
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
		if m.TapDetails > 0 { // 测试用：按 TapDetails 生成专家明细
			details := make([]map[string]int, m.TapDetails)
			for i := range details {
				details[i] = map[string]int{"frame": i}
			}
			b, _ := json.Marshal(map[string]any{
				"taps": []map[string]any{{"tap": "expert", "type": "expert", "details": details}},
			})
			return resp(string(b))
		}
		return resp(`{"tap":[],"err":0}`)
	}
	return respErr(-32601, "no such method")
}

// sizeOf 文件字节数（不可读为 0；调用方已 stat 成功，仅防御窗口）。
func sizeOf(path string) int64 {
	if fi, err := os.Stat(path); err == nil {
		return fi.Size()
	}
	return 0
}

// WriteShard 在 dir 写分片并以 os.Chtimes 指定 mtime（规避文件系统时间戳精度）。
func WriteShard(t *testing.T, dir, name string, mtime time.Time, content []byte) {
	// 写完即 Chtimes，保证 mtime 断言不受落盘时刻影响。
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, content, 0o644))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
}

// ReadFrame 带超时读一条 WS text 帧原文。
func ReadFrame(t *testing.T, ws *websocket.Conn) string {
	// 5s 读超时，防对端不回帧时测试挂死。
	t.Helper()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, data, err := ws.ReadMessage()
	require.NoError(t, err)
	return string(data)
}
