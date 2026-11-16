// status.go - GET /api/status：sharkd 可达性（拨号即关，不 load 文件）+ spool
// 分片清单（mtime 降序）。不可达时 HTTP 仍 200，附 reason（含 socket 路径），
// UI 据体内容提示。
package server

import (
	"net/http"

	"github.com/iotxfoundry/webshark/internal/sharkd"
	"github.com/iotxfoundry/webshark/internal/spool"
)

// handleStatus GET /api/status。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	connected := sharkd.Probe(s.cfg.SharkdSock, statusProbeTimeout)
	body := map[string]any{"connected": connected,
		// 与实时流同窗口：环外遗留分片（dumpcap 重启换代残留）不进计数。
		"files": spool.Ring(s.cfg.SpoolDir, s.cfg.RingSize)}
	if !connected {
		body["reason"] = "sharkd socket 不可拨: " + s.cfg.SharkdSock
	}
	writeJSON(w, body)
}
