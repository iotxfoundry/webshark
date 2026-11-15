// Package webshark 是 webshark 的对外可嵌入 API：把 webshark（Wireshark 的
// Web 化查看器）作为库嵌入你的 Go 服务，一行装配即得「实时抓包流 + 历史帧
// 详情 + 分片合并导出 + 静态界面」的完整 HTTP 服务。
//
// webshark 自身不生产 pcapng，也不解析协议：pcapng 分片由外部 dumpcap 以环形
// 滚动方式写入 spool 目录（如 dumpcap -b filesize:8388608 -b files:16 -w
// <spool>/net.pcapng），协议解析由外部 sharkd daemon 承担（经 Unix socket
// 访问）。本库只做装配、路由与流式推送。接口契约见仓库 docs/api.md。
//
// # 最小用法
//
//	srv, err := webshark.New(webshark.Config{
//		SpoolDir:   "/var/lib/webshark",
//		SharkdSock: "/run/webshark/sharkd.sock",
//	})
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer srv.Close()
//	http.ListenAndServe("127.0.0.1:8080", srv.Handler())
//
// Token 非空时 /api/* 需要 Authorization: Bearer 或 ?token=（WebSocket 与下载
// 直链用后者）；静态站点恒公开，使用者先打开界面录入 token。
package webshark

import (
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/iotxfoundry/webshark/internal/server"
)

// Config 服务配置；路径必填，其余零值可用（Token 空=不鉴权、Web 空=内嵌站点）。
type Config struct {
	SpoolDir   string       // pcapng 分片目录（dumpcap -b 滚动写入）
	SharkdSock string       // sharkd daemon 的 Unix socket 路径
	RingSize   int          // 环窗分片数, 0 → 默认 16（对齐 dumpcap -b files:16）
	Token      string       // API 鉴权 token, 空 = 不鉴权
	Web        fs.FS        // 静态站点根, nil → 内嵌 web/dist.zip（未构建时回提示页）
	Log        *slog.Logger // nil → slog.Default()
}

// Server 可嵌入的 webshark 服务（内聚 internal/server 装配）。
type Server struct {
	impl *server.Server
}

// New 校验必填路径非空后装配服务（不做 IO：spool 目录与 sharkd 可达性由服务
// 运行期在 /api/status 等接口报告）。
func New(cfg Config) (*Server, error) {
	if cfg.SpoolDir == "" {
		return nil, errRequired("SpoolDir")
	}
	if cfg.SharkdSock == "" {
		return nil, errRequired("SharkdSock")
	}
	return &Server{impl: server.New(server.Config(cfg))}, nil
}

// Handler 完整 HTTP 路由：/api/* + SPA 静态站点。
func (s *Server) Handler() http.Handler { return s.impl.Handler() }

// Close 收尾后台资源（流管理器、sharkd 连接）。幂等。
func (s *Server) Close() error { return s.impl.Close() }

// errRequired 必填字段缺失错误。
func errRequired(field string) error {
	return &requiredError{Field: field}
}

type requiredError struct{ Field string }

func (e *requiredError) Error() string { return "webshark: Config." + e.Field + " 不能为空" }
