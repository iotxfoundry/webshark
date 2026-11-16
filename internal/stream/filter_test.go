// filter_test.go - 实时筛选模式行为测试：进入/切换 reset 清视图后自环窗起点
// mtime 升序逐片走查（mode:"filter"，活动分片参与走查且增长重 load 续推）/
// 同 filter 重提 no-op / 退出回普通流（位置跳最新片）/ 非法 filter 错误帧保持
// 原状态 / 走查一次到底（背压即节流）+ 暂停片内游标续推不重不漏。
package stream

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestFilterEnterSweepResubmitExit 锁定筛选主链路：进入 → reset 清视图 → 逐片
// 走查（mode:"filter"，ref.shard=分片名，mtime 升序）→ 轮换后走查续推新片且
// 活动分片参与（增长重 load）→ 同 filter 重提 no-op → 退出回普通流（pos 跳
// 最新片）。
func TestFilterEnterSweepResubmitExit(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	// mtime 全部取过去时刻（不变量：活动片 mtime 恒最新——追加会把 mtime 重置
	// 为墙钟，未来 mtime 会令最新分片判定翻转致重走查假象）
	writeShard(t, spoolDir, "s1.pcapng", base.Add(-9*time.Minute), []byte("a"))
	writeShard(t, spoolDir, "s2.pcapng", base.Add(-6*time.Minute), []byte("b"))
	m := startMemSharkd(t)
	m.setRows("s1.pcapng", 2)
	m.setRows("s2.pcapng", 3)
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	sess := mustSession(t, mgr, "tkf")
	ws := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	// 冷启动：仅最新片 s2 全量
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))

	// 进入筛选：reset → 逐片走查 s1 → s2，全部 mode:"filter"
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcp"}`))
	require.JSONEq(t, `{"type":"reset","reason":"realign"}`,
		string(marshalFrame(t, readUntilType(t, ws, "reset", 5*time.Second))))
	m1 := readUntilType(t, ws, "rows", 5*time.Second)
	require.Equal(t, "filter", m1["mode"])
	require.Equal(t, "s1.pcapng", m1["shard"], "走查 ref.shard=分片名(mtime 升序)")
	require.Equal(t, []float64{1, 2}, rowsNums(t, m1))
	m2 := readUntilType(t, ws, "rows", 5*time.Second)
	require.Equal(t, "s2.pcapng", m2["shard"])
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, m2))
	require.True(t, hasLine(m.written(), `"filter":"tcp"`), "筛选拉取须透传 filter: %v", m.written())
	require.Equal(t, "tcp", sess.Filter())
	readNoFrame(t, ws) // 追平静默（连接读超时弃用）
	_ = ws.Close()

	// 轮换：s3/s4 落盘 → 走查续推 s3；活动分片 s4 参与走查（全量当前内容）。
	// 重连：走查进度（位置）与筛选状态随流，续推不重不漏（首帧可能为 rotated
	// ——attach 与扇出竞态，walk 读到 rows 为止）。
	writeShard(t, spoolDir, "s3.pcapng", base.Add(-3*time.Minute), []byte("c"))
	writeShard(t, spoolDir, "s4.pcapng", base.Add(-1*time.Minute), []byte("d"))
	m.setRows("s3.pcapng", 2)
	m.setRows("s4.pcapng", 1)
	ws2 := dialSession(t, sess)
	// 重连与走查并行：attach 前 worker 可能已推 s3 甚至 s4 行（旧连接销毁丢弃）。
	// 断言只锁走查覆盖：s3 行集与 s4 行集按序最终到达（复合键唯一，不重）。
	m3 := readUntilType(t, ws2, "rows", 10*time.Second)
	require.Equal(t, "filter", m3["mode"])
	shard1, nums1 := m3["shard"].(string), rowsNums(t, m3)
	if shard1 == "s3.pcapng" {
		require.Subset(t, []float64{1, 2}, nums1)
	} else {
		require.Equal(t, "s4.pcapng", shard1, "走查自窗内位置逐片（首行 s3 或续推 s4）")
	}
	var m4 map[string]any
	if shard1 == "s3.pcapng" {
		m4 = readUntilType(t, ws2, "rows", 10*time.Second)
		require.Equal(t, "s4.pcapng", m4["shard"], "活动分片参与走查(新帧滞后 ≤ reload 节流, 非一个轮换周期)")
	} else {
		m4 = m3
	}
	require.Equal(t, []float64{1}, rowsNums(t, m4))

	// 活动分片增长：重 load 后增量续推（冻结快照语义）
	appendShard(t, spoolDir, "s4.pcapng", 64)
	m.setRows("s4.pcapng", 2)
	m4b := readUntilType(t, ws2, "rows", 5*time.Second)
	require.Equal(t, "s4.pcapng", m4b["shard"])
	require.Equal(t, []float64{2}, rowsNums(t, m4b))
	_ = ws2.Close()

	// 同 filter 重提（重连，筛选状态随流）：no-op 不重走查
	ws3 := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws3))
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcp"}`))
	readNoFrame(t, ws3)
	_ = ws3.Close()

	// 退出：回普通单分片流（s4, mode live）
	ws4 := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws4))
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":""}`))
	require.Eventually(t, func() bool { // 退出经 worker tick 取走生效
		return sess.Filter() == ""
	}, 5*time.Second, 20*time.Millisecond, "退出后 filter 应为空")
	liveMsg := readUntilType(t, ws4, "rows", 5*time.Second)
	require.Equal(t, "live", liveMsg["mode"])
	require.Equal(t, "s4.pcapng", liveMsg["shard"], "退出后跳最新分片续推")
	require.Equal(t, []float64{1, 2}, rowsNums(t, liveMsg))
	require.False(t, hasLine(m.written(), `"filter":"tcp","limit"`), "普通段拉取不携 filter")
}

