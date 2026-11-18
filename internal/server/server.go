// Package server 是 webshark 的 HTTP 服务层：/api/* 路由（status/download/
// subscribe/frame/follow/tap/check/complete/ws）+ SPA 静态站点 + token 鉴权。
// 装配 internal/stream 的实时流管理器与分片缓存实例；行为契约见 docs/api.md。
package server

import (
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/iotxfoundry/webshark/internal/stream"
	"github.com/iotxfoundry/webshark/sharkd"
	"github.com/iotxfoundry/webshark/web"
)

// statusProbeTimeout status 探活拨号超时。
const statusProbeTimeout = time.Second

// 字体格式显式注册进进程 mime 表：Go 内建表不含 woff/woff2，未知扩展名时
// http.ServeContent 走内容嗅探回退（Seek 折返），而内嵌站点 FS（zip）不支持
// Seek → 500 "seeker can't seek"。精简容器镜像常无 /etc/mime.types，不能
// 依赖系统 mime 库。AddExtensionType 会覆盖系统库同名映射，行为即固定。
func init() {
	_ = mime.AddExtensionType(".woff2", "font/woff2")
	_ = mime.AddExtensionType(".woff", "font/woff")
	_ = mime.AddExtensionType(".ttf", "font/ttf")
}

// Config 服务配置（零值可用：Web 回落内嵌站点，Log 回落默认 logger）。
type Config struct {
	// SpoolDir pcapng 分片目录。
	SpoolDir   string
	SharkdSock string       // sharkd UDS
	RingSize   int          // 0 → spool.DefaultRingSize
	Token      string       // 空 = 不鉴权
	Web        fs.FS        // 静态站点, nil → web.FS()
	Log        *slog.Logger // nil → slog.Default()
}

// Server HTTP 服务：流管理器 + 分片缓存实例 + 静态站点。
type Server struct {
	// cfg 服务配置。
	cfg        Config
	log        *slog.Logger
	mgr        *stream.Manager
	cache      *shardCache
	probe      *sharkd.Client
	probeMu    sync.Mutex
	upgr       websocket.Upgrader
	webFS      fs.FS
	hasSPA     bool
	close      func() error
	pongWait   time.Duration // WS 读超时（死连接回收）
	pingPeriod time.Duration // WS ping 保活周期
}

// New 装配服务（不做 IO：spool 目录与 sharkd 可达性由 handler 运行期报告）。
func New(cfg Config) *Server {
	// 零值回落：Web 内嵌站点、Log 默认 logger。
	if cfg.Web == nil {
		cfg.Web = web.FS()
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	mgr := stream.NewManager(stream.Config{Sock: cfg.SharkdSock, SpoolDir: cfg.SpoolDir, RingSize: cfg.RingSize, Log: cfg.Log})
	s := &Server{
		cfg:        cfg,
		log:        cfg.Log,
		mgr:        mgr,
		cache:      newShardCache(cfg.SharkdSock, cfg.SpoolDir),
		probe:      sharkd.NewClient(cfg.SharkdSock),
		upgr:       websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: checkOrigin},
		webFS:      cfg.Web,
		pongWait:   defaultPongWait,
		pingPeriod: defaultPingPeriod,
	}
	index, err := fs.Stat(cfg.Web, "index.html")
	s.hasSPA = err == nil && !index.IsDir()
	s.close = func() error {
		mgr.Close()
		s.cache.Close()
		_ = s.probe.Close() // nil 接收者安全
		s.probe = nil
		return nil
	}
	return s
}

// Handler 完整路由：鉴权（仅 /api/*）→ /api/* 各端点 + SPA 静态回落。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/download", s.handleDownload)
	mux.HandleFunc("POST /api/subscribe", s.handleSubscribe)
	mux.HandleFunc("GET /api/frame", s.handleFrame)
	mux.HandleFunc("POST /api/follow", s.handleStreamRPC("follow"))
	mux.HandleFunc("POST /api/tap", s.handleStreamRPC("tap"))
	mux.HandleFunc("GET /api/check", s.handleCheck)
	mux.HandleFunc("POST /api/check", s.handleCheck)
	mux.HandleFunc("GET /api/complete", s.handleComplete)
	mux.HandleFunc("POST /api/complete", s.handleComplete)
	mux.HandleFunc("GET /api/ws", s.handleWS)
	// /api/ 前缀未知路径：404 JSON（Go 1.22 ServeMux 未匹配 /api/ 时才到这）
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, http.StatusNotFound, "接口不存在")
	})
	mux.Handle("/", s.staticHandler())
	return s.auth(mux)
}

// Close 收尾后台资源（流管理器、分片缓存连接）。幂等。
func (s *Server) Close() error {
	if s.close != nil {
		s.close()
		s.close = nil
	}
	return nil
}

// staticHandler SPA 静态服务：未构建（无 index.html）回提示页；已构建时文件
// 直接命中，未知路径回落 index.html（前端 HashRouter，无服务端路由）。
// 目录列表禁用：Go http.FileServer 对目录回 index.html，这里自定义恒回文件或
// 提示页，不列目录。
func (s *Server) staticHandler() http.Handler {
	// fs.Sub 只读子树，防穿越由 http.FileServer + fs.FS 语义保证。
	fileServer := http.FileServer(http.FS(s.webFS))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r) // 目录列表禁用（尾斜杠目录请求）
			return
		}
		if s.hasSPA {
			// 未知路径回落 index.html：fs.Stat 命中文件才走 FileServer
			if _, err := fs.Stat(s.webFS, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				data, rerr := fs.ReadFile(s.webFS, "index.html")
				if rerr != nil {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				_, _ = w.Write(data)
				return
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(placeholderHTML))
	})
}

// placeholderHTML 前端未构建时的提示页。
const placeholderHTML = `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>Webshark - 报文分析</title></head>
<body><h1>webshark 前端未构建</h1>
<p>静态站点缺失，请先在仓库根目录执行 <code>make web</code> 构建前端产物后重启服务。</p>
<p>HTTP 接口（/api/*）不受影响，可直接调用。</p></body></html>`
