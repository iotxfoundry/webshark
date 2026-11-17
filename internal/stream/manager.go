// Package stream 实时推送流引擎：会话管理器 + 每流追帧 worker + 筛选模式 +
// WebSocket 线格式。服务端不生产 pcapng，只消费外部滚动写入的 spool 分片目录，
// 经外部 sharkd daemon 解析；冷启动锚定最新分片，增量推送。行为契约见
// docs/api.md 第 3、4、5 节。
package stream

import (
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/iotxfoundry/webshark/internal/spool"
	"github.com/iotxfoundry/webshark/sharkd"
)

const (
	// maxStreams 流会话数上限（单机少量使用者场景，防无界建流）。
	maxStreams = 16
	// streamGrace 断线宽限：WS 断开后会话保留追帧位置与专属实例，重连续推；
	// 超窗未归由 janitor 销毁（实例随流断开销毁）。
	streamGrace = time.Minute
	// janitorTick 无连接会话回收节拍。
	janitorTick = 5 * time.Second
)

// ErrStreamLimit 流数达上限（GetOrCreate 返回，服务端映射 429）。
var ErrStreamLimit = errors.New("实时流数已达上限(" + strconv.Itoa(maxStreams) + ")")

// ErrClosed 管理器已关闭（GetOrCreate 返回，服务端映射 503）。
var ErrClosed = errors.New("服务已关闭")

// Config 管理器配置。
type Config struct {
	Sock     string       // sharkd UDS
	SpoolDir string       // pcapng 分片目录
	RingSize int          // 环窗分片数, 0 → spool.DefaultRingSize
	Log      *slog.Logger // nil → slog.Default()
}

// Manager 会话表 + 分片检测单例订阅分发 + 断线宽限回收 + 进程级轻查询实例。
type Manager struct {
	cfg      Config
	ringSize int
	log      *slog.Logger

	lightMu sync.Mutex
	light   *sharkd.Client

	mu       sync.Mutex
	sessions map[string]*Session
	closed   bool

	watch       *spool.Watcher
	stopJanitor chan struct{}
	janitorDone chan struct{}
	closeOnce   sync.Once
}

// NewManager 创建管理器并启动分片检测与 janitor。
func NewManager(cfg Config) *Manager {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	m := &Manager{
		cfg:         cfg,
		ringSize:    cfg.RingSize,
		log:         cfg.Log,
		sessions:    map[string]*Session{},
		stopJanitor: make(chan struct{}),
		janitorDone: make(chan struct{}),
	}
	m.watch = spool.NewWatcher(cfg.SpoolDir, cfg.Log)
	go m.janitor()
	return m
}

// GetOrCreate 幂等建流；流数达上限返回 ErrStreamLimit。建流即启动专属 worker
// （sharkd 连接懒拨号）。
func (m *Manager) GetOrCreate(handle string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if s, ok := m.sessions[handle]; ok {
		return s, nil
	}
	if len(m.sessions) >= maxStreams {
		// 容量淘汰：断线会话只是停放游标（宽限内），淘汰最旧的一个腾位；重连
		// 按新流重建（前端按 reset 对齐）。全部已连接才是真上限。
		var oldestH string
		var oldest time.Time
		for h, s := range m.sessions {
			at, ok := s.staleAt()
			if !ok {
				continue
			}
			if oldestH == "" || at.Before(oldest) {
				oldestH, oldest = h, at
			}
		}
		if oldestH == "" {
			return nil, ErrStreamLimit
		}
		m.sessions[oldestH].shutdown()
		delete(m.sessions, oldestH)
	}
	s := newSession(handle, m)
	m.sessions[handle] = s
	go m.workerLoop(s)
	return s, nil
}

// Lookup 仅查找（不建流）；不存在或断线宽限已过的会话返回 nil。
func (m *Manager) Lookup(handle string) *Session {
	m.mu.Lock()
	s := m.sessions[handle]
	m.mu.Unlock()
	if s != nil && s.expired(time.Now()) {
		return nil
	}
	return s
}

// Light 进程级轻查询实例（check/complete/筛选校验共用；不 load 文件），
// 懒建，与每流实时实例物理隔离互不排队。
func (m *Manager) Light() *sharkd.Client {
	m.lightMu.Lock()
	defer m.lightMu.Unlock()
	if m.light == nil {
		m.light = sharkd.NewClient(m.cfg.Sock)
	}
	return m.light
}

// latestShard 最新分片名（冷启动锚点；无分片为空串）。
func (m *Manager) latestShard() string { return spool.Latest(m.cfg.SpoolDir) }

// ringWindowAsc 环窗分片 mtime 升序（补推/走查序）。
func (m *Manager) ringWindowAsc() []spool.Shard {
	return spool.RingAsc(m.cfg.SpoolDir, m.ringSize)
}

// janitor 周期回收：无连接且宽限已过的会话销毁（cancel → worker 退订/关实例）。
func (m *Manager) janitor() {
	defer close(m.janitorDone)
	t := time.NewTicker(janitorTick)
	defer t.Stop()
	for {
		select {
		case <-m.stopJanitor:
			return
		case now := <-t.C:
			m.mu.Lock()
			for h, s := range m.sessions {
				if s.expired(now) {
					s.shutdown()
					delete(m.sessions, h)
					m.log.Info("实时流宽限过期销毁", "handle", h)
				}
			}
			m.mu.Unlock()
		}
	}
}

// Close 关停管理器：停 janitor、销毁全部会话（cancel worker、关 sharkd 连接、
// 关订阅）、关轻查询实例。幂等。
func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		close(m.stopJanitor)
		<-m.janitorDone
		m.mu.Lock()
		m.closed = true // 摘表前置位：GetOrCreate 锁内判定，此后不再建流
		sessions := make([]*Session, 0, len(m.sessions))
		for h, s := range m.sessions {
			sessions = append(sessions, s)
			delete(m.sessions, h)
		}
		m.mu.Unlock()
		for _, s := range sessions {
			s.shutdown()
		}
		_ = m.watch.Close()
		m.lightMu.Lock()
		if m.light != nil {
			_ = m.light.Close()
			m.light = nil
		}
		m.lightMu.Unlock()
	})
}
