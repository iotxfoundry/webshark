// worker_test.go - 实时推送流行为测试：每流 worker（冷启动自最新分片起 + 活动
// 分片增量 + 轮换增量 + 断线续推 + 出窗 reset 重推 + 增长重 load + 大拉取块 +
// skip 语义）/ 队列背压与销毁唤醒 / writer 顶替与断线唤醒 / 流上限。
package stream

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/iotxfoundry/webshark/internal/testutil"
)

// TestLiveInitialAndRotation 锁定初始全量对齐 + 活动分片增量 + 轮换增量：
// attach 后初始推最新分片全部行（带 ref 复合键）；活动分片增长按游标增量推送
// （无 rotated）；新分片落盘推 rotated 通知 + 新片行；frames 请求带 skip 游标；
// 冷启动不回放更早分片。
func TestLiveInitialAndRotation(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	testutil.WriteShard(t, spoolDir, "s0.pcapng", base, []byte("pcap")) // 更早分片：不进冷启动
	testutil.WriteShard(t, spoolDir, "s1.pcapng", base.Add(time.Minute), []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s0.pcapng", 2)
	m.SetRows("s1.pcapng", 2)
	mgr := newManager(t, m.Addr(), spoolDir)
	ws, _ := dialSession(t, mustSession(t, mgr, "tk1"), true)

	// 握手确认 + 冷启动（仅最新片）：hello → rotated s1 → rows s1（行透传 + ref 复合键）
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	require.JSONEq(t, `{"type":"rotated","shard":"s1.pcapng"}`,
		string(marshalFrame(t, readUntilType(t, ws, "rotated", 5*time.Second))))
	rowsMsg := readUntilType(t, ws, "rows", 5*time.Second)
	require.Equal(t, "live", rowsMsg["mode"])
	require.Equal(t, "s1.pcapng", rowsMsg["shard"], "冷启动只推最新分片(不回放 s0 历史)")
	require.Equal(t, []float64{1, 2}, rowsNums(t, rowsMsg))
	row0 := rowsMsg["rows"].([]any)[0].(map[string]any)
	require.Contains(t, row0, "time", "sharkd 行字段透传")

	// 活动分片增长：游标增量推送，无 rotated
	appendShard(t, spoolDir, "s1.pcapng", 64)
	m.SetRows("s1.pcapng", 3)
	rowsMsg = readUntilType(t, ws, "rows", 5*time.Second)
	require.Equal(t, "s1.pcapng", rowsMsg["shard"])
	require.Equal(t, []float64{3}, rowsNums(t, rowsMsg), "活动分片增量只含新行")

	// 轮换：新分片落盘 → rotated（新片名）+ 新片全量行
	testutil.WriteShard(t, spoolDir, "s2.pcapng", base.Add(2*time.Minute), []byte("pcap"))
	m.SetRows("s2.pcapng", 2)
	require.JSONEq(t, `{"type":"rotated","shard":"s2.pcapng"}`,
		string(marshalFrame(t, readUntilType(t, ws, "rotated", 5*time.Second))))
	rowsMsg = readUntilType(t, ws, "rows", 5*time.Second)
	require.Equal(t, "s2.pcapng", rowsMsg["shard"])
	require.Equal(t, []float64{1, 2}, rowsNums(t, rowsMsg))

	// sharkd 侧：load 补全 spool 绝对路径（s1 与 s2）；更早的 s0 零 load（不回放历史）
	recv := m.Written()
	for _, name := range []string{"s1.pcapng", "s2.pcapng"} {
		require.True(t, countLoadOf(recv, name) > 0, "应收到 %s 的 load(绝对路径): %v", name, recv)
	}
	require.Zero(t, countLoadOf(recv, "s0.pcapng"), "冷启动不回放历史片: %v", recv)
	require.True(t, hasLine(recv, `"method":"frames"`), "应发出 frames 请求: %v", recv)
	readNoFrame(t, ws) // 收尾：追平后静默
}

// TestLiveReconnectResume 锁定断线续推：追帧位置随流保存，断开期间不推（位置
// 冻结），同 handle 重连后从游标续推无重无漏。
func TestLiveReconnectResume(t *testing.T) {
	spoolDir := t.TempDir()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 4)
	mgr := newManager(t, m.Addr(), spoolDir)
	sess := mustSession(t, mgr, "tk2")

	ws1, _ := dialSession(t, sess, true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws1))
	rowsMsg := readUntilType(t, ws1, "rows", 5*time.Second)
	require.Equal(t, []float64{1, 2, 3, 4}, rowsNums(t, rowsMsg)) // 初始全量对齐（单批）
	srv1 := srvOf(t, sess)
	require.NoError(t, ws1.Close()) // 断线（位置=分片s1 游标4）
	sess.Detach(srv1)
	_ = srv1.Close() // 服务端侧同步关闭（writerLoop 随 PopFor false 退出）
	appendShard(t, spoolDir, "s1.pcapng", 96)
	m.SetRows("s1.pcapng", 6)          // 断开期间落盘增长
	time.Sleep(600 * time.Millisecond) // 断开窗口跨过拉取节拍

	ws2, _ := dialSession(t, sess, true) // 同 handle 重连
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws2))
	rowsMsg = readUntilType(t, ws2, "rows", 5*time.Second)
	require.Equal(t, []float64{5, 6}, rowsNums(t, rowsMsg), "续推无重(不含1-4)无漏(补齐5,6)")
}

