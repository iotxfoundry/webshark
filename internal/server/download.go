// download.go - GET /api/download：spool 全部分片按 mtime 升序逐文件 io.Copy
// 流式拼接为单一 pcapng（pcapng 允许多 SHB/IDB 段共存，纯字节拼接即合法文件）。
// 不设 Content-Length（滚动分片随时增删），Go http 自动走 chunked；单文件打开
// 失败（轮换瞬间被删）跳过；无分片 404。
package server

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/iotxfoundry/webshark/internal/spool"
)

// handleDownload GET /api/download。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	files := spool.List(s.cfg.SpoolDir) // mtime 降序 → 反转为时间升序（拼接后报文时间序才正确）
	slices.Reverse(files)
	if len(files) == 0 {
		writeErr(w, http.StatusNotFound, http.StatusNotFound, "spool 无 pcapng 分片可导出")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(
		"attachment; filename=%q", fmt.Sprintf("webshark-%s.pcapng", time.Now().Format("20060102150405"))))
	for _, f := range files {
		fp, err := os.Open(filepath.Join(s.cfg.SpoolDir, f.Name))
		if err != nil {
			continue // 轮换被删：跳过
		}
		_, _ = io.Copy(w, fp)
		_ = fp.Close()
	}
}
