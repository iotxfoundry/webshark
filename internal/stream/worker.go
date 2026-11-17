// worker.go - 每流后台 worker：订阅分片检测事件（扇出）→ 实时实例自动 load
// 最新分片（已 load 不重 load，单 goroutine 串行=解析在途不叠加）→ 按 filter
// 拉增量行（游标随流保存）→ 微批推送（批满 32 条即发，80ms 兜底冲刷；出站
// 队列满即背压阻塞，推送速率由消费端决定——有界环窗源下丢帧重推是活锁）。
// 冷启动自最新分片起（不回放环窗历史：历史阅读走筛选/导出）；筛选是同一管道
// 的参数（行帧 mode:"filter"，位置归零自环窗起点逐片走查）；追帧暂停/无连接
// 会话冻结：不追不推，位置随流保留；恢复/重连：从位置逐片补推；位置分片已被
// 环形清理/出窗 → reset + 当前状态从零全量重推（背压下走查快于窗滑，收敛）。
package stream

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/iotxfoundry/webshark/internal/spool"
)

const (
	// pullInterval 同片增量拉取最小间隔（in-shard 增长无文件事件，纯节拍兜底；
	// sharkd 单次拉取 ~5-10ms，150ms 节拍 CPU 可忽略）。
	pullInterval = 150 * time.Millisecond
	// flushInterval 微批兜底冲刷周期（批满即发不等；兼循环节拍）。
	flushInterval = 80 * time.Millisecond
	// batchRows 微批行数上限（契约：~100-200ms 或 32 条触发）。
	batchRows = 32
	// pullLimit 单次 frames 拉取行数上限：sharkd 的 skip 每次调用从第 1 行重扫
	// （filter 模式逐帧重评估 dfilter），拉取成本 O(游标) 与 limit 无关——大拉取
	// 把重扫次数降一个量级；2000 行×KB 级行远小于客户端消息上限。
	pullLimit = 2000
	// loadTimeout 单次 load 超时：全量重解析大分片在低配设备上可能超过
	// 客户端默认 10s，超时会断连重拨（sharkd 日志反复 Hello in child）
	loadTimeout = 60 * time.Second
	// retryBackoffMax 拉取/加载失败后的重试退避上限（指数 1s→2s→…→10s）；
	// sharkd 子进程崩溃或解析超时会形成「断连→重拨→全量重 load」循环，
	// 退避避免该循环空转烧 CPU
	retryBackoffMax = 10 * time.Second
	// reloadInterval 活动分片重 load 最小间隔：sharkd load=内存快照，load 后
	// 分片追加字节对会话不可见（不重 load 看不到追加行）——按 size 增长检测重
	// load，节流防高频 stat+load；增长可见延迟 ≤ reloadInterval+节拍+冲刷。
	reloadInterval = 1 * time.Second
)

// streamPos 追帧位置（随流保存：断线/暂停期间冻结，重连/恢复从此处续推）。
type streamPos struct {
	shard  string // 位置锚定的分片
	cursor int    // 该分片结果集内已推送行数（frames skip 游标）
}

// batch 微批累积器（单分片内成批，跨分片边界即冲刷，行不混 shard）；mode 为
// 下行 rows 帧的 mode（"live" 普通流 / "filter" 筛选流，线格式其余不变）。
type batch struct {
	s     *Session
	mode  string
	shard string
	rows  []json.RawMessage
}

func (b *batch) setShard(name string) { b.shard = name }

func (b *batch) add(row json.RawMessage) { b.rows = append(b.rows, row) }

// full 报告是否达微批行数上限（契约触发条件之一）。
func (b *batch) full() bool { return len(b.rows) >= batchRows }

// flush 冲刷未满批（拉取循环收尾/周期兜底/分片边界调用）。队列满时 Enqueue
// 背压阻塞——推送速率由消费端决定。
func (b *batch) flush() {
	if len(b.rows) == 0 {
		return
	}
	mode := b.mode
	if mode == "" {
		mode = "live"
	}
	b.s.Enqueue(RowsFrame(mode, b.shard, b.rows))
	b.rows = nil
}

// discard 丢弃未发批（筛选切换/清视图：未发行将被新结果或重推覆盖）。
func (b *batch) discard() { b.rows = nil }