// TestBackpressure 锁定阻塞背压：慢消费（逐帧读+歇 20ms）下，冷启动最新分片
// 全量行经背压节流不丢不重全部送达，全程无 reset 帧——对有界源（环窗）正确机制
// 是生产端等消费端，而非丢帧重推（重推对滑动环窗是追不上的活锁）。
func TestBackpressure(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	m := startMemSharkd(t)
	rowsPer := map[string]int{"s4.pcapng": 4000}
	testutil.WriteShard(t, spoolDir, "s4.pcapng", base.Add(time.Minute), []byte("p"))
	m.SetRows("s4.pcapng", rowsPer["s4.pcapng"])
	mgr := newManager(t, m.Addr(), spoolDir)

	type stamped struct {
		typ   string
		shard string
		nums  []float64
	}
	var mu sync.Mutex
	var recs []stamped
	sess := mustSession(t, mgr, "tkb")
	ws, _ := dialSession(t, sess, true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	go func() { // 慢消费：逐帧歇 20ms（≈50 批/s）
		for {
			if ws.SetReadDeadline(time.Now().Add(90*time.Second)) != nil {
				return // 测试可能已结束（ws 已关）
			}
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			st := stamped{typ: msg["type"].(string)}
			if st.typ == "rows" {
				st.shard, _ = msg["shard"].(string)
				st.nums = rowsNums(t, msg)
			}
			mu.Lock()
			recs = append(recs, st)
			mu.Unlock()
			time.Sleep(20 * time.Millisecond)
		}
	}()

	// 冷启动只回放最新分片 s4（4000 行=125 批）：背压节流下有限时间全部送达，
	// 按 ref 复合键（分片:num）计数，不重不漏，全程无 reset 帧。
	counts := make(map[string]int)
	seen := 0
	resets := 0
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for ; seen < len(recs); seen++ {
			r := recs[seen]
			if r.typ == "reset" {
				resets++
				continue
			}
			if r.typ != "rows" {
				continue
			}
			for _, f := range r.nums {
				counts[fmt.Sprintf("%s:%d", r.shard, int(f))]++
			}
		}
		done := len(counts) == rowsPer["s4.pcapng"]
		mu.Unlock()
		if done {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, rowsPer["s4.pcapng"], len(counts), "最新分片全量须在时限内送达(背压不丢帧)")
	for key := range counts {
		require.True(t, strings.HasPrefix(key, "s4.pcapng:"), "冷启动只推最新分片: %s", key)
	}
	require.Zero(t, resets, "背压模式下不得出现 reset 帧")
	dups := 0
	for _, c := range counts {
		if c != 1 {
			dups++
		}
	}
	require.Zero(t, dups, "背压送达不重不漏")
}

// TestRingWindowAlignment 锁定环窗语义：spool 遗留代分片不影响最新分片判定——
// 冷启动锚点仍为当代 mtime 最新片；冷启动只触窗首，其余当代片与老片零 load 零行
// （历史不回放）。
func TestRingWindowAlignment(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	m := startMemSharkd(t)
	for i := 1; i <= 4; i++ { // 遗留代：昨日 mtime
		name := fmt.Sprintf("net_old%02d.pcapng", i)
		testutil.WriteShard(t, spoolDir, name, base.Add(-24*time.Hour), []byte("old"))
		m.SetRows(name, 1)
	}
	for i := 1; i <= 16; i++ { // 当代 16 片：mtime 升序
		name := fmt.Sprintf("net_%04d.pcapng", i)
		testutil.WriteShard(t, spoolDir, name, base.Add(time.Duration(i)*time.Minute), []byte("cur"))
		m.SetRows(name, 1)
	}
	mgr := newManager(t, m.Addr(), spoolDir)
	sess := mustSession(t, mgr, "tkr")
	ws, _ := dialSession(t, sess, true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	require.Eventually(t, func() bool { // 实时实例就绪（当前分片=最新 mtime）
		return sess.Shard() == "net_0016.pcapng"
	}, 5*time.Second, 20*time.Millisecond)

	// 冷启动只触窗首 net_0016：其余当代片与老片零 load 零行
	shards := make(map[string]bool)
	require.Eventually(t, func() bool {
		for {
			msg := readFrameNonBlock(t, ws)
			if msg == nil {
				break
			}
			if msg["type"] == "rows" {
				for s := range rowsShards(t, msg) {
					shards[s] = true
				}
			}
		}
		return len(shards) == 1
	}, 10*time.Second, 50*time.Millisecond)
	require.Equal(t, map[string]bool{"net_0016.pcapng": true}, shards, "冷启动只推窗首最新片: %v", shards)
	for _, ln := range m.Written() {
		if strings.Contains(ln, `"method":"load"`) {
			require.Contains(t, ln, "net_0016.pcapng", "只 load 窗首最新片: %s", ln)
		}
	}
}

// TestEnqueueBackpressureAndShutdown 锁定入队背压与销毁唤醒：队列满时 Enqueue
// 阻塞等待消费腾位（不丢帧）；会话 shutdown（cancel+Broadcast）即时唤醒阻塞的
// 生产端，workerLoop 随 ctx 退出（阻塞在 Enqueue 的 goroutine 不悬挂）。
func TestEnqueueBackpressureAndShutdown(t *testing.T) {
	mgr := newManager(t, filepath.Join(t.TempDir(), "absent.sock"), t.TempDir())
	sess, err := mgr.GetOrCreate("tkq")
	require.NoError(t, err)

	frame := RowsFrame("live", "s1.pcapng", nil)
	for range maxOutQueue { // 灌满队列（无消费者：队列恒满）
		sess.Enqueue(frame)
	}
	done := make(chan struct{})
	go func() { // 满队后的下一次入队：阻塞直至腾位或销毁
		sess.Enqueue(frame)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("满队 Enqueue 应阻塞, 不应丢帧或立即成功")
	case <-time.After(300 * time.Millisecond):
	}
	sess.shutdown() // cancel + Broadcast：阻塞端即时唤醒（帧随会话作废）
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 2*time.Second, 10*time.Millisecond, "shutdown 须唤醒阻塞的 Enqueue")
	require.Eventually(t, func() bool { // worker 随 ctx 退出
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		return !strings.Contains(string(buf[:n]), "stream.(*Manager).workerLoop")
	}, 3*time.Second, 50*time.Millisecond)
}

// TestGrowthPullLatency 锁定增长可见延迟（活动分片增长：reload 节流 1s + 节拍
// 150ms + 冲刷 80ms）：新行须在 1.5s 内送达（load=内存快照，不 reload 增长永不可见）。
func TestGrowthPullLatency(t *testing.T) {
	spoolDir := t.TempDir()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 4)
	mgr := newManager(t, m.Addr(), spoolDir)
	ws, _ := dialSession(t, mustSession(t, mgr, "tkg"), true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	require.Equal(t, []float64{1, 2, 3, 4}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))

	delivered := 4
	for range 3 { // 3 轮增长：落盘 append + 行集增长 → reload 节流 1s 内可见
		delivered++
		mark := time.Now()
		appendShard(t, spoolDir, "s1.pcapng", 64)
		m.SetRows("s1.pcapng", delivered)
		rowsMsg := readUntilType(t, ws, "rows", 3*time.Second)
		require.Equal(t, []float64{float64(delivered)}, rowsNums(t, rowsMsg))
		require.Less(t, time.Since(mark), 1500*time.Millisecond, "增长可见延迟超标(reload 节流+节拍+冲刷)")
	}
}