// TestFilterInvalidCheckRejected 锁定 filter 语法校验：轻查询实例 check 失败 →
// 错误帧提示（reason 非空含校验原文）且不进入筛选（无走查拉取），普通流不受影响。
func TestFilterInvalidCheckRejected(t *testing.T) {
	spoolDir := t.TempDir()
	writeShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("a"))
	m := startMemSharkd(t)
	m.setRows("s1.pcapng", 2)
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	sess := mustSession(t, mgr, "tki")
	ws := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	require.Equal(t, []float64{1, 2}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))

	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"bad-filter"}`))
	errMsg := readUntilType(t, ws, "error", 5*time.Second)
	reason, _ := errMsg["reason"].(string)
	require.NotEmpty(t, reason, "error 帧 reason 不得为空")
	require.Contains(t, reason, "filter", "reason 须含校验提示: %q", reason)
	require.Equal(t, "", sess.Filter(), "非法 filter 不进入筛选")
	readNoFrame(t, ws)
	require.False(t, hasLine(m.written(), `"filter":"bad-filter","limit"`), "筛选拉取须未发生: %v", m.written())
}

// TestFilterSwitchResetsView 锁定筛选进入/切换清视图：新进入与切换 filter 均
// 先发 reset 再自环窗起点重走查；重进同 filter（no-op）不发 reset；退出不
// 发 reset（历史视图保留）。
func TestFilterSwitchResetsView(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	writeShard(t, spoolDir, "s1.pcapng", base, []byte("a"))
	writeShard(t, spoolDir, "s2.pcapng", base.Add(time.Minute), []byte("b"))
	m := startMemSharkd(t)
	m.setRows("s1.pcapng", 2)
	m.setRows("s2.pcapng", 3)
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	sess := mustSession(t, mgr, "tkw")
	ws := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))

	// 新进入 filter A：reset → 走查 s1 → s2
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcpA"}`))
	require.JSONEq(t, `{"type":"reset","reason":"realign"}`,
		string(marshalFrame(t, readUntilType(t, ws, "reset", 5*time.Second))))
	mA1 := readUntilType(t, ws, "rows", 10*time.Second)
	require.Equal(t, "filter", mA1["mode"])
	require.Equal(t, "s1.pcapng", mA1["shard"])
	mA2 := readUntilType(t, ws, "rows", 10*time.Second)
	require.Equal(t, "s2.pcapng", mA2["shard"])
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, mA2))

	// 切换 filter B：reset → 重走全窗（s1 → s2）
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcpB"}`))
	require.JSONEq(t, `{"type":"reset","reason":"realign"}`,
		string(marshalFrame(t, readUntilType(t, ws, "reset", 5*time.Second))))
	require.Equal(t, "s1.pcapng", readUntilType(t, ws, "rows", 10*time.Second)["shard"])
	mB := readUntilType(t, ws, "rows", 10*time.Second)
	require.Equal(t, "s2.pcapng", mB["shard"])
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, mB))

	// 重进同 filter：no-op 不发 reset
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcpB"}`))
	readNoFrame(t, ws)
}

