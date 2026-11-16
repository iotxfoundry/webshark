// cap_evict_test.go - 流上限淘汰语义：满额时断线（无连接）停放会话可被淘汰
// 腾位——前端每次刷新生成新 handle，60s 宽限内 16 次刷新即满员，不淘汰则全员
// 429 卡死；全部已连接才是真上限。
package stream

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStreamCapEvictsDetached 锁定满额淘汰：maxStreams 个未连接（断线停放）
// 会话占满后，新 handle 建流须淘汰最旧断线会话并成功（断线会话只停放游标）。
func TestStreamCapEvictsDetached(t *testing.T) {
	mgr := newManager(t, filepath.Join(t.TempDir(), "absent.sock"), t.TempDir())
	for i := range maxStreams {
		_, err := mgr.GetOrCreate(fmt.Sprintf("evict-%02d", i))
		require.NoError(t, err)
	}
	sess, err := mgr.GetOrCreate("over")
	require.NoError(t, err, "断线停放会话应被淘汰腾位, 而非返回 ErrStreamLimit")
	require.Same(t, sess, mgr.Lookup("over"))
}