// TestHistoricalShardNoReload 锁定历史片不可变语义：非活动分片不被冷启动回放
// （零 load），外部追加亦不触发重 load 不产新行（历史片本不可变；追加属异常
// 路径，诚实忽略）。
func TestHistoricalShardNoReload(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", base, []byte("a"))
	testutil.WriteShard(t, spoolDir, "s2.pcapng", base.Add(time.Minute), []byte("b"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 2)
	m.SetRows("s2.pcapng", 1)
	mgr := newManager(t, m.Addr(), spoolDir)
	ws, _ := dialSession(t, mustSession(t, mgr, "tkn"), true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	require.Equal(t, []float64{1}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))

	// 历史片 s1 外部追加：不重 load 不出新行（活动片为 s2）
	appendShard(t, spoolDir, "s1.pcapng", 128)
	m.SetRows("s1.pcapng", 5)
	readNoFrame(t, ws)
	require.Zero(t, countLoadOf(m.Written(), "s1.pcapng"), "历史片零 load(冷启动不回放+追加不触发): %v", m.Written())
}

// TestLargePullChunks 锁定大拉取块（pullLimit=2000）：sharkd frames 的 skip 每次
// 从第 1 行重扫（成本 O(游标)，与 limit 无关）——大拉取把重扫次数降到最少。2000
// 行分片须 ≤2 次调用拉完（1 次数据 + 1 次追平探针），且请求带 limit=2000。
func TestLargePullChunks(t *testing.T) {
	spoolDir := t.TempDir()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 2000)
	mgr := newManager(t, m.Addr(), spoolDir)
	ws, _ := dialSession(t, mustSession(t, mgr, "tkL"), true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	seen := make(map[float64]bool)
	require.Eventually(t, func() bool {
		require.NoError(t, ws.SetReadDeadline(time.Now().Add(2*time.Second)))
		_, data, err := ws.ReadMessage()
		if err != nil {
			return false
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil || msg["type"] != "rows" {
			return false
		}
		for _, f := range rowsNums(t, msg) {
			seen[f] = true
		}
		return len(seen) == 2000
	}, 30*time.Second, 50*time.Millisecond, "2000 行应推完")

	var calls []string
	for _, l := range m.Written() {
		if strings.Contains(l, `"method":"frames"`) {
			calls = append(calls, l)
		}
	}
	// 进度调用 = 可能返回行的调用（无 skip 或 skip<2000）；节拍空闲探针
	// （skip=游标，0 行）按设计存在不计。2000 行须恰 1 次进度调用拉完。
	progress := 0
	for _, l := range calls {
		if !strings.Contains(l, `"skip":`) {
			progress++
			continue
		}
		if !strings.Contains(l, `"skip":2000`) {
			progress++
		}
	}
	require.Equal(t, 1, progress, "2000 行须恰 1 次进度调用(大拉取块): %v", calls)
	require.NotEmpty(t, calls)
	require.Contains(t, calls[0], `"limit":2000`, "拉取块上限=2000: %s", calls[0])
}

// TestFramesSkipSemantics 锁定 frames skip 协议语义（真实 sharkd UINTEGER 约束：
// skip 须 >0，起始拉取须省略=从第 1 行）：起始拉取请求不含 skip 键；游标推进后
// 增量拉取请求含 skip=游标值。memSharkd 对 skip≤0 回 -32600，协议违约即拉取全废。
func TestFramesSkipSemantics(t *testing.T) {
	spoolDir := t.TempDir()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 3)
	mgr := newManager(t, m.Addr(), spoolDir)
	ws, _ := dialSession(t, mustSession(t, mgr, "tks"), true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	require.Equal(t, []float64{1, 2, 3}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))

	// 起始拉取：frames 请求不含 skip 键
	var frames []string
	for _, l := range m.Written() {
		if strings.Contains(l, `"method":"frames"`) {
			frames = append(frames, l)
		}
	}
	require.NotEmpty(t, frames)
	require.NotContains(t, frames[0], `"skip"`, "起始拉取须省略 skip: %s", frames[0])

	// 游标推进后：增量拉取请求含 skip=游标值（已推 3 行；落盘 append 触发活动片 reload）
	appendShard(t, spoolDir, "s1.pcapng", 64)
	m.SetRows("s1.pcapng", 5)
	require.Equal(t, []float64{4, 5}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second)))
	require.Eventually(t, func() bool {
		frames = nil
		for _, l := range m.Written() {
			if strings.Contains(l, `"method":"frames"`) {
				frames = append(frames, l)
			}
		}
		return len(frames) > 1 && strings.Contains(frames[len(frames)-1], `"skip":3`)
	}, 5*time.Second, 20*time.Millisecond, "增量拉取须带 skip=3: %v", frames)
}

