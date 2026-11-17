// download.go - GET /api/download：环窗（最近 -ring 片，默认 16）分片按 mtime
// 升序逐文件 io.Copy 流式拼接为单一 pcapng（pcapng 允许多 SHB/IDB 段共存，
// 纯字节拼接即合法文件）。不设 Content-Length（滚动分片随时增删），Go http
// 自动走 chunked；单文件打开失败（轮换瞬间被删）跳过；无分片 404；分片均在
// 但不可读（无一可打开）时 503。
package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/iotxfoundry/webshark/internal/spool"
)

// handleDownload GET /api/download。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	files := spool.RingAsc(s.cfg.SpoolDir, s.cfg.RingSize) // 环窗，mtime 升序（拼接后报文时间序才正确）
	if len(files) == 0 {
		writeErr(w, http.StatusNotFound, http.StatusNotFound, "spool 无 pcapng 分片可导出")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"attachment; filename=%q", fmt.Sprintf("webshark-%s.pcapng", time.Now().Format("20060102150405"))))
	var opened int // 成功打开的文件数（按可读性判，不以写出字节判——0 字节分片合法）
	for _, f := range files {
		fp, err := os.Open(filepath.Join(s.cfg.SpoolDir, f.Name))
		if err != nil {
			continue // 轮换被删：跳过
		}
		opened++
		_, _ = io.Copy(w, fp)
		_ = fp.Close()
	}
	if opened == 0 {
		// 无一可打开，直接覆盖应答为 503（writeErr 重设 Content-Type 与状态码）
		writeErr(w, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "分片均不可读（权限或已被清理）")
		return
	}
}
