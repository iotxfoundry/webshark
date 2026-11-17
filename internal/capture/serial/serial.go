// Package serial 串口采集源：eBPF 挂 tty 层抓任意进程的串口字节流（tty 排他，
// 应用侧无法自行埋点），用户态按 Modbus RTU 3.5 字符静默定界切帧。
//
// 部署前提（不满足时 Start 返回 *capture.ErrSourceUnavailable，调用方降级跳过该源）：
//
//   - 内核 ≥ 5.8（ringbuf map 支持）；
//   - CONFIG_DEBUG_INFO_BTF（CO-RE 按 BTF 解析结构体字段）且未 lockdown BPF；
//   - root / CAP_BPF + CAP_PERFMON（kprobe 挂点与程序装载）；
//   - 目标架构 amd64 / arm64 / arm（产物已按 GOARCH 入库，无需现场编译）。
package serial

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"structs"
	"sync"
	"time"
	"unsafe"

	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"golang.org/x/sys/unix"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// bpfEventT 事件头 Go 视图，布局对应 bpf/serial_bpf.c 的 struct event_t
// （u64 ts_ns + u8 dir + 填充 + u32 len + char tty_name[64] + 变长 data[4096]，
// 尾部 4 字节对齐填充）。不取自 bpf2go：-O2 内联后 clang 会把本地结构体裁出 BTF，
// -type 采集不可复现，Go 侧按此处固定布局解样。
type bpfEventT struct {
	_       structs.HostLayout
	TsNs    uint64
	Dir     uint8
	Pad     [7]uint8
	Len     uint32
	TtyName [64]int8
	Data    [4096]uint8
	_       [4]byte
}

// structsUnmarshal 按生成结构体布局（尾随 data 变长）从样本解出事件头。
// 不能 binary.Read 整个 bpfEventT：Data[4096] 字段要求样本满 4184B，而这里
// 只取头部字段，载荷由调用方按 ev.Len 从原始样本另取。
func structsUnmarshal(b []byte, ev *bpfEventT) error {
	// bpfEventT 固定头：data 之前的全部字段（变长载荷按 len 另取）。
	const hdr = int(unsafe.Offsetof(bpfEventT{}.Data))
	if len(b) < hdr {
		return fmt.Errorf("样本过短: %d < %d", len(b), hdr)
	}
	ev.TsNs = binary.LittleEndian.Uint64(b[0:8])
	ev.Dir = b[8]
	ev.Len = binary.LittleEndian.Uint32(b[16:20])
	for i := range ev.TtyName {
		ev.TtyName[i] = int8(b[20+i])
	}
	copy(ev.Data[:], b[hdr:])
	return nil
}

// BPF 事件方向值，与 struct event_t.dir 对应。
const (
	dirTX = 0 // n_tty_write：主机发
	dirRX = 1 // n_tty_receive_buf_common：主机收
)

// SerialSource 串口 eBPF 采集源：一实例服务一组 tty 名（tty->name，如 ttyUSB0），
// 每口 TX/RX 各一个 Framer 定界，切出的 RTU 帧转 capture.Frame 推给调用方。
type SerialSource struct {
	names   []string
	baud    int
	ifaceID uint16 // 帧携带的采集接口号（调用方分配，0 保留未知）
	log     *slog.Logger

	mu    sync.Mutex
	objs  *bpfObjects
	links []link.Link
	rbs   []*ringbuf.Reader
	fr    map[string]*framerPair
	wg    sync.WaitGroup
	tsb   tsBase // 帧时刻换算基准（Start 采样）
}

// framerPair 单 tty 的 TX/RX 定界器。
type framerPair struct {
	tx *Framer
	rx *Framer
}

// NewSerialSource 新建串口源。构造只记配置，能力探测在 Start 做（失败返回
// *capture.ErrSourceUnavailable 并记一条 WARN）。ifaceID 为帧携带的采集接口号
// （调用方分配，0 保留未知）；log 为 nil 时用 slog.Default()。
func NewSerialSource(names []string, baud int, ifaceID uint16, log *slog.Logger) *SerialSource {
	if log == nil {
		log = slog.Default()
	}
	return &SerialSource{names: names, baud: baud, ifaceID: ifaceID, log: log}
}

// Name 源标识。
func (s *SerialSource) Name() string { return "serial-ebpf:" + strings.Join(s.names, ",") }

