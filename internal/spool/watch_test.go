package spool

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// waitEvent 等待一条事件（stat 兜底周期 2s，留足余量）。
func waitEvent(t *testing.T, ch <-chan Event, timeout time.Duration) Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(timeout):
		t.Fatal("等待事件超时")
		return Event{}
	}
}

func requireNoEvent(t *testing.T, ch <-chan Event, d time.Duration) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("不应有事件, 收到 %v", ev)
	case <-time.After(d):
	}
}

func TestWatcherCreateEmitsOnce(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, slog.Default())
	defer func() { require.NoError(t, w.Close()) }()
	ch, unsub := w.Subscribe()
	defer func() { unsub() }()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.pcapng"), []byte("x"), 0o644))
	ev := waitEvent(t, ch, 5*time.Second)
	require.Equal(t, "a.pcapng", ev.Name)

	// 同一文件的后续 stat 轮不得重复发事件
	requireNoEvent(t, ch, 2500*time.Millisecond)
}

func TestWatcherMultiSubscriberSameSequence(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, slog.Default())
	defer func() { require.NoError(t, w.Close()) }()
	ch1, u1 := w.Subscribe()
	defer func() { u1() }()
	ch2, u2 := w.Subscribe()
	defer func() { u2() }()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "1.pcapng"), []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "2.pcapng"), []byte("x"), 0o644))

	var seq1, seq2 []string
	for range 2 {
		seq1 = append(seq1, waitEvent(t, ch1, 5*time.Second).Name)
		seq2 = append(seq2, waitEvent(t, ch2, 5*time.Second).Name)
	}
	require.Equal(t, seq1, seq2)
	require.ElementsMatch(t, []string{"1.pcapng", "2.pcapng"}, seq1)
}

func TestWatcherCloseIdempotent(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, slog.Default())
	ch, unsub := w.Subscribe()
	require.NoError(t, w.Close())
	require.NoError(t, w.Close()) // 幂等
	// Close 后退订通道关闭
	select {
	case _, ok := <-ch:
		require.False(t, ok)
	case <-time.After(2 * time.Second):
		t.Fatal("退订通道未关闭")
	}
	unsub() // Close 后退订仍安全
}

func TestWatcherUnsubscribe(t *testing.T) {
	dir := t.TempDir()
	w := NewWatcher(dir, slog.Default())
	defer func() { require.NoError(t, w.Close()) }()
	ch, unsub := w.Subscribe()
	unsub()
	unsub() // 幂等
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.pcapng"), []byte("x"), 0o644))
	requireNoEvent(t, ch, 2500*time.Millisecond)
}
