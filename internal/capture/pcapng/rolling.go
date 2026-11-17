// rolling.go 实现 dumpcap 式大小滚动: 当前分片写入超过 maxFileSize 后换新分片,
// 只保留最新 maxFiles 个(更旧的删除). 每个分片都是自含完整元数据的合法 pcapng
// (section header + 全部接口块), 可独立用任意 pcapng 工具打开.
package pcapng

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// shardName 分片文件名: cap-20060102-150405-<seq>.pcapng, seq 为毫秒级单调
// 后缀防同秒碰撞, 兼顾文件名字典序即时间序.
func shardName(t time.Time, seq uint32) string {
	return fmt.Sprintf("cap-%s-%06d.pcapng", t.UTC().Format("20060102-150405"), seq)
}

// RollingWriter 滚动写入器: 接口元数据记在内存, 每滚动新分片时重放,
// 保证分片自含元数据. 非并发安全.
type RollingWriter struct {
	dir      string
	maxSize  int64
	maxFiles int
	seq      uint64  // 分片名后缀, 单调递增防同秒碰撞
	shard    *Writer // 当前分片, 首次 Write 时打开
	ifaces   []ifaceRec
}

// NewRolling 创建滚动写入器; spool 目录不存在时立即创建(含父目录).
func NewRolling(dir string, maxFileSize int64, maxFiles int) (*RollingWriter, error) {
	if maxFileSize <= 0 {
		return nil, fmt.Errorf("pcapng: maxFileSize 必须为正, 得到 %d", maxFileSize)
	}
	if maxFiles <= 0 {
		return nil, fmt.Errorf("pcapng: maxFiles 必须至少为 1, 得到 %d", maxFiles)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建抓包目录: %w", err)
	}
	return &RollingWriter{dir: dir, maxSize: maxFileSize, maxFiles: maxFiles}, nil
}

// AddInterface 声明接口, 返回接口 id; 元数据留存, 滚动新分片时重放.
// 首帧已写入后仍可调用, 并立即滚动新分片使新接口当帧生效 —— pcapng 分片封板后
// 不能追加 IDB, 不滚动则新接口的帧在当前分片写不进(静默丢帧直到下次滚动).
func (w *RollingWriter) AddInterface(linkType capture.LinkType, name string) (uint16, error) {
	w.ifaces = append(w.ifaces, ifaceRec{linkType: linkType, name: name})
	if w.shard != nil {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	return uint16(len(w.ifaces) - 1), nil
}

// rotate 关当前分片, 开新分片(重放全部接口块), 并按 maxFiles 清理旧分片.
func (w *RollingWriter) rotate() error {
	if w.shard != nil {
		if err := w.shard.Close(); err != nil {
			return fmt.Errorf("关闭旧分片: %w", err)
		}
		w.shard = nil
	}
	name := shardName(time.Now(), uint32(w.seq%1_000_000))
	w.seq++
	shard := newWriterPath(w.dir, name)
	for i, rec := range w.ifaces {
		if _, err := shard.AddInterface(rec.linkType, rec.name); err != nil {
			return fmt.Errorf("分片 %s 重放接口 %d: %w", name, i, err)
		}
	}
	w.shard = shard
	w.prune()
	return nil
}

// prune 按 maxFiles 删除最旧的分片(名字字典序即创建序).
func (w *RollingWriter) prune() {
	files := w.Files()
	if len(files) <= w.maxFiles {
		return
	}
	for _, path := range files[:len(files)-w.maxFiles] {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			// 清理失败不影响写入主流程, 留待下次 prune 重试
			continue
		}
	}
}

// Files 返回当前存在的滚动分片路径, 按文件名字典序(即创建时间序).
func (w *RollingWriter) Files() []string {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "cap-") || !strings.HasSuffix(e.Name(), ".pcapng") {
			continue
		}
		out = append(out, filepath.Join(w.dir, e.Name()))
	}
	sort.Strings(out)
	return out
}

// Write 写一帧; 若尚无分片或当前分片已超 maxFileSize, 先滚动新分片.
func (w *RollingWriter) Write(ifaceID uint16, ts time.Time, data []byte) error {
	// 分片头(SHB+IDB)约占百字节级, 只在已有至少一帧且写满后才滚, 避免空转分片
	if w.shard != nil && (w.shard.size() > w.maxSize || int(ifaceID) >= len(w.shard.ifaces)) {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	if w.shard == nil {
		if err := w.rotate(); err != nil {
			return err
		}
	}
	return w.shard.Write(ifaceID, ts, data)
}

// Close 幂等关闭当前分片.
func (w *RollingWriter) Close() error {
	if w.shard == nil {
		return nil
	}
	shard := w.shard
	w.shard = nil
	return shard.Close()
}