// tsBase 帧时刻换算基准：Start 时一次采样内核 CLOCK_MONOTONIC 与墙钟的对应关系。
// eBPF 事件 ts_ns 为 bpf_ktime_get_ns() 单调时刻，换算墙钟 = wall + (ts_ns − mono)。
type tsBase struct {
	mono uint64
	wall time.Time
}

// nowTs 采样一次换算基准。
func nowTs() tsBase {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		// 采样失败（实际不可达）：基准取零，全部事件钳到 wall，时刻劣化但不丢帧。
		return tsBase{mono: 0, wall: time.Now()}
	}
	return tsBase{mono: uint64(ts.Nano()), wall: time.Now()}
}

// toWall 单调时刻 → 墙钟；早于基准采样点（负偏移，时钟回拨/重排）钳到基准墙钟。
func (b tsBase) toWall(tsNs uint64) time.Time {
	off := int64(tsNs - b.mono)
	if off < 0 {
		return b.wall
	}
	return b.wall.Add(time.Duration(off))
}

// unavailable 降级出口：记一条 WARN 后返回带原因的不可用错误，调用方据此跳过该源。
func (s *SerialSource) unavailable(reason string) error {
	err := &capture.ErrSourceUnavailable{Kind: "串口", Reason: reason}
	s.log.Warn("串口采集源不可用，降级跳过", "err", err)
	return err
}

// Start 装载 eBPF（能力探测）→ 双 kprobe 挂点 → ringbuf 读循环，字节块按 tty_name
// 分流进各口 Framer，切帧转 capture.Frame（LinkType 147）推给调用方。
// 装载/挂点任一步失败即整体失败：返回带原因的 *capture.ErrSourceUnavailable（已记 WARN），
// 不起循环不 panic。
func (s *SerialSource) Start(ctx context.Context, push func(capture.Frame)) error {
	// 能力探测：装载即冒烟（校验 BTF/ringbuf/指令集）；失败带原因降级，不 panic。
	objs := &bpfObjects{}
	if err := loadBpfObjects(objs, nil); err != nil {
		// ENOTSUPP（"function not implemented"）= 内核未编 CONFIG_BPF_SYSCALL：
		// 任何 eBPF 变体都不可用。
		reason := err.Error()
		if strings.Contains(reason, "function not implemented") {
			reason = "内核未启用 eBPF 系统调用（CONFIG_BPF_SYSCALL），eBPF 变体均不可用；需换内核或重编内核启用"
		}
		return s.unavailable(fmt.Sprintf("装载 eBPF 失败（需 ≥5.8 支持 ringbuf + BTF/未 lockdown）: %s", reason))
	}

	ltx, err := link.Kprobe("n_tty_write", objs.TtyTx, nil)
	if err != nil {
		objs.Close()
		return s.unavailable(fmt.Sprintf("kprobe n_tty_write 失败（需 root/CAP_BPF+CAP_PERFMON，符号须在 kallsyms）: %v", err))
	}
	lrx, err := link.Kprobe("n_tty_receive_buf_common", objs.TtyRx, nil)
	if err != nil {
		ltx.Close()
		objs.Close()
		return s.unavailable(fmt.Sprintf("kprobe n_tty_receive_buf_common 失败: %v", err))
	}

	rd, err := ringbuf.NewReader(objs.Events)
	if err != nil {
		ltx.Close()
		lrx.Close()
		objs.Close()
		return s.unavailable(fmt.Sprintf("打开 ringbuf 失败: %v", err))
	}

	s.mu.Lock()
	s.objs = objs
	s.links = []link.Link{ltx, lrx}
	s.rbs = []*ringbuf.Reader{rd}
	s.fr = make(map[string]*framerPair)
	s.tsb = nowTs() // 采样单调→墙钟基准：帧时刻用事件 ts_ns 换算（含排队时长）
	s.mu.Unlock()

	s.wg.Go(func() { s.readLoop(rd, push) })
	// 周期 FlushIdle：静默超时切表外帧（锁长帧在收满时已即时产出）。
	s.wg.Go(func() { s.flushLoop(ctx) })
	return nil
}

