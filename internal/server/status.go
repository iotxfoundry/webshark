// status.go - GET /api/status：sharkd 可达性（拨号即关，不 load 文件）+ spool
// 分片清单（mtime 降序）。不可达时 HTTP 仍 200，附 reason（含 socket 路径），
// UI 据体内容提示。
package server

import (
	"context"
	"net/http"

	"github.com/iotxfoundry/webshark/internal/spool"
	"github.com/iotxfoundry/webshark/sharkd"
)

// handleStatus GET /api/status。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	probeCtx, cancel := context.WithTimeout(r.Context(), statusProbeTimeout)
	defer cancel()
	connected := sharkd.Probe(probeCtx, s.cfg.SharkdSock)
	body := map[string]any{"connected": connected,
		// 与实时流同窗口：环外遗留分片（dumpcap 重启换代残留）不进计数。
		"files": spool.Ring(s.cfg.SpoolDir, s.cfg.RingSize)}
	if !connected {
		body["reason"] = "sharkd socket 不可拨: " + s.cfg.SharkdSock
	}
	writeJSON(w, body)
}