// workerLoop 每流 worker 主循环（会话生命周期 = worker 生命周期）。
func (m *Manager) workerLoop(s *Session) {
	sub, stopSub := m.watch.Subscribe()
	defer stopSub()
	defer s.closeShark()

	var (
		// 冷启动锚定最新分片（空 spool 等检测）：不回放环窗历史，见文件头注。
		pos        = streamPos{shard: m.latestShard()}
		loaded     string    // 实例当前 load 的分片（重拨/失败后复位强制重 load）
		loadedSize int64     // load 时该分片的字节数（load=内存快照，增长检测依据）
		lastLoad   time.Time // 最近一次成功 load 时刻（reload 节流）
		lastPush   string    // 最近推送分片（rotated 只在切换时发）
		needScan   = true    // 首轮走查：自锚点对齐到最新
		lastPull   time.Time
		b          = batch{s: s, mode: "live"}
	)
	flush := time.NewTicker(flushInterval)
	defer flush.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case _, ok := <-sub: // 检测事件扇出：下轮对齐（通道关闭=管理器收尾）
			if !ok {
				return
			}
			needScan = true
		case <-flush.C: // 兜底冲刷兼循环节拍（暂停/连接检查每 80ms 复核）
			b.flush()
		}
		if s.Paused() || !s.attached() {
			continue // 追帧暂停/无前端：会话冻结，不追不推，位置随流保留
		}
		if s.pullFailBackoff() {
			continue // 拉取/加载失败退避中：不追帧不重 load，微批照常冲刷
		}
		if pf, ok := s.takePendingFilter(); ok { // 筛选进/出/切换
			m.applyFilter(s, pf, &pos, &b)
			continue
		}
		filter := s.Filter() // 筛选=同一管道的参数（空串=普通模式）
		if needScan {
			needScan = false
			m.backfill(s, &pos, &loaded, &loadedSize, &lastLoad, &lastPush, &b, filter)
			continue
		}
		if time.Since(lastPull) < pullInterval {
			continue
		}
		lastPull = time.Now()
		// 兜底核对最新分片（文件事件丢失只损延迟不损正确性）
		if latest := m.latestShard(); latest != "" && latest != pos.shard {
			needScan = true
			continue
		}
		if pos.shard == "" { // 尚无分片：等检测
			needScan = true
			continue
		}
		if err := m.pullShard(s, pos.shard, &pos, &b, &loadedSize, &lastLoad, filter); err != nil {
			ra := s.markPullFail()
			m.log.Warn("实时增量拉取失败", "handle", s.handle, "err", err, "退避", ra.Sub(time.Now()))
			loaded = ""    // 连接状态可疑：下轮强制重 load
			loadedSize = 0 // 复位（伴随 loaded）
			needScan = true
		} else {
			s.clearPullFail()
		}
	}
}

// loadShard load 分片并记录快照字节数（sharkd load=内存快照：load 后分片追加
// 字节对会话不可见，size 是活动片增长重 load 的检测依据）。
func (m *Manager) loadShard(s *Session, shard string, loadedSize *int64) error {
	path := filepath.Join(m.cfg.SpoolDir, shard)
	// load 是全量重解析：数万包的大分片在低配设备上可能超过客户端默认 10s，
	// 超时会导致连接被丢弃后重拨、再全量解析的会话抖动（sharkd 日志反复
	// Hello in child），故放宽到 60s
	if _, err := s.CallTimeout("load", map[string]string{"file": path}, loadTimeout); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		*loadedSize = fi.Size()
	} else {
		*loadedSize = 0
	}
	return nil
}

