//go:build linux

// Package can 基于 Linux SocketCAN(AF_CAN RAW socket)的 CAN 总线采集源:
// 采集点在 OS 层, 任何进程(can-utils/插件/用户程序)在该接口上的收发帧全部可见.
// CAN_RAW socket 缺省过滤规则为全收; 要收窄(只收指定 can_id)可用 CAN_RAW_FILTER
// setsockopt 注入过滤器数组, v1 不做, 留作后续扩展.
//
// 帧落盘 LinkType 227(LINKTYPE_CAN_SOCKETCAN), Wireshark 内建 SocketCAN dissector
// 直接深解析 —— 因此 Data 必须携带完整 can_frame/canfd_frame 原始字节(含头部).
package can

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// SocketCAN 帧布局(内核 linux/can.h), 长度即帧型判据: CAN_RAW_FD_FRAMES 开启后
// 同一 socket 上经典帧读出 16B(can_frame), FD 帧读出 72B(canfd_frame),
// 帧内无独立类型位 —— 读循环按单帧 read 的返回长度区分.
const (
	classicFrameLen = 16 // struct can_frame: can_id u32 + len u8 + pad u8x2 + data[8], 4 字节对齐
	fdFrameLen      = 72 // struct canfd_frame: can_id u32 + len u8 + flags u8 + res u8x2 + data[64]
)

// can_id 高位标志位(余下低位为仲裁 id).
const (
	canIDEFF = 0x80000000 // EFF: 扩展帧(29 位 id), 否则标准帧(11 位)
	canIDRTR = 0x40000000 // RTR: 远程请求帧(线上不携带数据字节)
)

// ParseCanFrame 解析 SocketCAN 原始帧(用户态纯函数): 经典 16B 与 FD 72B 两种布局,
// 按输入长度区分(采集侧单帧 read 的返回长度即帧型). 字段表:
// can.id(hex)/can.ext/can.rtr/can.fd/can.dlc(线上 DLC 码)/can.len(实际数据字节数).
func ParseCanFrame(b []byte) (map[string]string, error) {
	switch len(b) {
	case classicFrameLen:
		return parseClassic(b)
	case fdFrameLen:
		return parseFD(b)
	default:
		return nil, fmt.Errorf("CAN 帧长度非法: %d(应为 %d 或 %d)", len(b), classicFrameLen, fdFrameLen)
	}
}

// parseClassic struct can_frame: can_id u32(LE) + len u8 + pad[2] + data[8].
func parseClassic(b []byte) (map[string]string, error) {
	n := int(b[4])
	if n > 8 {
		return nil, fmt.Errorf("经典帧 DLC 非法: %d > 8", n)
	}
	raw := binary.LittleEndian.Uint32(b[0:4])
	f := baseFields(raw, false)
	f["can.dlc"] = strconv.Itoa(n)
	f["can.len"] = strconv.Itoa(n)
	if raw&canIDRTR != 0 {
		f["can.len"] = "0" // 远程帧线上无数据字节, DLC 仅是请求长度
	}
	return f, nil
}

// parseFD struct canfd_frame: can_id u32 + len u8 + flags u8(BRS 0x01/ESI 0x02/FDF 0x04)
// + res[2] + data[64]. flags 位 v1 不出字段, 原始字节已全量落盘供离线解析.
func parseFD(b []byte) (map[string]string, error) {
	n := int(b[4])
	if n > 64 {
		return nil, fmt.Errorf("FD 帧长度非法: %d > 64", n)
	}
	f := baseFields(binary.LittleEndian.Uint32(b[0:4]), true)
	f["can.dlc"] = strconv.Itoa(fdDlc(n))
	f["can.len"] = strconv.Itoa(n)
	return f, nil
}

// baseFields 公共字段: proto/can.id/can.ext/can.rtr/can.fd.
func baseFields(raw uint32, fd bool) map[string]string {
	return map[string]string{
		"proto":   "can",
		"can.id":  fmt.Sprintf("0x%X", raw&0x1FFFFFFF),
		"can.ext": strconv.FormatBool(raw&canIDEFF != 0),
		"can.rtr": strconv.FormatBool(raw&canIDRTR != 0),
		"can.fd":  strconv.FormatBool(fd),
	}
}

// fdDlc CAN FD 线上 DLC 码: ≤8 直传, 更长按固定档 12/16/20/24/32/48/64 → 9..15.
func fdDlc(n int) int {
	switch {
	case n <= 8:
		return n
	case n <= 12:
		return 9
	case n <= 16:
		return 10
	case n <= 20:
		return 11
	case n <= 24:
		return 12
	case n <= 32:
		return 13
	case n <= 48:
		return 14
	default:
		return 15
	}
}

