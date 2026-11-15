// middleware.go - token 鉴权中间件与 WebSocket 同源校验。token 为空全部放行；
// 非空时只保护 /api/*（静态站点必须公开，使用者才能打开界面录入 token）。
package server

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// auth 鉴权中间件：仅拦 /api/*，静态资源恒放行。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" || !strings.HasPrefix(r.URL.Path, "/api/") || s.checkToken(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeErr(w, http.StatusUnauthorized, http.StatusUnauthorized, "未认证")
	})
}

// checkToken 校验凭证：Authorization: Bearer <token> 优先，回落 ?token=<token>
// （WebSocket 握手与下载直链无法注入 header）。常量时间比较。
func (s *Server) checkToken(r *http.Request) bool {
	want := []byte(s.cfg.Token)
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(auth, "Bearer ")), want) == 1
	}
	tok := r.URL.Query().Get("token")
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), want) == 1
}

// checkOrigin WebSocket 同源校验：Origin 为空（非浏览器客户端）放行；非空要求
// host 与请求 Host 一致（防跨站 WebSocket 劫持），升级器校验失败即回 403。
func checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Host == r.Host
}
