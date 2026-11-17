package spool

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/iotxfoundry/webshark/internal/testutil"
)

func TestListOrdering(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Truncate(time.Second)
	// 同 mtime：名字降序（c 在 b 前）
	testutil.WriteShard(t, dir, "b.pcapng", base, []byte("bb"))
	testutil.WriteShard(t, dir, "c.pcapng", base, []byte("c"))
	// 更新 mtime：排最前
	testutil.WriteShard(t, dir, "a.pcapng", base.Add(time.Minute), []byte("aaa"))

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
	testutil.WriteShard(t, dir, "ok.pcapng", base, []byte("x"))
	testutil.WriteShard(t, dir, "skip.txt", base, []byte("x"))
	testutil.WriteShard(t, dir, "skip.pcapng.tmp", base, []byte("x"))
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
	testutil.WriteShard(t, dir, "old.pcapng", base, []byte("x"))
	testutil.WriteShard(t, dir, "new.pcapng", base.Add(time.Second), []byte("x"))
	require.Equal(t, "new.pcapng", Latest(dir))
}

func TestRing(t *testing.T) {
	dir := t.TempDir()
	base := time.Now()
	for i := 0; i < 20; i++ {
		name := fmt.Sprintf("%02d.pcapng", 19-i) // 名字大 = mtime 新
		testutil.WriteShard(t, dir, name, base.Add(time.Duration(i)*time.Second), []byte("x"))
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
