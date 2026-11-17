// sharkd_test.go - 假 sharkd 自检：行协议解析、load=会话快照、skip 约束、
// check "bad" 与观测计数（两消费包全量套件外的直接契约锚点）。
package testutil

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// rpcLine 发一行 JSON-RPC 并读回单行响应。
func rpcLine(t *testing.T, c net.Conn, method string, params string) map[string]any {
	t.Helper()
	fmt.Fprintf(c, `{"jsonrpc":"2.0","id":1,"method":%q,"params":%s}`+"\n", method, params)
	resp, err := bufio.NewReader(c).ReadString('\n')
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(resp), &m))
	return m
}

func TestFakeSharkdContract(t *testing.T) {
	dir := t.TempDir()
	m := StartSharkd(t)
	a := filepath.Join(dir, "a.pcapng")
	WriteShard(t, dir, "a.pcapng", time.Now(), make([]byte, 3)) // 简化模型：3 帧

	c, err := net.Dial("unix", m.Addr())
	require.NoError(t, err)
	defer func() { _ = c.Close() }()
	require.Eventually(t, func() bool { return m.Conns() == 1 }, time.Second, 5*time.Millisecond)

	// load 简化模型：快照=文件字节数 3
	require.NotContains(t, rpcLine(t, c, "load", fmt.Sprintf(`{"file":%q}`, a)), "error")
	// rows 按 skip/limit 在会话快照上切片
	res := rpcLine(t, c, "frames", `{"skip":1,"limit":10}`)["result"].([]any)
	require.Len(t, res, 2)
	require.Equal(t, float64(2), res[0].(map[string]any)["num"])
	// skip≤0：-32600（UINTEGER 约束锁定）
	require.Equal(t, float64(-32600), rpcLine(t, c, "frames", `{"skip":0,"limit":10}`)["error"].(map[string]any)["code"])
	// frame 越界 -8003；check "bad" 语法错
	require.Equal(t, float64(-8003), rpcLine(t, c, "frame", `{"frame":9}`)["error"].(map[string]any)["code"])
	require.Equal(t, float64(-1), rpcLine(t, c, "check", `{"filter":"bad filter"}`)["error"].(map[string]any)["code"])
	// 行数表模型：快照=SetRows 行数，与字节数无关
	m2 := StartSharkd(t)
	m2.RowsByTable = true
	b := filepath.Join(dir, "b.pcapng")
	WriteShard(t, dir, "b.pcapng", time.Now(), []byte("x")) // 字节数 1，行数表 2
	m2.SetRows("b.pcapng", 2)
	c2, err := net.Dial("unix", m2.Addr())
	require.NoError(t, err)
	defer func() { _ = c2.Close() }()
	require.NotContains(t, rpcLine(t, c2, "load", fmt.Sprintf(`{"file":%q}`, b)), "error")
	require.Len(t, rpcLine(t, c2, "frames", `{"limit":10}`)["result"].([]any), 2)
	// 不存在的文件 load 即 envelope error（真 sharkd 打不开文件同形态）
	require.Contains(t, rpcLine(t, c, "load", `{"file":"/no/such.pcapng"}`), "error")

	require.Equal(t, 1, m.LoadCount("a.pcapng"))
	require.Equal(t, 1, m2.LoadCount("b.pcapng"))
	require.Eventually(t, func() bool { return m2.Conns() == 1 }, time.Second, 5*time.Millisecond)
	recv := m.Written()
	require.Len(t, recv, 6)
	require.Contains(t, recv[0], `"method":"load"`)
}
