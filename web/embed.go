// Package web 内嵌前端构建产物。
//
// 产物目录 dist/（vite build 输出）不入库，未构建时只有 .gitkeep，
// 调用方据 index.html 是否存在决定是否回退到"未构建"提示页。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS 返回站点根文件系统（dist 子目录）。即使未构建也返回可用的 fs.FS。
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return dist
	}
	return sub
}
