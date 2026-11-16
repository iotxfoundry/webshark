package webshark_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/iotxfoundry/webshark"
)

// TestNew 校验公开 API 的必填项检查与幂等 Close（根包是库使用者最先看到的门面）。
func TestNew(t *testing.T) {
	t.Run("缺必填路径", func(t *testing.T) {
		_, err := webshark.New(webshark.Config{SharkdSock: "/tmp/x.sock"})
		require.ErrorContains(t, err, "SpoolDir")

		_, err = webshark.New(webshark.Config{SpoolDir: t.TempDir()})
		require.ErrorContains(t, err, "SharkdSock")
	})

	t.Run("装配与幂等关闭", func(t *testing.T) {
		srv, err := webshark.New(webshark.Config{
			SpoolDir:   t.TempDir(),
			SharkdSock: "/tmp/absent.sock", // 懒拨号：构造不做 IO
		})
		require.NoError(t, err)
		require.NotNil(t, srv.Handler())
		require.NoError(t, srv.Close())
		require.NoError(t, srv.Close())
	})
}