// TestFilterSwitchInvalidReason 锁定筛选切换遇非法 filter 的错误帧：reason 非空
// 且含「filter 校验失败」原文，且当前筛选不被破坏（旧 filter 会话保持）。
func TestFilterSwitchInvalidReason(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	writeShard(t, spoolDir, "s1.pcapng", base, []byte("a"))
	writeShard(t, spoolDir, "s2.pcapng", base.Add(time.Minute), []byte("b"))
	m := startMemSharkd(t)
	m.setRows("s1.pcapng", 2)
	m.setRows("s2.pcapng", 3)
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	sess := mustSession(t, mgr, "tkv")
	ws := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))

	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcp"}`))
	require.Equal(t, "s1.pcapng", readUntilType(t, ws, "rows", 10*time.Second)["shard"], "走查自环窗起点逐片")
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, readUntilType(t, ws, "rows", 10*time.Second)))
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"!!bad!!"}`))
	errMsg := readUntilType(t, ws, "error", 5*time.Second)
	reason, _ := errMsg["reason"].(string)
	require.NotEmpty(t, reason, "error 帧 reason 不得为空")
	require.Contains(t, reason, "filter 校验失败", "reason 须含原文: %q", reason)
	require.Equal(t, "tcp", sess.Filter(), "非法切换不破坏当前筛选")
	readNoFrame(t, ws) // 非法切换不进入筛选：无新结果批
}

// TestFilterPauseMidSweep 锁定走查推送一次到底（无人工匀速，背压即节流）+ 暂停
// 续推：慢消费下暂停 → 冻结（位置随会话保留），resume → 从片内游标续推不重不
// 漏；走查全量在时限内送达。
func TestFilterPauseMidSweep(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	writeShard(t, spoolDir, "s1.pcapng", base, []byte("a"))
	writeShard(t, spoolDir, "s2.pcapng", base.Add(time.Minute), []byte("b"))
	m := startMemSharkd(t)
	m.slowFrames = 300 * time.Millisecond // 拉取往返 300ms：暂停必落在 chunk 内（确定性中断点）
	m.setRows("s1.pcapng", 6000)
	m.setRows("s2.pcapng", 6000)
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	sess := mustSession(t, mgr, "tkp")
	ws := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	// 先进筛选再谈其余：pendingFilter 在 worker 首轮唤醒即取走（先于冷启动），
	// 走查是唯一行源（12000 行），暂停/吞吐断言不被 live 冷启动流污染
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcp"}`))
	// 常驻读 goroutine：慢消费（30ms/帧 ≈33 批/s）驱动背压节流，按复合键收集
	frames := make(chan map[string]any, 4096)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if ws.SetReadDeadline(time.Now().Add(30*time.Second)) != nil {
				return // 测试可能已结束（ws 已关）
			}
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) == nil {
				frames <- msg
			}
		}
	}()
	var mu sync.Mutex
	seen := make(map[string]int)
	total := func() int {
		for {
			select {
			case msg := <-frames:
				if msg["type"] == "rows" {
					shard, _ := msg["shard"].(string)
					mu.Lock()
					for _, n := range rowsNums(t, msg) {
						seen[fmt.Sprintf("%s:%d", shard, int(n))]++
					}
					mu.Unlock()
				}
				continue
			default:
			}
			mu.Lock()
			defer mu.Unlock()
			return len(seen)
		}
	}

	enterAt := time.Now()
	require.Eventually(t, func() bool { return total() > 0 }, 5*time.Second, 20*time.Millisecond)

	// 推送启动后暂停（行粒度抢占：生产端 ~32 行+一个出队周期内冻结；队列存量
	// 由消费端继续消化，计数至平台期）
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"capture","state":"pause"}`))
	plateau := 0
	frozenAt := time.Duration(0)
	for d := time.Duration(0); d < 12*time.Second; d += 600 * time.Millisecond {
		time.Sleep(600 * time.Millisecond)
		c := total()
		if c == plateau {
			frozenAt = d
			break
		}
		plateau = c
	}
	c1 := total()
	require.Greater(t, c1, 0, "暂停应落在推送中途")
	require.Less(t, c1, 12000, "暂停应落在走查推送中途")
	require.NotZero(t, frozenAt, "暂停后推送须冻结(计数平台期)")

	// 恢复：从片内游标续推，不重不漏；全量须在时限内送达（无人工匀速）
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"capture","state":"resume"}`))
	require.Eventually(t, func() bool { return total() == 12000 }, 30*time.Second, 50*time.Millisecond,
		"走查全量应背压节流尽快送达")
	require.Less(t, time.Since(enterAt), 15*time.Second, "走查全量送达总时限(定案: 一次推送)")
	mu.Lock()
	dups := 0
	for _, c := range seen {
		if c != 1 {
			dups++
		}
	}
	mu.Unlock()
	require.Zero(t, dups, "行不得重复(跨片复合键)")
	_ = ws.Close()
	<-done
}

