// stream_fix_test.go - 修复回归：/api/frame 一律走分片缓存实例（分流竞态）、
// /api/tap 响应补注实际载入分片、Manager.Close 后 subscribe 503、0 字节分片
// download 200。
package server

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestFrameAlwaysUsesCache 请求 shard == 会话当前分片时仍走分片缓存实例：
// load 与 setCurShard 两步非原子，若按 cur 分流实时实例，轮换瞬间会把请求
// 落到已换文件的实例上，返回另一分片的同号帧。缓存实例按复合键 load 目标
// 文件后查询，无此竞态（同片首次多一次 load，LRU 驻留后命中零 load）。
func TestFrameAlwaysUsesCache(t *testing.T) {
	base := time.Unix(1700000000, 0)
	_, ts, spoolDir, sock := newServer(t, nil)
	writeShard(t, spoolDir, "live.pcapng", base, []byte("pcap"))
	// WS 接入解锁 worker 追帧，等实时实例 load live.pcapng
	ws, err := dialWS(t, ts, nil, "handle=st-cc")
	require.NoError(t, err)
	readFrame(t, ws) // hello
	require.Eventually(t, func() bool {
		_, body := postJSON(t, ts, "/api/subscribe", `{"handle":"st-cc"}`)
		return body["shard"] == "live.pcapng"
	}, 10*time.Second, 50*time.Millisecond, "实时实例未就绪")

	before := sock.loadCount("live.pcapng") // worker 冷启动 load 过一次
	code, body := getJSON(t, ts, "/api/frame?handle=st-cc&shard=live.pcapng&num=7&proto=true")
	require.Equal(t, http.StatusOK, code, "帧查询失败: %v", body)
	require.Equal(t, float64(7), body["num"])
	require.Equal(t, "live.pcapng", body["loaded"])
	require.Equal(t, before+1, sock.loadCount("live.pcapng"), "同当前分片也应经缓存实例 load 恰一次")

	// 缺省 shard（取 cur）：LRU 命中，缓存实例不重 load
	code, body = getJSON(t, ts, "/api/frame?handle=st-cc&num=8")
	require.Equal(t, http.StatusOK, code, "帧查询失败: %v", body)
	require.Equal(t, float64(8), body["num"])
	require.Equal(t, before+1, sock.loadCount("live.pcapng"), "LRU 驻留不应重 load")
}

// TestTapReportsLoadedShard /api/tap 响应在 sharkd result 同级补注 shard 键
// （服务端权威告知实际载入分片，前端不再猜测）；未载入报空串；follow 不变。
func TestTapReportsLoadedShard(t *testing.T) {
	base := time.Unix(1700000000, 0)
	_, ts, spoolDir, _ := newServer(t, nil)
	writeShard(t, spoolDir, "cur.pcapng", base, []byte("pcap"))
	ws, err := dialWS(t, ts, nil, "handle=st-tap")
	require.NoError(t, err)
	readFrame(t, ws) // hello
	require.Eventually(t, func() bool {
		_, body := postJSON(t, ts, "/api/subscribe", `{"handle":"st-tap"}`)
		return body["shard"] == "cur.pcapng"
	}, 10*time.Second, 50*time.Millisecond, "实时实例未就绪")

	code, body := postJSON(t, ts, "/api/tap", `{"handle":"st-tap","tap":"expert"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "cur.pcapng", body["shard"], "tap 响应应补注实际载入分片")
	require.Contains(t, body, "tap")

	// follow 透传不变：无 shard 键
	code, body = postJSON(t, ts, "/api/follow", `{"handle":"st-tap","follow":"TCP"}`)
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, body, "shard")

	// spool 为空：实时实例永不 load，shard 报空串
	_, ts2, _, _ := newServer(t, nil)
	subscribe(t, ts2, "st-tap-empty")
	code, body = postJSON(t, ts2, "/api/tap", `{"handle":"st-tap-empty","tap":"expert"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "", body["shard"])
}

// TestSubscribeAfterClose503 服务关闭后 /api/subscribe 回 503（服务暂不可用）
// 而非 500。
func TestSubscribeAfterClose503(t *testing.T) {
	s, ts, _, _ := newServer(t, nil)
	require.NoError(t, s.Close())
	code, body := postJSON(t, ts, "/api/subscribe", `{"handle":"st-after-close"}`)
	require.Equal(t, http.StatusServiceUnavailable, code, "body=%v", body)
}

// TestDownloadEmptyShard200 0 字节分片可读：download 200（拼接空段），不再按
// 写出字节数误判为"均不可读"。
func TestDownloadEmptyShard200(t *testing.T) {
	_, ts, spoolDir, _ := newServer(t, nil)
	writeShard(t, spoolDir, "empty.pcapng", time.Unix(1700000000, 0), nil)
	resp, err := ts.Client().Get(ts.URL + "/api/download")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Empty(t, body)
}

// TestSubscribeColdFlag 新建会话回 cold=true，复用回 cold=false：前端据此
// 清空本地视图（会话被淘汰/宽限过期后追帧位置已丢失，新流锚定最新分片）。
func TestSubscribeColdFlag(t *testing.T) {
	_, ts, _, _ := newServer(t, nil)
	code, body := postJSON(t, ts, "/api/subscribe", `{"handle":"cold-1"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, body["cold"], "首次建流应为 cold")

	_, body = postJSON(t, ts, "/api/subscribe", `{"handle":"cold-1"}`)
	require.Equal(t, false, body["cold"], "复用会话不应为 cold")
}
