// Package web 内嵌前端构建产物。
//
// 产物以**单个 zip** 入库（web/dist.zip）：vite 输出的资源文件名带内容哈希，
// 逐文件入库会让每次前端改动产生成片的文件增删与重命名；打成单个 zip 后仓库
// 只多一个二进制差异。zip 内容是站点根目录树，运行时直接以 archive/zip 的
// Reader 作为 fs.FS 使用（无需解压到磁盘）。
//
// 未构建（空包或包损坏）时 Open("index.html") 返回 ErrNotExist，调用方回退
// "前端未构建"提示页，不拖垮服务。
package web

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io/fs"
	"sync"
)

//go:embed dist.zip
var distZip []byte

// fsOnce 解析 zip 中央目录（幂等；包损坏时回落空 FS，不 panic）。
var fsOnce = sync.OnceValue(func() fs.FS {
	zr, err := zip.NewReader(bytes.NewReader(distZip), int64(len(distZip)))
	if err != nil {
		return emptyFS{}
	}
	return zr
})

// FS 返回站点根文件系统（dist 目录树的 zip 视图）。
func FS() fs.FS { return fsOnce() }

// emptyFS 站点缺失时的空文件系统（任何 Open 都是 ErrNotExist）。
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
