// Package web 内嵌前端构建产物。
//
// 产物以**单个 zip** 入库（web/dist.zip）：vite 输出的资源文件名带内容哈希，
// 逐文件入库会让每次前端改动产生成片的文件增删与重命名；打成单个 zip 后仓库
// 只多一个二进制差异。zip 内容是站点根目录树，运行时以 archive/zip 直接作为
// fs.FS 使用（无需解压到磁盘）。
//
// 未构建（空包或包损坏）时 Open 返回 ErrNotExist，调用方回退“前端未构建”
// 提示页，不拖垮服务。
package web

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"io/fs"
)

//go:embed dist.zip
var distZip []byte

// zipFS 站点文件系统（dist 目录树的 zip 视图）。
//
// zip.Reader 非并发安全（多文件 Open/Read 共享内部游标），而 HTTP 层各请求
// goroutine 会并发读静态资源——共用一个 Reader 会让浏览器并行拉取时偶发
// seek 失败（ServeContent 报 500 "seeker can't seek"）。故每次 Open 用只读
// 字节重建独立 Reader：中央目录解析是对嵌入字节的内存扫描，站点规模下开销
// 可忽略。包损坏时每次 Open 返回 ErrNotExist（视同站点缺失）。
type zipFS []byte

func (z zipFS) Open(name string) (fs.File, error) {
	zr, err := zip.NewReader(bytes.NewReader(z), int64(len(z)))
	if err != nil {
		return nil, fs.ErrNotExist
	}
	return zr.Open(name)
}

// FS 返回站点根文件系统（并发安全）。
func FS() fs.FS { return zipFS(distZip) }
