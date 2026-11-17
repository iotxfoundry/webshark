// stream_api_test.go - 流接口契约：/api/frame 一律走分片缓存实例、/api/tap
// 响应补注实际载入分片、Manager.Close 后 subscribe 503、0 字节分片 download
// 200、tap 明细截断（maxTapDetails + truncated）。
package server

import (
	"fmt"
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
	writeShard(t, spoolDir, "live.pcapng", base, make([]byte, 32))
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

// TestTapDetailsCapped 锁定 tap 明细截断：单次响应明细按帧序最多
// maxTapDetails 条（跨 tap 共享预算），丢弃总数经顶层 truncated 键披露；
// 未截断时 truncated 为 0 且明细原样保留。
func TestTapDetailsCapped(t *testing.T) {
	t.Run("超限截断为前5000条并报truncated", func(t *testing.T) {
		_, ts, _, sock := newServer(t, nil)
		sock.tapDetails = 6000
		subscribe(t, ts, "st-tap-cap")
		code, body := postJSON(t, ts, "/api/tap", `{"handle":"st-tap-cap","tap0":"expert"}`)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, float64(1000), body["truncated"], "丢弃 6000-5000=1000 条")
		taps := body["taps"].([]any)
		require.Len(t, taps, 1)
		require.Len(t, taps[0].(map[string]any)["details"], maxTapDetails)
		require.Contains(t, body, "shard") // 既有 shard 补注保留
	})

	t.Run("未截断truncated为0且明细全保留", func(t *testing.T) {
		_, ts, _, sock := newServer(t, nil)
		sock.tapDetails = 2
		subscribe(t, ts, "st-tap-small")
		code, body := postJSON(t, ts, "/api/tap", `{"handle":"st-tap-small","tap0":"expert"}`)
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, float64(0), body["truncated"])
		taps := body["taps"].([]any)
		require.Len(t, taps[0].(map[string]any)["details"], 2)
	})
}

// TestFrameOutOfRangeShardContentReplaced 锁定「分片内容被覆盖」的降级契约：
// 旧行引用的分片在磁盘上被同名覆盖（dumpcap 重启会重写同名分片）后，load 仍
// 成功但帧号越界（sharkd -8003）→ 404 + 明确中文说明（含覆盖/清理原因），
// 而非透传 sharkd 英文原文让使用者无从判断。
func TestFrameOutOfRangeShardContentReplaced(t *testing.T) {
	_, ts, spoolDir, _ := newServer(t, nil)
	writeShard(t, spoolDir, "live.pcapng", time.Unix(1700000000, 0), make([]byte, 32))
	ws, err := dialWS(t, ts, nil, "handle=st-oob")
	require.NoError(t, err)
	readFrame(t, ws) // hello
	require.Eventually(t, func() bool {
		_, body := postJSON(t, ts, "/api/subscribe", `{"handle":"st-oob"}`)
		return body["shard"] == "live.pcapng"
	}, 10*time.Second, 50*time.Millisecond, "实时实例未就绪")

	code, body := getJSON(t, ts, "/api/frame?handle=st-oob&shard=live.pcapng&num=77478")
	require.Equal(t, http.StatusNotFound, code)
	require.Contains(t, body["message"], "覆盖", "应说明分片内容被覆盖的成因")
	require.Equal(t, float64(-8003), body["code"], "透传 sharkd 错误码供宿主判断")
}

// TestFrameCacheReloadsGrownShard 锁定活动分片增长的详情可用性：分片缓存实例
// 驻留的是 load 时刻的快照，活动分片持续追加后快照帧数已落后——缓存命中必须
// 按「文件尺寸变化即重 load」刷新快照，否则新推送行的帧详情全部越界 404。
func TestFrameCacheReloadsGrownShard(t *testing.T) {
	_, ts, spoolDir, _ := newServer(t, nil)
	name := "grow.pcapng"
	writeShard(t, spoolDir, name, time.Unix(1700000000, 0), make([]byte, 100)) // 100 帧
	subscribe(t, ts, "st-grow")
	code, body := getJSON(t, ts, fmt.Sprintf("/api/frame?handle=st-grow&shard=%s&num=70", name))
	require.Equal(t, http.StatusOK, code, "初次查询（快照 100 帧）应成功: %v", body)

	// 分片追加到 200 帧（同名覆盖写，模拟 dumpcap 持续落盘）
	writeShard(t, spoolDir, name, time.Unix(1700000000, 0), make([]byte, 200))
	code, body = getJSON(t, ts, fmt.Sprintf("/api/frame?handle=st-grow&shard=%s&num=150", name))
	require.Equal(t, http.StatusOK, code, "增长后查询应触发重 load 而非命中旧快照: %v", body)
	require.Equal(t, float64(150), body["num"])
}

// TestStatusProbeReusesConnection 锁定探活常驻会话：/api/status 不再「拨号即
// 关」（sharkd daemon 每连接 fork 一子进程，高频探活会堆积僵尸子进程）——
// 前端每 5s 轮询一次，多次 status 只应建立 1 条 sharkd 连接。
func TestStatusProbeReusesConnection(t *testing.T) {
	base := time.Unix(1700000000, 0)
	_, ts, spoolDir, sock := newServer(t, nil)
	writeShard(t, spoolDir, "live.pcapng", base, make([]byte, 32))
	for i := 0; i < 3; i++ {
		code, body := getJSON(t, ts, "/api/status")
		require.Equal(t, http.StatusOK, code)
		require.Equal(t, true, body["connected"], "假 sharkd 在听，应可达")
	}
	require.Equal(t, 1, sock.conns, "多次 status 应复用同一条 sharkd 连接（每次连接 fork 一子进程）")
}

// connCount 统计 memSharkd 已接受的连接数（每次拨号一条，sharkd 每连接 fork
// 一个子进程——连接数即 fork 数）。
func connCount(t *testing.T, shard string) int {
	t.Helper()
	return 0
}