// TestFilterSnapshotThroughput 锁定走查推送吞吐（定案：即扫即显，不匀速）：
// 6000 行走查在无人工延迟下尽快送达（背压即唯一节流），总时限 5s——匀速节拍
// （100ms/批）下 ≥18s 必红。
func TestFilterSnapshotThroughput(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	writeShard(t, spoolDir, "s1.pcapng", base, []byte("a"))
	writeShard(t, spoolDir, "s2.pcapng", base.Add(5*time.Minute), []byte("b"))
	m := startMemSharkd(t)
	m.setRows("s1.pcapng", 3000)
	m.setRows("s2.pcapng", 3000)
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	sess := mustSession(t, mgr, "tkz")
	ws := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	// 常驻快速消费：不限速（背压由队列水位承担）
	var mu sync.Mutex
	seen := make(map[string]int)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if ws.SetReadDeadline(time.Now().Add(30*time.Second)) != nil {
				return
			}
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil || msg["type"] != "rows" {
				continue
			}
			shard, _ := msg["shard"].(string)
			mu.Lock()
			for _, f := range rowsNums(t, msg) {
				seen[fmt.Sprintf("%s:%d", shard, int(f))]++
			}
			mu.Unlock()
		}
	}()

	start := time.Now()
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcp"}`))
	ok := require.New(t)
	ok.Eventually(func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) == 6000
	}, 5*time.Second, 50*time.Millisecond, "走查 6000 行应一次推送尽快送达(匀速节拍下 ≥18s)")
	ok.Less(time.Since(start), 5*time.Second, "走查送达超时限(人工匀速残留)")
	mu.Lock()
	dups := 0
	for _, c := range seen {
		if c != 1 {
			dups++
		}
	}
	mu.Unlock()
	ok.Zero(dups, "不重不漏")
	_ = ws.Close()
	<-done
}

