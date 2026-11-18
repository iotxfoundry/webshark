//go:build linux

// pktdump 串口/CAN 采集装配器: 串口(eBPF 旁听内核 tty 层)与 CAN(SocketCAN RAW)
// 多源并发采集, 合并写同一 pcapng 滚动分片(RollingWriter, cap-*.pcapng), 与
// dumpcap 产出的 net_*.pcapng 同目录共存, webshark 环窗一并可见. 生命周期由
// entrypoint 守护循环托管: 进程退出 2s 后被拉起, 崩溃自愈不进本程序职责.
//
// 部署前提(能力/设备/宿主内核)见 build/entrypoint.sh 头注释与
// docs/deployment.md「串口与 CAN」; 各源启动探测失败打 WARN 跳过该源,
// 全部源不可用退出码 1, 任一源致命错误取消其余源后退出.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/iotxfoundry/webshark/internal/capture"
	"github.com/iotxfoundry/webshark/internal/capture/can"
	"github.com/iotxfoundry/webshark/internal/capture/pcapng"
	"github.com/iotxfoundry/webshark/internal/capture/serial"
)

// defaultBaud --serial 未带 @波特率时的缺省值.
const defaultBaud = 115200

// serialPort 单串口配置: tty 为内核 tty 名(ttyUSB0, eBPF 事件按 tty->name 匹配,
// 故剥掉 /dev/ 前缀); baud 只影响成帧的 3.5 字符静默时长, 不碰 termios.
type serialPort struct {
	tty      string
	baud     int
	linkType capture.LinkType // 帧盖章链路类型(协议→USERn)
}

// parseSerial 解析 --serial 取值: 逗号分隔多口, 每口 `路径@波特率`(缺省 115200),
// 空段忽略.
func parseSerial(s string) ([]serialPort, error) {
	var out []serialPort
	for seg := range strings.SplitSeq(s, ",") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		p := serialPort{tty: seg, baud: defaultBaud, linkType: capture.LinkTypeUser0}
		if i := strings.LastIndex(seg, "@"); i > 0 {
			spec := seg[i+1:]
			if j := strings.IndexByte(spec, ':'); j >= 0 {
				lt := serialProtoMap[spec[j+1:]]
				if lt == 0 {
					return nil, fmt.Errorf("--serial 协议标签非法 %q (支持 modbus/ft12/645/698)", seg)
				}
				p.linkType = lt
				spec = spec[:j]
			}
			b, err := strconv.Atoi(spec)
			if err != nil {
				return nil, fmt.Errorf("--serial 波特率非法 %q: %w", seg, err)
			}
			if b <= 0 {
				return nil, fmt.Errorf("--serial 波特率须为正: %q", seg)
			}
			p.tty, p.baud = seg[:i], b
		}
		p.tty = strings.TrimPrefix(p.tty, "/dev/")
		out = append(out, p)
	}
	return out, nil
}

