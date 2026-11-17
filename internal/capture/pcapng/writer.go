// Package pcapng 提供 pcapng 格式的抓包落盘写入器: 单文件 Writer 与
// dumpcap 式大小滚动 RollingWriter. 块编码(SHB/IDB/EPB)按 pcapng 规范
// 直接手写(小端), 依赖面收敛到标准库 —— Wireshark/tshark 可直接打开.
// 接口块按活跃源声明(AddInterface 按声明顺序得接口号), 每个滚动分片重放
// 全部接口块, 自含元数据可独立打开.
package pcapng

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// 落盘链路类型常量(libpcap LINKTYPE 值域): 接口由 daemon 按帧携带的链路类型
// 懒声明, 无需配置.
const (
	// LinkTypeUser0 LINKTYPE_USER0(147): 公司自定义链路类型(串口等私有帧落盘用).
	LinkTypeUser0 = capture.LinkType(147)
	// LinkTypeSocketCAN LINKTYPE_CAN_SOCKETCAN(227): SocketCAN 帧, Wireshark
	// 内建 dissector 深解析.
	LinkTypeSocketCAN = capture.LinkType(227)
)

// pcapng 块类型(规范 Block Type 值).
const (
	blockSHB = 0x0A0D0D0A // Section Header Block
	blockIDB = 0x00000001 // Interface Description Block
	blockEPB = 0x00000006 // Enhanced Packet Block
)

// pcapng 选项码(if_name)与 opt_endofopt.
const (
	optEndOfOpt = 0
	optIfName   = 2
)

// ErrClosed 已关闭的写入器上继续操作的哨兵错误(可用 errors.Is 判定).
var ErrClosed = errors.New("pcapng: 写入器已关闭")

// pad4 n 向上补齐到 4 字节边界的填充数(pcapng 块内各字段 4 字节对齐).
func pad4(n int) int { return (4 - n&3) & 3 }

// shbBlock Section Header Block(32B): magic 1A2B3C4D 标识小端, 版本 1.0,
// 段长未指定(-1), opt_endofopt 收尾.
func shbBlock() []byte {
	b := make([]byte, 32)
	binary.LittleEndian.PutUint32(b[0:4], blockSHB)
	binary.LittleEndian.PutUint32(b[4:8], 32)
	binary.LittleEndian.PutUint32(b[8:12], 0x1A2B3C4D)
	binary.LittleEndian.PutUint16(b[12:14], 1) // major
	// b[14:16] minor=0, b[16:24] 段长全 1(未指定), b[24:28] opt_endofopt(码 0 长 0)
	binary.LittleEndian.PutUint32(b[28:32], 32)
	return b
}

// idbBlock Interface Description Block: linktype + snaplen(0x40000, 不截断常规帧)
// + if_name 选项(名称缺省省略) + opt_endofopt.
func idbBlock(linkType capture.LinkType, name string) []byte {
	body := make([]byte, 8, 16+len(name))
	binary.LittleEndian.PutUint16(body[0:2], uint16(linkType))
	binary.LittleEndian.PutUint32(body[4:8], 0x40000) // snaplen
	if name != "" {
		body = binary.LittleEndian.AppendUint16(body, optIfName)
		body = binary.LittleEndian.AppendUint16(body, uint16(len(name)))
		body = append(body, name...)
		body = append(body, make([]byte, pad4(len(name)))...)
	}
	body = binary.LittleEndian.AppendUint16(body, optEndOfOpt)
	body = binary.LittleEndian.AppendUint16(body, 0)
	total := uint32(8 + len(body) + 4)
	b := make([]byte, 8, int(total))
	binary.LittleEndian.PutUint32(b[0:4], blockIDB)
	binary.LittleEndian.PutUint32(b[4:8], total)
	b = append(b, body...)
	return binary.LittleEndian.AppendUint32(b, total)
}

// epbBlock Enhanced Packet Block: 接口号 + 微秒时间戳(规范缺省 if_tsresol=6)
// + caplen/origlen + 数据(4 字节对齐填充), 无选项区.
func epbBlock(ifaceID uint16, ts time.Time, data []byte) []byte {
	micros := uint64(ts.Unix())*1e6 + uint64(ts.Nanosecond())/1e3
	total := uint32(32 + len(data) + pad4(len(data)))
	b := make([]byte, 8, int(total))
	binary.LittleEndian.PutUint32(b[0:4], blockEPB)
	binary.LittleEndian.PutUint32(b[4:8], total)
	b = binary.LittleEndian.AppendUint32(b, uint32(ifaceID))
	b = binary.LittleEndian.AppendUint32(b, uint32(micros>>32))
	b = binary.LittleEndian.AppendUint32(b, uint32(micros))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	b = binary.LittleEndian.AppendUint32(b, uint32(len(data)))
	b = append(b, data...)
	b = append(b, make([]byte, pad4(len(data)))...)
	return binary.LittleEndian.AppendUint32(b, total)
}