// CANSource 单 CAN 接口采集源: 一个 AF_CAN RAW socket, 读循环单协程.
type CANSource struct {
	iface   string
	ifaceID uint16 // 帧携带的采集接口号(引擎分配, 0 保留未知)
	mu      sync.Mutex
	fd      int
	started bool
	stopped atomic.Bool
	wg      sync.WaitGroup // 读循环
}

// NewCANSource 新建 CAN 源. 构造只记配置, 探测在 Start 做(失败返回 *capture.ErrSourceUnavailable).
// ifaceID 为帧携带的采集接口号(引擎分配, 0 保留未知).
func NewCANSource(iface string, ifaceID uint16) *CANSource {
	return &CANSource{iface: iface, ifaceID: ifaceID, fd: -1}
}

// Name 源标识.
func (s *CANSource) Name() string { return "can:" + s.iface }

// Start 实现采集源: ifindex 解析 → AF_CAN RAW socket → bind →(尽力)开 FD 帧
// 接收 → 读循环. 任一准备步骤失败返回 *capture.ErrSourceUnavailable; FD 开关失败仅降级为纯经典帧
// (老内核无此选项时内核会丢弃总线上的 FD 帧), 不致命.
func (s *CANSource) Start(ctx context.Context, push func(capture.Frame)) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("can: Start 重复调用")
	}
	if s.stopped.Load() {
		s.mu.Unlock()
		return errors.New("can: 源已停止")
	}
	ifc, err := net.InterfaceByName(s.iface)
	if err != nil {
		s.mu.Unlock()
		return &capture.ErrSourceUnavailable{Kind: "CAN", Reason: fmt.Sprintf("接口 %s 不存在: %v", s.iface, err)}
	}
	fd, err := unix.Socket(unix.AF_CAN, unix.SOCK_RAW, unix.CAN_RAW)
	if err != nil {
		s.mu.Unlock()
		return &capture.ErrSourceUnavailable{Kind: "CAN", Reason: fmt.Sprintf("创建 AF_CAN RAW socket 失败(内核需编 CONFIG_CAN+CONFIG_CAN_RAW, 进程需 CAP_NET_RAW): %v", err)}
	}
	if err := unix.Bind(fd, &unix.SockaddrCAN{Ifindex: ifc.Index}); err != nil {
		_ = unix.Close(fd)
		s.mu.Unlock()
		return &capture.ErrSourceUnavailable{Kind: "CAN", Reason: fmt.Sprintf("bind %s 失败: %v", s.iface, err)}
	}
	// 读循环用短超时轮询(200ms)驱动退出检查, 免去阻塞 read 感知不到 ctx/Stop 的问题,
	// 也免去并发 close fd 的复用竞窗.
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &unix.Timeval{Usec: 200000})
	_ = unix.SetsockoptInt(fd, unix.SOL_CAN_RAW, unix.CAN_RAW_FD_FRAMES, 1)

	s.fd = fd
	s.started = true
	s.wg.Go(func() { s.readLoop(ctx, fd, push) })
	s.mu.Unlock()
	return nil
}

// frameOf 单帧构造(独立于句柄, 可直接单测): 原始整帧字节 + 字段表 → capture.Frame.
func (s *CANSource) frameOf(fields map[string]string, data []byte) capture.Frame {
	fields["iface"] = s.iface
	return capture.Frame{
		IfaceID:  s.ifaceID,
		LinkType: capture.LinkTypeSocketCAN,
		TS:       time.Now(),
		Data:     data,
		Fields:   fields,
	}
}

// readLoop 逐帧 read: 返回长度即帧型(16=经典, 72=FD), 解析出字段表后推原始整帧字节.
// 时间戳取用户态到达时刻(SocketCAN 无内核抓包时间戳可取).
func (s *CANSource) readLoop(ctx context.Context, fd int, push func(capture.Frame)) {
	buf := make([]byte, fdFrameLen)
	for {
		if s.stopped.Load() || ctx.Err() != nil {
			return
		}
		n, err := unix.Read(fd, buf)
		if err != nil {
			// 超时/信号唤醒: 回头查退出条件; 真实读错误(接口 down 等)退出循环.
			if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
				continue
			}
			return
		}
		fields, perr := ParseCanFrame(buf[:n])
		if perr != nil {
			continue // 布局外长度理论不可达(内核保证), 防御性跳过
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		push(s.frameOf(fields, data))
	}
}

// Stop 幂等停止: 置位后读循环在 ≤200ms 超时唤醒内退出, 再关 socket(不与 read 并发 close).
func (s *CANSource) Stop() error {
	s.stopped.Store(true)
	s.wg.Wait()
	s.mu.Lock()
	fd := s.fd
	s.fd = -1
	s.mu.Unlock()
	if fd >= 0 {
		_ = unix.Close(fd)
	}
	return nil
}
