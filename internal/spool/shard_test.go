package spool

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, dir, name string, data []byte, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, data, 0o644))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
}

func TestListOrdering(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Truncate(time.Second)
	// 同 mtime：名字降序（c 在 b 前）
	writeFile(t, dir, "b.pcapng", []byte("bb"), base)
	writeFile(t, dir, "c.pcapng", []byte("c"), base)
	// 更新 mtime：排最前
	writeFile(t, dir, "a.pcapng", []byte("aaa"), base.Add(time.Minute))

	got := List(dir)
	require.Len(t, got, 3)
	require.Equal(t, []string{"a.pcapng", "c.pcapng", "b.pcapng"},
		[]string{got[0].Name, got[1].Name, got[2].Name})
	require.Equal(t, int64(3), got[0].Size)
	require.True(t, got[0].Mtime.Equal(base.Add(time.Minute)))
}

func TestListIgnoresNonPcapngAndDirs(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	writeFile(t, dir, "ok.pcapng", []byte("x"), base)
	writeFile(t, dir, "skip.txt", []byte("x"), base)
	writeFile(t, dir, "skip.pcapng.tmp", []byte("x"), base)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub.pcapng"), 0o755))

	got := List(dir)
	require.Len(t, got, 1)
	require.Equal(t, "ok.pcapng", got[0].Name)
}

func TestListMissingDir(t *testing.T) {
	got := List(filepath.Join(t.TempDir(), "nope"))
	require.NotNil(t, got)
	require.Empty(t, got)
}

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	require.Equal(t, "", Latest(dir))
	base := time.Now()
	writeFile(t, dir, "old.pcapng", []byte("x"), base)
	writeFile(t, dir, "new.pcapng", []byte("x"), base.Add(time.Second))
	require.Equal(t, "new.pcapng", Latest(dir))
}

func TestRing(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("%02d.pcapng", 19-i) // 名字大 = mtime 新
		writeFile(t, dir, name, []byte("x"), base.Add(time.Duration(i)*time.Second))
	}
	ring := Ring(dir, 5)
	require.Len(t, ring, 5)
	// 前缀必须是 List 的前 5 片（mtime 最新在前）
	full := List(dir)
	for i := range ring {
		require.Equal(t, full[i].Name, ring[i].Name)
	}
	require.Len(t, Ring(dir, 0), DefaultRingSize)
	require.Len(t, Ring(dir, -3), DefaultRingSize)
	require.Len(t, Ring(dir, 100), 20)
}
