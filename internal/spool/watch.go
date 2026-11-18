package spool

import (
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Event 是一次分片出现事件（Name 为 spool 内文件名；轮换即新分片出现）。
type Event struct {
	// Name spool 内文件名。
	Name string
}

// statInterval stat 兜底扫描周期（fsnotify 丢事件时 2s 内补检）。
const statInterval = 2 * time.Second

// Watcher 是分片检测单例：fsnotify 事件驱动（即时）+ stat 兜底双层检测，known 集去重保证每文件恰好一个事件，扇出给全部订阅者。只关心 Create；Write/Remove 等其余事件由消费方自行消化。
type Watcher struct {
	// dir 监听的 spool 目录。
	dir   string
	log   *slog.Logger
	watch *fsnotify.Watcher

	mu    sync.Mutex
	known map[string]bool
	subs  map[chan Event]struct{}

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewWatcher 创建并启动检测循环；log 为 nil 时用 slog.Default()。目录暂缺时由 stat 兜底周期重试挂载。
func NewWatcher(dir string, log *slog.Logger) *Watcher {
	// log 为 nil 时回落默认 logger。
	if log == nil {
		log = slog.Default()
	}
	w := &Watcher{
		dir:   dir,
		log:   log,
		known: map[string]bool{},
		subs:  map[chan Event]struct{}{},
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	fw, err := fsnotify.NewWatcher()
	if err != nil { // fsnotify 不可用：纯 stat 兜底层运转
		log.Warn("创建 fsnotify 失败，仅 stat 兜底", "err", err)
	} else {
		w.watch = fw
		if aerr := fw.Add(dir); aerr != nil {
			log.Warn("fsnotify 挂载 spool 目录失败，stat 兜底", "dir", dir, "err", aerr)
		}
	}
	go w.run()
	return w
}

// Subscribe 登记一条订阅（缓冲 64；慢订阅溢出丢弃，丢事件只损延迟不损正确性）。
// 返回事件 chan 与幂等的退订函数。
func (w *Watcher) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 64)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	return ch, func() {
		w.mu.Lock()
		delete(w.subs, ch)
		w.mu.Unlock()
	}
}

// Close 停止检测循环并释放 inotify 句柄；幂等，阻塞至后台 goroutine 退出，
// 并关闭全部订阅通道（run 退出后不再广播，关闭无并发发送风险）。
func (w *Watcher) Close() error {
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
	w.mu.Lock()
	for ch := range w.subs {
		close(ch)
	}
	w.subs = map[chan Event]struct{}{}
	w.mu.Unlock()
	return nil
}

// run 检测主循环（单 goroutine，按到达序广播 → 各订阅者同序）。
func (w *Watcher) run() {
	defer close(w.done)
	if w.watch != nil {
		defer func() { _ = w.watch.Close() }()
	}
	t := time.NewTicker(statInterval)
	defer t.Stop()
	added := w.watch != nil // 初次 Add 已在 NewWatcher 尝试
	for {
		select {
		case <-w.stop:
			return
		case ev := <-w.events():
			if ev.Op&fsnotify.Create != 0 && strings.HasSuffix(ev.Name, ".pcapng") {
				w.mark(filepath.Base(ev.Name))
			}
		case <-t.C:
			if !added && w.watch != nil { // 目录曾缺：兜底周期重试挂载
				if err := w.watch.Add(w.dir); err == nil {
					added = true
				}
			}
			for _, name := range w.scanNew() { // stat 兜底：known 去重
				w.mark(name)
			}
		}
	}
}

// events fsnotify 事件 chan（watch 创建失败时返回永阻塞的 nil chan）。
func (w *Watcher) events() <-chan fsnotify.Event {
	if w.watch == nil {
		return nil
	}
	return w.watch.Events
}

// scanNew 列出全部 .pcapng 分片名（升序：同批多片按名稳定序即轮换序）。
func (w *Watcher) scanNew() []string {
	des, err := os.ReadDir(w.dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, de := range des {
		if !de.IsDir() && filepath.Ext(de.Name()) == ".pcapng" {
			names = append(names, de.Name())
		}
	}
	sort.Strings(names)
	return names
}

// mark 登记新分片并广播（fsnotify 与 stat 兜底可能撞同一文件）。
func (w *Watcher) mark(name string) {
	w.mu.Lock()
	if w.known[name] {
		w.mu.Unlock()
		return
	}
	w.known[name] = true
	subs := make([]chan Event, 0, len(w.subs))
	for ch := range w.subs {
		subs = append(subs, ch)
	}
	w.mu.Unlock()
	ev := Event{Name: name}
	for _, ch := range subs {
		select {
		case ch <- ev:
		default: // 慢订阅丢弃
		}
	}
}