// TestFramesColumnsSpec 锁定 frames 显式列集协议（sharkd column0..N 参数，值=
// Wireshark col_format 枚举编号）：每个 frames 请求必携 column0..column7 且编
// 号固定——显式列集消除对 sharkd profile 默认列的隐性依赖，服务端推送行 c[]
// 列序对前端是契约。
func TestFramesColumnsSpec(t *testing.T) {
	spoolDir := t.TempDir()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 2)
	mgr := newManager(t, m.Addr(), spoolDir)
	ws, _ := dialSession(t, mustSession(t, mgr, "tkc"), true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	readUntilType(t, ws, "rows", 5*time.Second)
	require.Eventually(t, func() bool { return hasLine(m.Written(), `"method":"frames"`) },
		5*time.Second, 20*time.Millisecond)

	want := []struct{ key, val string }{
		{"column0", "32"}, // %m No.
		{"column1", "36"}, // %Rt 相对秒
		{"column2", "0"},  // %Yt 绝对日期时间
		{"column3", "37"}, // %s 源
		{"column4", "11"}, // %d 目的
		{"column5", "35"}, // %p 协议
		{"column6", "34"}, // %L 长度
		{"column7", "25"}, // %i Info
	}
	for _, l := range m.Written() {
		if !strings.Contains(l, `"method":"frames"`) {
			continue
		}
		for _, c := range want {
			require.Contains(t, l, fmt.Sprintf(`%q:%q`, c.key, c.val), "frames 请求列集: %s", l)
		}
		require.NotContains(t, l, `"column8"`, "列集恰 8 列: %s", l)
	}
}