// readLoop ringbuf 消费：每个 4KB 块按 tty_name 分流到对应 Framer 喂入。
func (s *SerialSource) readLoop(rd *ringbuf.Reader, push func(capture.Frame)) {
	for {
		rec, err := rd.Read()
		if err != nil {
			// Close 触发的预期退出；真实读错误（设备消失等）同样终止消费。
			return
		}
		var ev bpfEventT
		if err := structsUnmarshal(rec.RawSample, &ev); err != nil {
			continue
		}

		name := cstr(ev.TtyName)
		if !s.want(name) {
			continue
		}
		dir := capture.RX
		if ev.Dir == dirTX {
			dir = capture.TX
		}
		// 帧时刻 = 事件内核单调时刻经基准换算的墙钟（偏移含 ringbuf 排队时长）。
		now := s.tsb.toWall(ev.TsNs)
		s.feed(name, dir, ev.Data[:ev.Len], now, push)
	}
}

// flushLoop 周期（2ms）驱动全部 Framer 静默切帧。
func (s *SerialSource) flushLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			s.mu.Lock()
			if s.rbs == nil { // Stop 已清空运行态
				s.mu.Unlock()
				return
			}
			// 与 feed 同锁：Framer 非并发安全，FlushIdle 与 Feed 不得并发。
			for _, p := range s.fr {
				p.tx.FlushIdle(now)
				p.rx.FlushIdle(now)
			}
			s.mu.Unlock()
		}
	}
}

// feed 喂入单口字节流并回调产出帧。
func (s *SerialSource) feed(name string, dir capture.Direction, data []byte, now time.Time, push func(capture.Frame)) {
	s.mu.Lock()
	p, ok := s.fr[name]
	if !ok {
		p = &framerPair{}
		factory := func(d capture.Direction) *Framer {
			return NewFramer(FramerConfig{Baud: s.baud, Dir: d, OnFrame: func(fr Frame) {
				push(capture.Frame{
					IfaceID:   s.ifaceID,
					LinkType:  capture.LinkTypeUser0,
					Direction: fr.Dir,
					TS:        fr.TS,
					Data:      fr.Data,
					Fields:    fieldsOf(name, fr),
				})
			}})
		}
		p.tx = factory(capture.TX)
		p.rx = factory(capture.RX)
		s.fr[name] = p
	}
	// Feed 与 flushLoop 的 FlushIdle 共享同一 Framer（其注释自认非并发安全），
	// 全程持 s.mu 串行化；push 回调亦在锁内（调用方 push 只入缓冲，快速返回）。
	if dir == capture.TX {
		p.tx.Feed(data, now)
	} else {
		p.rx.Feed(data, now)
	}
	s.mu.Unlock()
}

// want 该 tty 是否在订阅名单内；空名单=全收。
func (s *SerialSource) want(name string) bool {
	if len(s.names) == 0 {
		return true
	}
	return slices.Contains(s.names, name)
}

// Stop 幂等停止：断挂点 + 关 ringbuf + 关 map。
func (s *SerialSource) Stop() error {
	s.mu.Lock()
	objs, links, rbs := s.objs, s.links, s.rbs
	s.objs, s.links, s.rbs = nil, nil, nil
	s.mu.Unlock()
	for _, r := range rbs {
		r.Close()
	}
	for _, l := range links {
		l.Close()
	}
	if objs != nil {
		objs.Close()
	}
	s.wg.Wait()
	return nil
}

// fieldsOf 帧 → 字段表：proto/iface/serial.tty/rtu.slave/rtu.func/rtu.dir 必填，
// CRC 失败加 rtu.crcfail=1。iface 为 tty 名：落盘与 UI Details 读 Fields["iface"]。
func fieldsOf(tty string, fr Frame) map[string]string {
	f := map[string]string{
		"proto":      "modbus-rtu",
		"iface":      tty,
		"serial.tty": tty,
		"rtu.slave":  fmt.Sprintf("%d", fr.Slave),
		"rtu.func":   fmt.Sprintf("%d", fr.Func),
		"rtu.dir":    "tx",
	}
	if fr.Dir == capture.RX {
		f["rtu.dir"] = "rx"
	}
	if !fr.CRCOK {
		f["rtu.crcfail"] = "1"
	}
	return f
}

// cstr 取定长 char 数组的 C 字符串（首个 NUL 截断）。
func cstr(b [64]int8) string {
	var sb strings.Builder
	for _, c := range b {
		if c == 0 {
			break
		}
		sb.WriteByte(byte(c))
	}
	return sb.String()
}
