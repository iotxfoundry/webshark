// Package spool 提供 pcapng 滚动分片目录（spool）的枚举与分片出现检测。
package spool

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"
)

// Shard 是一条分片清单项。
type Shard struct {
	// Name 分片文件名（spool 内 basename）。
	Name string `json:"name"`
	// Size 文件字节数。
	Size int64 `json:"size"`
	// Mtime 文件修改时刻。
	Mtime time.Time `json:"mtime"`
}

// DefaultRingSize 环窗分片数（对齐部署 dumpcap -b files:16）。
const DefaultRingSize = 16

// List 列出 dir 下全部 .pcapng 分片：mtime 最新在前，同 mtime 按名字降序；目录不可读返回空切片（非 nil）。
func List(dir string) []Shard {
	// 只取普通文件，忽略目录与扩展名非 .pcapng 的条目。
	des, err := os.ReadDir(dir)
	if err != nil {
		return []Shard{}
	}
	files := make([]Shard, 0, len(des))
	for _, de := range des {
		if de.IsDir() || filepath.Ext(de.Name()) != ".pcapng" {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		files = append(files, Shard{Name: de.Name(), Size: info.Size(), Mtime: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].Mtime.Equal(files[j].Mtime) {
			return files[i].Mtime.After(files[j].Mtime)
		}
		return files[i].Name > files[j].Name
	})
	return files
}

// Latest 返回最新分片名；无分片返回空串。
func Latest(dir string) string {
	// 复用 List 排序，队首即最新。
	files := List(dir)
	if len(files) == 0 {
		return ""
	}
	return files[0].Name
}

// Ring 返回环窗清单：List 的前 n 片；n<=0 时取 DefaultRingSize。
func Ring(dir string, n int) []Shard {
	// 环窗缺省。
	if n <= 0 {
		n = DefaultRingSize
	}
	files := List(dir)
	if len(files) > n {
		return files[:n]
	}
	return files
}

// RingAsc 返回环窗清单 mtime 升序（拼接与补推走查的时间序）。
func RingAsc(dir string, n int) []Shard {
	// Ring 降序结果反转即升序。
	files := Ring(dir, n)
	slices.Reverse(files)
	return files
}
