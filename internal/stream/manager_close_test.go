// manager_close_test.go - Close 后 GetOrCreate 拒绝建流（返回 ErrClosed），
// 防止 srv.Close() 之后仍可 subscribe 出永久存活的 worker。
package stream

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetOrCreateAfterClose(t *testing.T) {
	m := NewManager(Config{Sock: filepath.Join(t.TempDir(), "s.sock"), SpoolDir: t.TempDir()})
	defer m.Close()
	s, err := m.GetOrCreate("h1")
	require.NoError(t, err)
	require.NotNil(t, s)
	m.Close()
	_, err = m.GetOrCreate("h2")
	require.ErrorIs(t, err, ErrClosed)
}