// backfill 从追帧位置向环窗最新分片逐片补齐（历史连续不重不漏；环窗外遗留片
// 不进走查——位置片已出窗 = 超缓冲 → reset + 当前状态从零重推）。冷启动时 pos
// 已锚最新片，走查退化为单片 load+拉取；恢复/补缺口时自位置片逐片推进；筛选
// 模式（非空 filter）同管道透传，位置归零即全窗走查。节流语义：已 load 分片
// 不重 load（检测事件风暴零重解析），串行循环=解析不叠加；推送由出站队列背压
// 节流（消费速度决定），走查快于窗滑即收敛；ctx/paused 逐片检查。
func (m *Manager) backfill(s *Session, pos *streamPos, loaded *string, loadedSize *int64, lastLoad *time.Time, lastPush *string, b *batch, filter string) {
	shards := m.ringWindowAsc()
	start := 0
	if pos.shard != "" {
		idx := slices.IndexFunc(shards, func(f spool.Shard) bool { return f.Name == pos.shard })
		if idx < 0 {
			// 位置分片已被滚动清理/出环窗：超缓冲 → reset 全量对齐
			b.discard()
			s.realign()
			out := pos.shard // 清空前取值（日志实值）
			*pos = streamPos{}
			*loaded = "" // 会话 load 的旧片亦出窗：复位强制重 load 窗内片
			*loadedSize = 0
			*lastPush = "" // 全量重推：rotated 重新发
			m.log.Info("追帧位置分片已清理/出窗，reset 全量重推", "handle", s.handle, "shard", out)
		} else {
			start = idx
		}
	}
	for i := start; i < len(shards); i++ {
		if s.ctx.Err() != nil || s.Paused() || s.hasPendingFilter() { // 关闭/暂停/待生效筛选（pos 已随片推进）
			return
		}
		name := shards[i].Name
		if *loaded != name {
			if err := m.loadShard(s, name, loadedSize); err != nil {
				*loaded = ""
				*loadedSize = 0 // 复位（伴随 loaded）
				ra := s.markPullFail()
				m.log.Warn("load 分片失败", "handle", s.handle, "shard", name, "err", err, "退避", ra.Sub(time.Now()))
				return // 下轮事件/周期重试
			}
			*loaded = name
			s.setCurShard(name)
		}
		if pos.shard != name { // 换片：游标归零（游标是片内结果集偏移，不跨片延续）
			pos.shard = name
			pos.cursor = 0
			b.flush() // 片界冲刷：上片未满批先于本片 rotated 下发（行不混 shard）
		}
		if *lastPush != name {
			s.Enqueue(RotatedFrame(name))
			*lastPush = name
		}
		if err := m.pullShard(s, name, pos, b, loadedSize, lastLoad, filter); err != nil {
			*loaded = ""
			*loadedSize = 0 // 复位（伴随 loaded）
			m.log.Warn("补推拉取失败", "handle", s.handle, "shard", name, "err", err)
			return
		}
	}
	b.flush()
	// 走查收尾以**当前**环窗核对位置（走查用的事件前快照可能滞后——背压走查
	// 期间窗滑过位置即 reset 对齐，收敛语义）
	if pos.shard != "" {
		if idx := slices.IndexFunc(m.ringWindowAsc(), func(f spool.Shard) bool { return f.Name == pos.shard }); idx < 0 {
			b.discard()
			s.realign()
			out := pos.shard
			*pos = streamPos{}
			*loaded = ""
			*loadedSize = 0
			*lastPush = ""
			m.log.Info("走查后位置出窗，reset 全量重推", "handle", s.handle, "shard", out)
		}
	}
}

// pullShard 对指定分片按游标增量拉取（frames skip=cursor）并微批推送；返回时
// 位置推进到该分片已拉行数（返回 < limit 即追平）。非空 filter=筛选模式（请求
// 携 filter 逐帧重评估，行帧 mode:"filter"），空串=普通模式（不携 filter——契约）。
// 活动分片（=环窗最新）增长时按 size 重 load（sharkd load=内存快照，追加字节
// 对已 load 会话不可见；节流 reloadInterval；历史片不可变，不 stat 不重 load）。
func (m *Manager) pullShard(s *Session, shard string, pos *streamPos, b *batch, loadedSize *int64, lastLoad *time.Time, filter string) error {
	if shard == m.latestShard() { // 活动片：增长检测（size 增长 + 节流）
		if fi, err := os.Stat(filepath.Join(m.cfg.SpoolDir, shard)); err == nil &&
			fi.Size() > *loadedSize && time.Since(*lastLoad) >= reloadInterval {
			if lerr := m.loadShard(s, shard, loadedSize); lerr != nil {
				return lerr
			}
			*lastLoad = time.Now()
		}
	}
	b.flush() // 分片边界：先冲上一片未满批（行不混 shard）
	b.setShard(shard)
	if filter != "" {
		b.mode = "filter"
	} else {
		b.mode = "live"
	}
	for {
		if s.ctx.Err() != nil || s.Paused() || s.hasPendingFilter() { // 关闭/暂停/待生效筛选（游标已推进续推）
			return nil
		}
		params := struct {
			Limit  int    `json:"limit"`
			Skip   int    `json:"skip,omitempty"`   // 0 序省略（sharkd UINTEGER: skip 须 >0，省略=从第 1 行）
			Filter string `json:"filter,omitempty"` // 普通模式不携（契约）
		}{pullLimit, pos.cursor, filter}
		res, err := s.Call("frames", params)
		if err != nil {
			return err
		}
		var rows []json.RawMessage
		if json.Unmarshal(res, &rows) != nil { // res 即裸行数组（sharkd frames result 无包装键）
			return errFramesShape
		}
		if len(rows) == 0 {
			pos.shard = shard // 追平：位置锚定该片（0 行亦然）
			return nil
		}
		for i, row := range rows {
			b.add(withRefRow(row, shard))
			if !b.full() {
				continue
			}
			if s.ctx.Err() != nil || s.Paused() || s.hasPendingFilter() { // 行粒度中断：游标锚定已收行（chunk 可达 2000 行，轮界检查不即时）
				pos.cursor += i + 1
				pos.shard = shard
				return nil
			}
			b.flush()
		}
		pos.cursor += len(rows)
		pos.shard = shard
		if len(rows) < pullLimit {
			return nil
		}
	}
}

// errFramesShape frames 响应形态异常（非裸行数组）。
var errFramesShape = errors.New("frames 响应形态异常（非裸行数组）")
