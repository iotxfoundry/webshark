// session.go - 一条实时推送流：会话持有专属 sharkd 实例（每流一条不共享，
// 流销毁即销毁）与出站推送队列。同 handle 二次接入顶替旧连接；断线进入宽限
// 供重连续推；队列满即背压阻塞生产端（有界环窗源下丢帧+reset 重推是活锁，
// 正确机制是生产端等消费端）。
package stream

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/iotxfoundry/webshark/sharkd"
)

// maxOutQueue 出站队列帧数上限：积压到此即触发背压阻塞。
const maxOutQueue = 256

// resetFrame 全量对齐信号帧（出窗对齐/筛选清视图共用）。
var resetFrame = ResetFrame("realign")

// errSessionClosed 会话已关闭后的 Call 拒绝。
var errSessionClosed = errors.New("实时流已关闭")

// Session 一条实时流（单生产者 worker + 单消费者 WS writer）。
type Session struct {
	handle string
	m      *Manager
	ctx    context.Context
	cancel context.CancelFunc

	wsMu    sync.Mutex
	ws      *websocket.Conn
	expires time.Time // 断线宽限到期时刻（janitor 据此销毁无连接会话）

	stMu     sync.Mutex
	paused   bool
	filter   string // 生效筛选串（空串=普通模式；worker 侧写）
	pendSet  bool   // 待生效筛选请求（WS/subscribe 提交，worker tick 取走执行）
	pendVal  string
	curShard string    // 实时实例当前 load 的分片（frame 路由依据）
	pullErrs int       // 连续拉取/加载失败次数（成功清零，驱动指数退避）
	retryAft time.Time // 退避截止时刻（此前不追帧不重 load）

	outMu     sync.Mutex
	outCond   *sync.Cond
	outQ      [][]byte // 满即背压阻塞生产端
	outClosed bool

	sharkMu sync.Mutex
	shark   *sharkd.Client
}

func newSession(handle string, m *Manager) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{handle: handle, m: m, ctx: ctx, cancel: cancel}
	s.outCond = sync.NewCond(&s.outMu)
	s.touchLocked()
	return s
}

// Handle 流句柄。
func (s *Session) Handle() string { return s.handle }

// ---- WS 连接持有 ----

// Attach 绑定 WS 连接并续期；返回被顶替的旧连接（同 handle 二次接入时接管流）。
func (s *Session) Attach(ws *websocket.Conn) (old *websocket.Conn) {
	s.wsMu.Lock()
	old = s.ws
	s.ws = ws
	s.touchLocked()
	s.wsMu.Unlock()
	// 唤醒旧 writer 复查当前持有者：被接管即退出，不再消费队列
	s.outMu.Lock()
	s.outCond.Broadcast()
	s.outMu.Unlock()
	return old
}

// Detach 解绑连接（仅当前持有者生效）并进入断线宽限。
func (s *Session) Detach(ws *websocket.Conn) {
	s.wsMu.Lock()
	if s.ws == ws {
		s.ws = nil
	}
	s.touchLocked()
	s.wsMu.Unlock()
	// 唤醒写循环复查持有者：解绑后挂起在 PopFor 的 writer 即时退出
	s.outMu.Lock()
	s.outCond.Broadcast()
	s.outMu.Unlock()
}

func (s *Session) attached() bool {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return s.ws != nil
}

// Touch 续期宽限窗口（HTTP 面 frame/subscribe 并发调用与 WS attach/detach 竞态）。
func (s *Session) Touch() {
	s.wsMu.Lock()
	s.touchLocked()
	s.wsMu.Unlock()
}

func (s *Session) touchLocked() { s.expires = time.Now().Add(streamGrace) }

// staleAt 断线停放会话的到期时刻（janitor 据此销毁；容量淘汰同判据）。ok=false
// 表示有活跃连接不可淘汰。
func (s *Session) staleAt() (time.Time, bool) {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	if s.ws != nil {
		return time.Time{}, false
	}
	return s.expires, true
}

// expired 报告会话是否可销毁（无连接且宽限已过）。
func (s *Session) expired(now time.Time) bool {
	s.wsMu.Lock()
	defer s.wsMu.Unlock()
	return s.ws == nil && now.After(s.expires)
}

// ---- 流状态 ----

// SetPaused 追帧暂停/恢复。
func (s *Session) SetPaused(v bool) {
	s.stMu.Lock()
	s.paused = v
	s.stMu.Unlock()
}

// Paused 当前是否暂停。
func (s *Session) Paused() bool {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	return s.paused
}

// setCurShard 由 worker 在 load 成功后更新（frame 路由依据）。
func (s *Session) setCurShard(name string) {
	s.stMu.Lock()
	s.curShard = name
	s.stMu.Unlock()
}

// Shard 实时实例当前 load 的分片（未就绪为空串）。
func (s *Session) Shard() string {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	return s.curShard
}

// Call 走该流专属实例（懒拨号；与 worker 追帧共用，客户端内串行）。
// 超时取 sharkd 客户端默认值（10s）；sharkd.Error 原样返回。
func (s *Session) Call(method string, params any) (json.RawMessage, error) {
	return s.CallCtx(s.ctx, method, params)
}

