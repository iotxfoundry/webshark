// status.go - GET /api/status：sharkd 可达性 + 环窗（最近 -ring 片，默认 16）
// 分片清单（mtime 降序，同 mtime 按名字降序）。不可达时 HTTP 仍 200，附
// reason（含 socket 路径），UI 据体内容提示。
//
// 可达性不走「拨号即关」探活：sharkd daemon 对每个连接 fork 一个子进程，
// 高频探活连接会造成子进程（含僵尸）无限堆积；改用常驻探活会话——对
// sharkd 发一次极轻的 check（空 filter 恒 OK）判定可达，会话断线时下次
// 调用自动重拨。
package server

import (
	"context"
	"net/http"

	"github.com/iotxfoundry/webshark/internal/spool"
	"github.com/iotxfoundry/webshark/sharkd"
)

// probeConnected 经常驻会话探测 sharkd 可达性：check 空参恒 OK。会话断线
// （sharkd 重启/子进程退出）时丢弃连接，下次调用自动重拨（新 fork 一次）。
func (s *Server) probeConnected(ctx context.Context) bool {
	s.probeMu.Lock()
	defer s.probeMu.Unlock()
	if s.probe == nil {
		s.probe = sharkd.NewClient(s.cfg.SharkdSock)
	}
	_, err := s.probe.Call(ctx, "check", map[string]string{})
	if err != nil {
		_ = s.probe.Close()
		s.probe = nil
		return false
	}
	return true
}

// handleStatus GET /api/status。
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), statusProbeTimeout)
	defer cancel()
	connected := s.probeConnected(ctx)
	body := map[string]any{"connected": connected,
		// 与实时流同窗口：环外遗留分片（dumpcap 重启换代残留）不进计数。
		"files": spool.Ring(s.cfg.SpoolDir, s.cfg.RingSize)}
	if !connected {
		body["reason"] = "sharkd socket 不可拨: " + s.cfg.SharkdSock
	}
	writeJSON(w, body)
}
