// webshark 命令行入口：装配 HTTP 服务并监听（实时抓包流 + 历史详情 + 合并导出
// + 静态界面）。外部依赖：sharkd daemon（UDS）与 dumpcap（写 spool 分片目录）。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/iotxfoundry/webshark"
)

// version 版本号（-ldflags 注入）。
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
}

func run() error {
	var (
		listen = flag.String("listen", "127.0.0.1:8080", "HTTP 监听地址")
		sharkd = flag.String("sharkd", "/run/webshark/sharkd.sock", "sharkd daemon 的 Unix socket 路径")
		spoolD = flag.String("spool", "/var/lib/webshark", "pcapng 分片目录（dumpcap 滚动写入）")
		tokenF = flag.String("token", "", "API 鉴权 token（为空时读环境变量 WEBSHARK_TOKEN）")
		ring   = flag.Int("ring", 16, "环窗分片数")
		webDir = flag.String("web-dir", "", "静态站点磁盘目录（开发用；空 = 内嵌产物）")
		ver    = flag.Bool("version", false, "打印版本退出")
	)
	flag.Parse()
	if *ver {
		fmt.Println(version)
		return nil
	}
	token := *tokenF
	if token == "" {
		token = os.Getenv("WEBSHARK_TOKEN")
	}

	// 安全阀：非 loopback 监听且未配 token 会把抓包数据暴露到任意来源，拒绝启动。
	if !isLoopbackListen(*listen) && token == "" {
		return fmt.Errorf("拒绝启动: 监听地址 %s 非本机回环且未配置 token（-token 或环境变量 WEBSHARK_TOKEN），存在数据暴露风险", *listen)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg := webshark.Config{SpoolDir: *spoolD, SharkdSock: *sharkd, RingSize: *ring, Token: token, Log: log}
	if *webDir != "" {
		cfg.Web = os.DirFS(*webDir)
	}
	srv, err := webshark.New(cfg)
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	ln, lerr := net.Listen("tcp", *listen)
	if lerr != nil {
		return lerr
	}
	go func() {
		log.Info("webshark 启动", "listen", *listen, "spool", *spoolD, "sharkd", *sharkd, "auth", token != "")
		if serr := hs.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			errCh <- serr
		}
	}()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if serr := hs.Shutdown(shutCtx); serr != nil {
		return serr
	}
	return srv.Close()
}

// isLoopbackListen 报告监听地址是否仅本机可达（主机名为 loopback 字面量或解析
// 出的全是回环 IP；解析失败按非回环保守处理）。
func isLoopbackListen(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return false
	}
	for _, ip := range ips {
		if !ip.IsLoopback() {
			return false
		}
	}
	return true
}
