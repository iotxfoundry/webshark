// cache.go - 分片缓存实例：进程级 1 个 sharkd 连接，历史帧详情查询按需 load
// 分片，LRU 12 片驻留（超出驱逐最久未用的登记，下次查询重 load），查询串行
// （客户端内互斥天然串行）。load 失败（分片已被环形清理等）以 sharkd.Error
// 返回，调用方映射 404。
package server

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"

	"github.com/iotxfoundry/webshark/internal/sharkd"
)

// shardCacheCap LRU 容量（驻留分片数）：环窗 16 片取 12，覆盖约 3/4 回看窗口
// ——历史详情查询集中在最近数片。
const shardCacheCap = 12

// shardCache 分片缓存实例（串行 + LRU 驻留登记）。
type shardCache struct {
	mu   sync.Mutex
	c    *sharkd.Client
	lru  []string // 最近使用序（尾=最近）
	dir  string   // spool 目录（load 绝对路径）
	sock string
}

func newShardCache(sock, spoolDir string) *shardCache {
	return &shardCache{sock: sock, dir: spoolDir}
}

// frame load 目标分片（未驻留时）后查单帧；返回 sharkd envelope 的 result。
func (c *shardCache) frame(shard string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c == nil {
		c.c = sharkd.NewClient(c.sock)
	}
	if i := slices.Index(c.lru, shard); i >= 0 { // 命中：提为最近使用
		c.lru = append(c.lru[:i], c.lru[i+1:]...)
		c.lru = append(c.lru, shard)
	} else {
		if _, err := c.c.Call("load", map[string]string{"file": filepath.Join(c.dir, shard)}); err != nil {
			return nil, err
		}
		c.lru = append(c.lru, shard)
		if len(c.lru) > shardCacheCap { // 驱逐最久未用的登记（实例内仍驻留至下次 load）
			c.lru = c.lru[1:]
		}
	}
	return c.c.Call("frame", params)
}

// Close 关停缓存实例连接。幂等。
func (c *shardCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c != nil {
		_ = c.c.Close()
		c.c = nil
		c.lru = nil
	}
}
