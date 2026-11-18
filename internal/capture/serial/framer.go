package serial

import (
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// Direction 字节流方向，本源侧视角：TX=主机发，RX=主机收。
type Direction = capture.Direction

// Frame 静默成帧切出的字节帧（协议无关：帧内语义由 sharkd 侧分发器解析）。
type Frame struct {
	// Dir 字节流方向。
	Dir  Direction
	TS   time.Time // 帧末字节到达时刻
	Data []byte    // 帧原始字节
}

// FramerConfig 成帧器配置。
type FramerConfig struct {
	// Baud 线上波特率，决定 3.5 字符静默时长。
	Baud int
	// Dir 本 Framer 所在字节流方向。
	Dir Direction
	// OnFrame 切出一帧时回调；静默间隙未到不产半帧。
	OnFrame func(Frame)
}

// Framer 通用静默成帧器：字节累积 + 超过 3.5 字符静默切帧，不含任何协议知识（帧定界只依赖字节时序，Modbus/FT1.2/DL/T 645 等协议语义全部交给上层解析）。单实例只服务一条字节流，Feed/FlushIdle 非并发安全。
type Framer struct {
	// cfg 成帧配置。
	cfg    FramerConfig
	char35 time.Duration // 3.5 字符静默时长
	buf    []byte        // 当前累积字节
	last   time.Time     // 末字节到达时刻，其后静默超过 char35 即切帧
}

// NewFramer 新建静默成帧器。静默时长按 11 位/字符：≤19200bps 时 t35 = 3.5*11*1e6/baud 微秒；>19200bps 固定 1.75ms。
func NewFramer(cfg FramerConfig) *Framer {
	// 波特率未给按保守常用值 9600 兜底。
	if cfg.Baud <= 0 {
		cfg.Baud = 9600
	}
	// t35 = 3.5 字符 × 11 位/字符 ÷ baud；>19200bps 按规范取固定 1.75ms。
	var t35 time.Duration
	if cfg.Baud > 19200 {
		t35 = 1750 * time.Microsecond
	} else {
		t35 = time.Duration(38.5 * float64(time.Second) / float64(cfg.Baud))
	}
	return &Framer{cfg: cfg, char35: t35}
}

// Feed 字节块到达（同一 tty 字节流按序调用）；块首字节距上一字节超过静默时长
// 即切出上一帧；块内字节共用同一到达时刻，不内切。
func (f *Framer) Feed(chunk []byte, now time.Time) {
	for _, b := range chunk {
		if !f.last.IsZero() && now.Sub(f.last) > f.char35 {
			f.cut()
		}
		f.buf = append(f.buf, b)
		f.last = now
	}
}

// FlushIdle 由调用方周期调用：静默超过 3.5 字符即切帧产出（驱动尾帧落袋）。
func (f *Framer) FlushIdle(now time.Time) {
	if f.last.IsZero() || now.Sub(f.last) <= f.char35 {
		return
	}
	f.cut()
}

// cut 把当前累积定界为一帧并回调；重置缓冲重开静默窗口。
func (f *Framer) cut() {
	buf, ts := f.buf, f.last
	f.buf, f.last = nil, time.Time{}
	if len(buf) == 0 || f.cfg.OnFrame == nil {
		return
	}
	f.cfg.OnFrame(Frame{Dir: f.cfg.Dir, TS: ts, Data: buf})
}