// splitNames 解析逗号分隔的接口名(--can): 去首尾空白, 空段忽略.
func splitNames(s string) []string {
	var out []string
	for seg := range strings.SplitSeq(s, ",") {
		if seg = strings.TrimSpace(seg); seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// source 采集源最小契约: serial.SerialSource 与 can.CANSource 均满足,
// Start 兼作启动探测(环境不可用返回 *capture.ErrSourceUnavailable), 测试注入桩源.
type source interface {
	Name() string
	Start(ctx context.Context, push func(capture.Frame)) error
	Stop() error
}

// isUnavailable 源启动失败是否为环境不可用类(接口不存在/能力缺失/内核过旧等):
// 此类失败降级跳过该源, 其余启动错误视为致命.
func isUnavailable(err error) bool {
	var ue *capture.ErrSourceUnavailable
	return err != nil && errors.As(err, &ue)
}

// frameSink 共享落盘汇聚点: 全部源并发推送, RollingWriter 非并发安全, 互斥锁
// 串行化; CAN 帧落盘前经 CanFrameBE 换转 can_id 字节序; 首个写错误留存并取消
// 全体采集(退出码 1, 由守护循环拉起重试), 其后到达的帧静默丢弃.
type frameSink struct {
	w      *pcapng.RollingWriter
	cancel context.CancelFunc
	log    *slog.Logger

	mu    sync.Mutex
	first error
}

// push 采集源回调: 写一帧入滚动分片.
func (s *frameSink) push(f capture.Frame) {
	data := f.Data
	if f.LinkType == capture.LinkTypeSocketCAN {
		data = pcapng.CanFrameBE(data)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.first != nil {
		return
	}
	if err := s.w.Write(f.IfaceID, f.TS, data); err != nil {
		s.first = fmt.Errorf("落盘失败: %w", err)
		s.log.Error("落盘失败, 取消全体采集", "err", s.first)
		s.cancel()
	}
}

// firstErr 返回首个落盘写错误(无则 nil).
func (s *frameSink) firstErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.first
}

// runSources 并发运行采集源: Start 即启动探测 —— 环境不可用记 WARN 跳过该源
// (不致命), 其余启动错误取消其余源整体退出; ctx 取消(SIGTERM/落盘错误)后逐源
// Stop 并正常返回 nil. 返回实际启动的源数与首个致命错误.
func runSources(ctx context.Context, log *slog.Logger, push func(capture.Frame), srcs []source) (int, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		started bool
		err     error
	}
	resCh := make(chan result, len(srcs))
	for _, s := range srcs {
		go func(s source) {
			var res result
			defer func() { resCh <- res }()
			if err := s.Start(runCtx, push); err != nil {
				if isUnavailable(err) {
					log.Warn("采集源不可用, 跳过该源", "src", s.Name(), "err", err)
					return
				}
				res.err = fmt.Errorf("源 %s 启动失败: %w", s.Name(), err)
				cancel()
				return
			}
			res.started = true
			<-runCtx.Done()
			if err := s.Stop(); err != nil {
				log.Error("停止采集源", "src", s.Name(), "err", err)
			}
		}(s)
	}
	started := 0
	var first error
	for range srcs {
		res := <-resCh
		if res.started {
			started++
		}
		if res.err != nil && first == nil {
			first = res.err
		}
	}
	return started, first
}

func main() {
	os.Exit(run())
}

// run 装配并运行采集, 返回进程退出码: 0 优雅退出(SIGTERM/SIGINT), 1 采集失败
// (全部源不可用/致命错误/落盘失败), 2 用法错误.
func run() int {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	var (
		serialArg = flag.String("serial", "", `串口列表, 逗号分隔 "/dev/ttyUSB0@115200,/dev/ttyUSB1@9600", @波特率缺省 115200(仅用于成帧静默时序, 协议无关); 空=不采串口`)
		canArg    = flag.String("can", "", "SocketCAN 接口名, 逗号分隔 can0,can1; 空=不采 CAN")
		spool     = flag.String("spool", "/var/lib/webshark", "pcapng 滚动分片目录")
		roll      = flag.Int64("roll", 8388608, "单分片字节数")
		files     = flag.Int("files", 16, "保留分片数")
	)
	flag.Parse()

	ports, err := parseSerial(*serialArg)
	if err != nil {
		log.Error("参数错误", "err", err)
		return 2
	}
	ifaces := splitNames(*canArg)
	if len(ports) == 0 && len(ifaces) == 0 {
		log.Error("未配置任何采集源: --serial 与 --can 均为空")
		return 2
	}

	w, err := pcapng.NewRolling(*spool, *roll, *files)
	if err != nil {
		log.Error("初始化滚动写入器失败", "err", err)
		return 1
	}

	// 候选源按声明序(串口各口 → CAN 各口)分配接口号, 接口块全量登记: 未就绪的源
	// 不产帧, 接口块仅是分片元数据; 接口号=声明序, 帧与 IDB 恒一致.
	var srcs []source
	for _, p := range ports {
		id, err := w.AddInterface(capture.LinkTypeUser0, p.tty)
		if err != nil {
			log.Error("登记串口接口失败", "iface", p.tty, "err", err)
			_ = w.Close()
			return 1
		}
		srcs = append(srcs, serial.NewSerialSource([]string{p.tty}, p.baud, id, p.linkType, log))
	}
	for _, name := range ifaces {
		id, err := w.AddInterface(capture.LinkTypeSocketCAN, name)
		if err != nil {
			log.Error("登记 CAN 接口失败", "iface", name, "err", err)
			_ = w.Close()
			return 1
		}
		srcs = append(srcs, can.NewCANSource(name, id))
	}

	// SIGTERM/SIGINT 优雅退出; 内层 cancel 供落盘错误/源致命错误提前收束全体.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	sink := &frameSink{w: w, cancel: cancel, log: log}

	log.Info("采集启动", "spool", *spool, "sources", len(srcs))
	started, runErr := runSources(runCtx, log, sink.push, srcs)
	if runErr == nil {
		runErr = sink.firstErr()
	}
	if err := w.Close(); err != nil {
		log.Error("关闭滚动写入器", "err", err)
	}
	if started == 0 {
		log.Error("全部采集源不可用, 退出(守护循环将拉起重试)")
		return 1
	}
	if runErr != nil {
		log.Error("采集异常退出", "err", runErr)
		return 1
	}
	log.Info("采集退出")
	return 0
}