// CallTimeout 带独立超时的调用（load 类大文件全量解析可放宽，防慢设备上
// 解析超时导致连接被丢弃后重拨、再全量解析的会话抖动）。
func (s *Session) CallTimeout(method string, params any, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(s.ctx, timeout)
	defer cancel()
	return s.CallCtx(ctx, method, params)
}

// CallCtx 以指定 ctx 调用（懒拨号；与 worker 追帧共用，客户端内串行）。
// sharkd.Error 原样返回；会话关闭即取消在途请求（连接随之丢弃）。
func (s *Session) CallCtx(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.sharkMu.Lock()
	defer s.sharkMu.Unlock()
	if s.ctx.Err() != nil {
		return nil, errSessionClosed
	}
	if s.shark == nil {
		s.shark = sharkd.NewClient(s.m.cfg.Sock)
	}
	return s.shark.Call(ctx, method, params)
}

// closeShark 关停专属实例（worker 退出即关连接）。
func (s *Session) closeShark() {
	s.sharkMu.Lock()
	defer s.sharkMu.Unlock()
	if s.shark != nil {
		_ = s.shark.Close()
		s.shark = nil
	}
}

// RequestFilter 提交待生效筛选请求（worker tick 取走执行）。
func (s *Session) RequestFilter(f string) {
	s.stMu.Lock()
	s.pendSet, s.pendVal = true, f
	s.stMu.Unlock()
}

// takePendingFilter 取走待生效筛选请求（无则 false）。
func (s *Session) takePendingFilter() (string, bool) {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	if !s.pendSet {
		return "", false
	}
	s.pendSet = false
	return s.pendVal, true
}

// hasPendingFilter 报告有待生效筛选请求——长推送循环的中断点检查：退出/切换
// 须及时生效，不得等整段走查（背压下可达分钟级）走完。
func (s *Session) hasPendingFilter() bool {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	return s.pendSet
}

// Filter 当前生效筛选串（空串=普通模式）。
func (s *Session) Filter() string {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	return s.filter
}

// setFilter 生效筛选串（worker 侧写）。
func (s *Session) setFilter(f string) {
	s.stMu.Lock()
	s.filter = f
	s.stMu.Unlock()
}

// markPullFail 记一次拉取/加载失败并返回退避截止时刻（指数 1s→2s→…→10s）：
// 防 sharkd 子进程崩溃或大分片解析超时形成「断连→重拨→全量重 load」循环空转。
func (s *Session) markPullFail() time.Time {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	s.pullErrs++
	d := min(time.Duration(1<<uint(s.pullErrs-1))*time.Second, retryBackoffMax)
	s.retryAft = time.Now().Add(d)
	return s.retryAft
}

// clearPullFail 拉取/加载成功：退避清零（恢复正常节拍）。
func (s *Session) clearPullFail() {
	s.stMu.Lock()
	s.pullErrs = 0
	s.retryAft = time.Time{}
	s.stMu.Unlock()
}

// pullFailBackoff 报告退避期内（不追帧不重 load，微批照常冲刷）。
func (s *Session) pullFailBackoff() bool {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	return time.Now().Before(s.retryAft)
}

// ---- 出站队列（单生产者/单消费者） ----

// Enqueue 追加下行帧。队列满即阻塞等待消费腾位（背压，不丢帧）。会话关闭即
// 返回（帧随会话作废），由 shutdown 的 Broadcast 唤醒阻塞端。
func (s *Session) Enqueue(msg []byte) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	for len(s.outQ) >= maxOutQueue {
		if s.outClosed {
			return
		}
		s.outCond.Wait()
	}
	if s.outClosed {
		return
	}
	s.outQ = append(s.outQ, msg)
	s.outCond.Signal()
}

// realign 清空待发队列仅保留 reset 帧（筛选切换/出窗对齐语义）；背压下生产端
// 在 Wait 中让出锁，重检见队列腾空即续入队（顺序：reset 先于后续行）。
func (s *Session) realign() {
	s.outMu.Lock()
	s.outQ = append(s.outQ[:0], resetFrame)
	s.outCond.Broadcast()
	s.outMu.Unlock()
}

// PopFor 取队首下行帧（队列空则挂起；会话关闭或该连接已被顶替返回 false）。
func (s *Session) PopFor(ws *websocket.Conn) (msg []byte, ok bool) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	for {
		s.wsMu.Lock()
		current := s.ws == ws
		s.wsMu.Unlock()
		if s.outClosed || !current {
			return nil, false // 会话关闭 / 被新连接接管：writer 退出
		}
		if len(s.outQ) > 0 {
			msg := s.outQ[0]
			s.outQ = s.outQ[1:]
			s.outCond.Broadcast() // 唤醒背压阻塞的生产端腾位复查
			return msg, true
		}
		s.outCond.Wait()
	}
}

// shutdown 关停会话（worker 与 writer 随之退出）。
func (s *Session) shutdown() {
	s.outMu.Lock()
	s.outClosed = true
	s.outCond.Broadcast()
	s.outMu.Unlock()
	s.cancel()
}
