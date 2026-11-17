// cache.go - 分片缓存实例：进程级 1 个 sharkd 连接，帧详情查询按需 load 分片，
// LRU 12 片驻留。驻留登记记录 load 时的文件字节数：命中后与当前尺寸比对，
// 尺寸变化（活动分片持续追加，sharkd load 是内存快照）即重 load 刷新快照——
// 否则新推送行的帧详情会全部越界。查询串行（客户端内互斥天然串行）。
// load 失败（分片已被环形清理等）以 sharkd.Error 返回，调用方映射 404。
package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/iotxfoundry/webshark/sharkd"
)

// shardCacheCap LRU 容量（驻留分片数）：环窗 16 片取 12，覆盖约 3/4 回看窗口
// ——历史详情查询集中在最近数片。
const shardCacheCap = 12

// shardCache 分片缓存实例（串行 + LRU 驻留登记）。
type shardCache struct {
	mu    sync.Mutex
	c     *sharkd.Client
	lru   []string         // 最近使用序（尾=最近）
	sizes map[string]int64 // shard → load 时的文件字节数（增长检测）
	dir   string           // spool 目录（load 绝对路径）
	sock  string
}

func newShardCache(sock, spoolDir string) *shardCache {
	return &shardCache{sock: sock, dir: spoolDir, sizes: map[string]int64{}}
}

// frame 按需 load 目标分片（未驻留或文件尺寸已变化时）后查单帧；返回 sharkd
// envelope 的 result。尺寸未变化的命中不重复 load。
func (c *shardCache) frame(ctx context.Context, shard string, params map[string]any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c == nil {
		c.c = sharkd.NewClient(c.sock)
	}
	path := filepath.Join(c.dir, shard)

	// 尺寸检测：与 load 时的字节数比对，变化（含新增/被覆盖重写）即重 load。
	// stat 失败（分片刚被环形清理删除）按「需要重 load」处理，load 会给出
	// envelope 错误，由调用方映射 404。
	var cur int64 = -1
	if fi, err := os.Stat(path); err == nil {
		cur = fi.Size()
	}
	stale, tracked := c.sizes[shard]
	if !tracked || stale != cur {
		if _, err := c.c.Call(ctx, "load", map[string]string{"file": path}); err != nil {
			c.forget(shard)
			return nil, err
		}
		c.sizes[shard] = cur
	}

	// LRU 驻留与驱逐（仅维护登记，不影响本帧查询）
	c.removeLocked(shard)
	c.lru = append(c.lru, shard)
	if len(c.lru) > shardCacheCap {
		old := c.lru[0]
		c.lru = c.lru[1:]
		delete(c.sizes, old)
	}

	return c.c.Call(ctx, "frame", params)
}

// removeLocked 摘除分片的 LRU 驻留登记（持 mu 调用）。
func (c *shardCache) removeLocked(shard string) {
	if i := slices.Index(c.lru, shard); i >= 0 {
		c.lru = append(c.lru[:i], c.lru[i+1:]...)
	}
}

// forget 移除分片的驻留登记（load 失败时调用，避免残留尺寸导致跳过重 load）。
func (c *shardCache) forget(shard string) {
	c.removeLocked(shard)
	delete(c.sizes, shard)
}

// Close 关停缓存实例连接。幂等。
func (c *shardCache) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.c != nil {
		_ = c.c.Close()
		c.c = nil
		c.lru = nil
		c.sizes = map[string]int64{}
	}
}