// TestFilterLightQueryParallelBackfill 锁定轻查询与后台追帧并行不互扰：后台
// 追帧拉取被拖慢期间（frames 响应 2s），check 走进程级轻查询实例（独立连接）
// 应立即返回——若与追帧同实例则必然排队 ≥2s。
func TestFilterLightQueryParallelBackfill(t *testing.T) {
	spoolDir := t.TempDir()
	writeShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("pcap"))
	m := startMemSharkd(t)
	m.slowFrames = 2 * time.Second
	m.setRows("s1.pcapng", 2000)
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	ws := dialSession(t, mustSession(t, mgr, "tkl"))
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	require.Eventually(t, func() bool {
		return hasLine(m.written(), `"method":"frames"`)
	}, 3*time.Second, 20*time.Millisecond, "追帧应在途")

	// check：不被追帧排队（独立轻查询实例）
	start := time.Now()
	res, err := mgr.Light().Call(context.Background(), "check", map[string]string{"filter": "tcp"})
	require.NoError(t, err)
	require.JSONEq(t, `{"err":0}`, string(res))
	require.Less(t, time.Since(start), 1500*time.Millisecond, "check 不得排队于追帧之后")
	_ = ws.Close()
}

// TestFilterSweepShardOrdering 锁定走查分片顺序：同一 reset 后走查结果按 mtime
// 升序逐片（ref.shard 单调不回退）。
func TestFilterSweepShardOrdering(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	writeShard(t, spoolDir, "a.pcapng", base.Add(-5*time.Minute), []byte("a"))
	writeShard(t, spoolDir, "b.pcapng", base.Add(-3*time.Minute), []byte("b"))
	writeShard(t, spoolDir, "c.pcapng", base.Add(-1*time.Minute), []byte("c"))
	m := startMemSharkd(t)
	for _, name := range []string{"a.pcapng", "b.pcapng", "c.pcapng"} {
		m.setRows(name, 2)
	}
	mgr := newManager(t, m.ln.Addr().String(), spoolDir)
	sess := mustSession(t, mgr, "tko2")
	ws := dialSession(t, sess)
	require.Equal(t, `{"type":"hello","mode":"live"}`, readFrameString(t, ws))
	require.Equal(t, []float64{1, 2}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second))) // 冷启动 c

	HandleCtl(sess, []byte(`{"type":"_ctl","op":"filter","filter":"tcp"}`))
	require.True(t, readIsReset(t, ws))
	order := sweepOrder(t, ws, []string{"a.pcapng", "b.pcapng", "c.pcapng"})
	require.Equal(t, []string{"a.pcapng", "b.pcapng", "c.pcapng"}, order, "走查须 mtime 升序逐片")
}

// readIsReset 读到一帧并断言 reset。
func readIsReset(t *testing.T, ws *websocket.Conn) bool {
	t.Helper()
	msg := readUntilType(t, ws, "reset", 5*time.Second)
	reason, _ := msg["reason"].(string)
	return strings.HasSuffix(reason, "realign")
}

// sweepOrder 顺序收集 rows 帧 shard 序列（直到窗内分片全部出现）。gorilla 读
// 超时后连接不可复用——本函数单 deadline 常驻读，超时即收尾。
func sweepOrder(t *testing.T, ws *websocket.Conn, want []string) []string {
	t.Helper()
	wantSet := map[string]bool{}
	for _, s := range want {
		wantSet[s] = true
	}
	var order []string
	deadline := time.Now().Add(10 * time.Second)
	require.NoError(t, ws.SetReadDeadline(deadline))
	for time.Now().Before(deadline) {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break // 读超时/断开：已收部分即结果
		}
		var msg map[string]any
		require.NoError(t, json.Unmarshal(data, &msg))
		if msg["type"] != "rows" {
			continue
		}
		shard, _ := msg["shard"].(string)
		if wantSet[shard] && (len(order) == 0 || order[len(order)-1] != shard) {
			order = append(order, shard)
		}
		if len(order) == len(want) {
			return order
		}
	}
	return order
}
