// export_test.go - /api/status 与 /api/download 的环窗与不可读分片契约测试。
// 与实时流同窗口：环外遗留分片（dumpcap 重启换代残留）不进计数/导出；
// 分片均不可读时 download 回 503 而非 200+0 字节。
package server

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotxfoundry/webshark/internal/testutil"
)

// TestDownloadUnreadableShard503 分片均不可读（如 chmod 000）时 download 应
// 503，而不是带 attachment 头的 200 空附件。
func TestDownloadUnreadableShard503(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 忽略文件权限")
	}
	_, ts, spoolDir, _ := newServer(t, nil)
	testutil.WriteShard(t, spoolDir, "a.pcapng", time.Now(), []byte("hello-pcapng"))
	require.NoError(t, os.Chmod(spoolDir+"/a.pcapng", 0o000))

	resp, err := ts.Client().Get(ts.URL + "/api/download")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode, "body=%q", buf.String())
}

// TestRingWindowStatusAndDownload status/download 与实时流同用环窗：目录 20 片
// 时 status files=16、download 只拼最新 16 片，环外旧片不参与。
func TestRingWindowStatusAndDownload(t *testing.T) {
	_, ts, spoolDir, _ := newServer(t, nil)
	contents := make(map[string][]byte, 20)
	base := time.Now().Add(-time.Hour)
	for i := range 20 {
		content := fmt.Appendf(nil, "shard-%02d-content", i)
		name := fmt.Sprintf("s%02d.pcapng", i)
		contents[name] = content
		testutil.WriteShard(t, spoolDir, name, base.Add(time.Duration(i)*time.Minute), content)
	}

	// status：环窗 16 片
	code, body := getJSON(t, ts, "/api/status")
	require.Equal(t, http.StatusOK, code)
	files, ok := body["files"].([]any)
	require.True(t, ok, "files 类型异常: %v", body["files"])
	require.Len(t, files, 16)

	// download：最新 16 片（mtime 升序），不含最旧 4 片
	resp, err := ts.Client().Get(ts.URL + "/api/download")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var got bytes.Buffer
	_, _ = got.ReadFrom(resp.Body)
	var want bytes.Buffer
	for i := 4; i < 20; i++ {
		want.Write(contents[fmt.Sprintf("s%02d.pcapng", i)])
	}
	require.Equal(t, want.Len(), got.Len())
	require.Equal(t, want.String(), got.String())
	require.NotContains(t, got.String(), "shard-00-content")
	require.NotContains(t, got.String(), "shard-03-content")
}