// countWriter 包装文件统计已写字节, 供滚动判断当前分片体积.
type countWriter struct {
	f *os.File
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return c.f.Write(p)
}

// ifaceRec 已声明接口的描述, 供 RollingWriter 滚动后在新分片重放 IDB.
type ifaceRec struct {
	linkType capture.LinkType
	name     string
}

// Writer 单文件 pcapng 写入器: AddInterface 必须在首包前全部完成, 首包即封板
// (sealed), 之后不能再增接口. 每块整体单次 Write 落盘, 不缓冲.
type Writer struct {
	dir    string
	path   string // 指定分片文件名时用(RollingWriter); 空则 CreateTemp 随机名
	file   *os.File
	cw     *countWriter
	ifaces []ifaceRec
	sealed bool // 首包已落, 禁止再 AddInterface
	closed bool
}

// NewWriter 创建写入器, spool 目录由调用方给定; 目录在首个 AddInterface 时
// 创建(含父目录). 文件名自动生成(cap-<随机>.pcapng).
func NewWriter(dir string) *Writer {
	return &Writer{dir: dir}
}

// newWriterPath 创建写到固定文件名的写入器, 供滚动分片使用.
func newWriterPath(dir, name string) *Writer {
	return &Writer{dir: dir, path: filepath.Join(dir, name)}
}

// open 建目录建文件, 落 section header 与首个接口块.
func (w *Writer) open(linkType capture.LinkType, name string) error {
	if err := os.MkdirAll(w.dir, 0o755); err != nil {
		return fmt.Errorf("创建抓包目录: %w", err)
	}
	var f *os.File
	var err error
	if w.path != "" {
		f, err = os.OpenFile(w.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("创建分片 %s: %w", w.path, err)
		}
	} else {
		if f, err = os.CreateTemp(w.dir, "cap-*.pcapng"); err != nil {
			return fmt.Errorf("创建抓包文件: %w", err)
		}
	}
	w.file, w.cw = f, &countWriter{f: f}
	if _, err := w.cw.Write(shbBlock()); err != nil {
		_ = f.Close()
		return fmt.Errorf("写 section header: %w", err)
	}
	if _, err := w.cw.Write(idbBlock(linkType, name)); err != nil {
		_ = f.Close()
		return fmt.Errorf("写接口块: %w", err)
	}
	return nil
}

// AddInterface 声明采集接口, 返回帧引用的接口 id(按声明顺序从 0 递增).
// 必须在首包前全部 Add 完; 首包后调用返回错误.
func (w *Writer) AddInterface(linkType capture.LinkType, name string) (uint16, error) {
	if w.closed {
		return 0, fmt.Errorf("AddInterface: %w", ErrClosed)
	}
	if w.sealed {
		return 0, errors.New("pcapng: 首包已写入, 不能再 AddInterface")
	}
	if w.cw == nil {
		if err := w.open(linkType, name); err != nil {
			return 0, err
		}
	} else if _, err := w.cw.Write(idbBlock(linkType, name)); err != nil {
		return 0, fmt.Errorf("追加接口块: %w", err)
	}
	w.ifaces = append(w.ifaces, ifaceRec{linkType: linkType, name: name})
	return uint16(len(w.ifaces) - 1), nil
}

// Write 写一帧并立即落盘. ifaceID 须为 AddInterface 返回的 id.
func (w *Writer) Write(ifaceID uint16, ts time.Time, data []byte) error {
	if w.closed {
		return fmt.Errorf("Write: %w", ErrClosed)
	}
	if w.cw == nil {
		return errors.New("pcapng: 尚未声明接口, 不能写帧")
	}
	if int(ifaceID) >= len(w.ifaces) {
		return fmt.Errorf("pcapng: 未知接口 id %d(共声明 %d 个)", ifaceID, len(w.ifaces))
	}
	if _, err := w.cw.Write(epbBlock(ifaceID, ts, data)); err != nil {
		return fmt.Errorf("写帧: %w", err)
	}
	w.sealed = true
	return nil
}

// size 当前分片已写字节.
func (w *Writer) size() int64 {
	if w.cw == nil {
		return 0
	}
	return w.cw.n
}

// Close 幂等关闭: 关文件. 只声明接口即关闭时无帧也已落 SHB+IDB, 文件仍合法.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file == nil {
		return nil
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("关闭文件: %w", err)
	}
	return nil
}