// TestPauseResumeBackfill 锁定流控制：pause 冻结（不追新片不推增量，落盘不受
// 影响），resume 后冻结期间轮换/增长的分片逐片补推（历史连续）再回实时。冻结期
// 零推送以"短超时读探针超时"证明（该连接读错不复用，resume 断言走重连）。
func TestPauseResumeBackfill(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", base, []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 2)
	mgr := newManager(t, m.Addr(), spoolDir)
	sess := mustSession(t, mgr, "tk4")

	ws1, _ := dialSession(t, sess, true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws1))
	require.Equal(t, []float64{1, 2}, rowsNums(t, readUntilType(t, ws1, "rows", 5*time.Second)))

	// pause：冻结期活动分片增长 + 新分片轮换均不追不推
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"capture","state":"pause"}`))
	time.Sleep(100 * time.Millisecond)
	appendShard(t, spoolDir, "s1.pcapng", 96)
	m.SetRows("s1.pcapng", 5)           // 冻结期 s1（活动片）落盘增长：resume 时 reload 补推
	time.Sleep(1200 * time.Millisecond) // 冻结期跨过检测与拉取窗口
	readNoFrame(t, ws1)                 // 冻结期零推送（连接弃用）

	// resume（重连，暂停态随流）：s1 仍为活动片 → reload → 增量 3-5 补推
	ws2, _ := dialSession(t, sess, true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws2))
	HandleCtl(sess, []byte(`{"type":"_ctl","op":"capture","state":"resume"}`))
	rowsMsg := readUntilType(t, ws2, "rows", 5*time.Second)
	require.Equal(t, "s1.pcapng", rowsMsg["shard"])
	require.Equal(t, []float64{3, 4, 5}, rowsNums(t, rowsMsg), "冻结期 s1 增量补推")

	// 轮换：s2 落盘 → rotated + 新片全量（冻结期轮换补推语义）
	testutil.WriteShard(t, spoolDir, "s2.pcapng", base.Add(time.Minute), []byte("pcap"))
	m.SetRows("s2.pcapng", 2)
	require.JSONEq(t, `{"type":"rotated","shard":"s2.pcapng"}`,
		string(marshalFrame(t, readUntilType(t, ws2, "rotated", 5*time.Second))))
	rowsMsg = readUntilType(t, ws2, "rows", 5*time.Second)
	require.Equal(t, "s2.pcapng", rowsMsg["shard"])
	require.Equal(t, []float64{1, 2}, rowsNums(t, rowsMsg), "冻结期轮换分片补推")
}

// TestOutOfRingWindowReset 锁定位置出环窗：追帧位置锚定的分片被滚动清理（新片
// 落盘挤出环窗）后，worker 发 reset 并从当前状态全量重推。
func TestOutOfRingWindowReset(t *testing.T) {
	spoolDir := t.TempDir()
	base := time.Now()
	m := startMemSharkd(t)
	// 环窗压到 2 片：位置片须被 2 片新文件挤出窗
	mgr := NewManager(Config{Sock: m.Addr(), SpoolDir: spoolDir, RingSize: 2})
	t.Cleanup(mgr.Close)
	for i := 1; i <= 2; i++ {
		name := fmt.Sprintf("r%d.pcapng", i)
		testutil.WriteShard(t, spoolDir, name, base.Add(time.Duration(i)*time.Minute), []byte("p"))
		m.SetRows(name, 1)
	}
	sess := mustSession(t, mgr, "tko")
	ws, _ := dialSession(t, sess, true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws))
	require.Equal(t, []float64{1}, rowsNums(t, readUntilType(t, ws, "rows", 5*time.Second))) // 位置=r2

	// r3/r4 落盘：先按增量轮换消化（位置推进到窗内 r4），常驻读收集（连接不
	// 逐条 SetReadDeadline——gorilla 读超时后连接不可复用）
	frames := make(chan []byte, 64)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			if ws.SetReadDeadline(time.Now().Add(10*time.Second)) != nil {
				return
			}
			_, data, err := ws.ReadMessage()
			if err != nil {
				return
			}
			frames <- data
		}
	}()
	// 位置出窗须位置片被 2 片新文件挤出：r3/r4 按增量轮换消化（pos→r4），r5/r6
	// 同时落盘（单次事件批）→ 首轮走查即见窗=r5,r6 而位置 r4 出窗 → reset。
	// 事件扇出时序竞态：r5 单独消化时窗仍含 r4（增量轮换无 reset），随后 r6
	// 触发走查快照核对——两种路径都收敛到 reset，断言只锁最终集。
	testutil.WriteShard(t, spoolDir, "r3.pcapng", base.Add(3*time.Minute), []byte("p"))
	m.SetRows("r3.pcapng", 1)
	testutil.WriteShard(t, spoolDir, "r4.pcapng", base.Add(4*time.Minute), []byte("p"))
	m.SetRows("r4.pcapng", 2)
	testutil.WriteShard(t, spoolDir, "r5.pcapng", base.Add(5*time.Minute), []byte("p"))
	m.SetRows("r5.pcapng", 1)
	testutil.WriteShard(t, spoolDir, "r6.pcapng", base.Add(6*time.Minute), []byte("p"))
	m.SetRows("r6.pcapng", 2)
	require.Eventually(t, func() bool { return sess.Shard() == "r6.pcapng" }, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		sess.outMu.Lock()
		defer sess.outMu.Unlock()
		return len(sess.outQ) == 0
	}, 5*time.Second, 20*time.Millisecond)
	time.Sleep(500 * time.Millisecond) // reset 对齐批落地

	var resetSeen, r5rows, r6rows bool
	var extraReset int
drainFrames:
	for {
		select {
		case data := <-frames:
			var msg map[string]any
			require.NoError(t, json.Unmarshal(data, &msg))
			switch msg["type"] {
			case "reset":
				if resetSeen {
					extraReset++
				}
				resetSeen = true
				require.Equal(t, "realign", msg["reason"], "reset reason")
			case "rows":
				switch msg["shard"] {
				case "r5.pcapng":
					require.Equal(t, []float64{1}, rowsNums(t, msg))
					r5rows = true
				case "r6.pcapng":
					require.Equal(t, []float64{1, 2}, rowsNums(t, msg))
					r6rows = true
				}
			}
		case <-readerDone:
			t.Fatal("读循环意外退出")
		default:
			break drainFrames
		}
	}
	require.True(t, resetSeen, "位置出环窗须发 reset 帧")
	require.True(t, r5rows, "重推 r5")
	require.True(t, r6rows, "重推 r6")
	require.LessOrEqual(t, extraReset, 1, "重复 reset 至多一次（事件批时序竞态）")
}

// TestAttachTakeover 锁定同 handle 二次接入顶替：旧连接被接管（关闭），新连接
// 从保存位置续推不重不漏。
func TestAttachTakeover(t *testing.T) {
	spoolDir := t.TempDir()
	testutil.WriteShard(t, spoolDir, "s1.pcapng", time.Now(), []byte("pcap"))
	m := startMemSharkd(t)
	m.SetRows("s1.pcapng", 4)
	mgr := newManager(t, m.Addr(), spoolDir)
	sess := mustSession(t, mgr, "tkd")

	ws1, _ := dialSession(t, sess, true)
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, ws1))
	require.Equal(t, []float64{1, 2, 3, 4}, rowsNums(t, readUntilType(t, ws1, "rows", 5*time.Second)))

	// 二次接入（不关旧连接）：Attach 返回旧连接；旧 writer 挂起在 PopFor 即时退出（不悬挂）
	_, old := dialSession(t, sess, false)
	require.NotNil(t, old, "Attach 应返回被顶替的旧连接")
	require.Equal(t, `{"type":"hello","mode":"live"}`, testutil.ReadFrame(t, wsOf(t, sess)))
	appendShard(t, spoolDir, "s1.pcapng", 64)
	m.SetRows("s1.pcapng", 5)
	require.Equal(t, []float64{5}, rowsNums(t, readUntilType(t, wsOf(t, sess), "rows", 5*time.Second)),
		"接管后新连接续推；旧连接不再收帧")

	require.Eventually(t, func() bool { // 旧 writer 退出（不悬挂至回收）
		buf := make([]byte, 1<<20)
		n := runtime.Stack(buf, true)
		return strings.Count(string(buf[:n]), "stream.writerLoop") == 1
	}, 3*time.Second, 50*time.Millisecond)
}

// TestStreamCap 锁定流数上限：全部会话已连接（真占用）时，超过 maxStreams 的
// 建流返回 ErrStreamLimit（未连接的停放会话可被淘汰，见 TestStreamCapEvictsDetached）。
func TestStreamCap(t *testing.T) {
	mgr := newManager(t, filepath.Join(t.TempDir(), "absent.sock"), t.TempDir())
	for i := range maxStreams {
		s, err := mgr.GetOrCreate(fmt.Sprintf("cap-%d", i))
		require.NoError(t, err)
		require.NotNil(t, s)
		s.Attach(&websocket.Conn{}) // 零值指针仅作 ws != nil 判据（不读写）
	}
	_, err := mgr.GetOrCreate("over")
	require.ErrorIs(t, err, ErrStreamLimit)
	// 幂等：既有流仍可取
	s, err := mgr.GetOrCreate("cap-0")
	require.NoError(t, err)
	require.Equal(t, "cap-0", s.Handle())
}

// TestLookup 锁定 Lookup 仅查找（不建流）；不存在返回 nil。
func TestLookup(t *testing.T) {
	mgr := newManager(t, filepath.Join(t.TempDir(), "absent.sock"), t.TempDir())
	require.Nil(t, mgr.Lookup("absent"))
	sess, err := mgr.GetOrCreate("tk")
	require.NoError(t, err)
	require.Same(t, sess, mgr.Lookup("tk"))
}

// --- 小工具 ---

// readFrameNonBlock 非阻塞读一帧（无帧返回 nil）——收集型断言用。
func readFrameNonBlock(t *testing.T, ws *websocket.Conn) map[string]any {
	t.Helper()
	require.NoError(t, ws.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
	_, data, err := ws.ReadMessage()
	if err != nil {
		time.Sleep(20 * time.Millisecond)
		return nil
	}
	var m map[string]any
	require.NoError(t, json.Unmarshal(data, &m))
	return m
}

// TestLoadFailBackoff 锁定 load 持续失败下的指数退避：注入持续失败的 load，
// 退避期（1s→2s→4s…）内 worker 的 load 请求频率必须显著低于 150ms 轮询节拍
// ——否则 sharkd 子进程崩溃/解析超时形成的「断连→重拨→全量重 load」循环会
// 空转烧 CPU。
func TestLoadFailBackoff(t *testing.T) {
	m := startMemSharkd(t)
	m.FailLoads.Store(1 << 30) // 注入持续失败的 load
	dir := t.TempDir()
	mgr := newManager(t, m.Addr(), dir)
	testutil.WriteShard(t, dir, "backoff.pcapng", time.Now(), make([]byte, 32))

	s, err := mgr.GetOrCreate("st-backoff")
	require.NoError(t, err)
	dialSession(t, s, false) // 解锁 worker 追帧（写循环已由接入启动）

	// 5s 窗口：退避 1s→2s→4s 下 load 请求 ≤8 次；无退避（150ms 节拍）会到 30+
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
	}
	n := 0
	for _, l := range m.Written() {
		if strings.Contains(l, `"method":"load"`) && strings.Contains(l, "backoff.pcapng") {
			n++
		}
	}
	require.LessOrEqual(t, n, 8, "退避期内 load 请求频率过高: %d", n)
}
