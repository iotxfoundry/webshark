// Package spool 提供 pcapng 滚动分片目录（spool）的枚举与分片出现检测。
package spool

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Shard 是一条分片清单项。
type Shard struct {
	Name  string    `json:"name"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime"`
}

// DefaultRingSize 环窗分片数（对齐部署 dumpcap -b files:16）。
const DefaultRingSize = 16

// List 列出 dir 下全部 .pcapng 分片：mtime 最新在前，同 mtime 按名字降序；
// 目录不可读返回空切片（非 nil）。
func List(dir string) []Shard {
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
	files := List(dir)
	if len(files) == 0 {
		return ""
	}
	return files[0].Name
}

// Ring 返回环窗清单：List 的前 n 片；n<=0 时取 DefaultRingSize。
func Ring(dir string, n int) []Shard {
	if n <= 0 {
		n = DefaultRingSize
	}
	files := List(dir)
	if len(files) > n {
		return files[:n]
	}
	return files
}
