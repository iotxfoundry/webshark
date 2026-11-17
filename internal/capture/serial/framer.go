package serial

import (
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// Direction 字节流方向，本源侧视角：TX=主机发（线上为主站请求），RX=主机收。
type Direction = capture.Direction

// Frame 单 Modbus RTU 帧（完整 ADU：地址+PDU+CRC）。
type Frame struct {
	Dir   Direction // 字节流方向
	TS    time.Time // 帧末字节到达时刻
	Data  []byte    // ADU 原始字节
	CRCOK bool      // CRC16 校验通过；false 时帧仍产出（留现场证据），上层打 rtu.crcfail=1
	Slave uint8     // 站地址（ADU 首字节）
	Func  uint8     // 功能码（异常帧为 0x80|原功能码）
}

// FramerConfig 定界器配置。
type FramerConfig struct {
	// Baud 线上波特率，决定 3.5 字符静默时长。
	Baud int
	// Dir 本 Framer 所在字节流方向。
	Dir Direction
	// Listen 监听混流模式：单口同时载主站请求与从站响应，TX/RX 两套方向长度表均
	// 不适用——跳过长度表锁定，仅按 3.5 字符静默切帧（旁路监听适配器采集用）。
	Listen bool
	// OnFrame 切出一帧时回调（定界与 CRC 判定完成后），半帧不回调。
	OnFrame func(Frame)
}

// rtuBuf 静默定界累积缓冲。
type rtuBuf struct {
	buf []byte
	// last 末字节到达时刻，其后静默超过 char35 即切帧。
	last time.Time
}

// Framer Modbus RTU 定界器：3.5 字符静默切帧 + CRC16（0xA001 反射）+ 功能码长度表双重校验。
// 单实例只服务一条字节流，Feed/FlushIdle 非并发安全。
type Framer struct {
	cfg    FramerConfig
	char35 time.Duration // 3.5 字符静默时长
	cur    rtuBuf        // 当前累积字节
	// candLen>0：已按功能码长度表锁定帧长的候选帧总字节数（含 CRC），静默超时可提前切；0=未锁定。
	candLen int
}

// NewFramer 新建 RTU 定界器。
// 静默时长公式（RTU 规范 11 位/字符）：≤19200bps 时 t35 = 3.5*11*1e6/baud 微秒；>19200bps 固定 1.75ms。
func NewFramer(cfg FramerConfig) *Framer {
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

// feedByte 单字节到达。
func (f *Framer) feedByte(b byte, now time.Time) {
	// 已锁帧长但未收满：字节仍属当前帧，无论间隔多久不切（半帧受长度表保护）。
	// 未锁帧长：距上一字节超过 3.5 字符静默即切出上一帧，本字节起新帧。
	if f.candLen == 0 && !f.cur.last.IsZero() && now.Sub(f.cur.last) > f.char35 {
		f.cut()
		f.cur = rtuBuf{last: now}
	}
	f.cur.buf = append(f.cur.buf, b)
	f.cur.last = now
	// 长度表命中即锁定帧尾，后续字节属于下一帧（Listen 模式不锁，见 FramerConfig.Listen）。
	if f.candLen == 0 && !f.cfg.Listen {
		if n, ok := frameLen(f.cfg.Dir, f.cur.buf); ok {
			f.candLen = n
		}
	}
	// 帧长已锁定且收满：立即定界产出（无需再等静默）。
	if f.candLen > 0 && len(f.cur.buf) >= f.candLen {
		f.cut()
		// 锁定切帧后起新帧，last=now 重开静默窗口（本字节已入上一帧）。
		f.cur = rtuBuf{last: now}
	}
}

// Feed 字节块到达（同一 tty 字节流按序调用）。
func (f *Framer) Feed(chunk []byte, now time.Time) {
	for _, b := range chunk {
		f.feedByte(b, now)
	}
}

// FlushIdle 由调用方周期调用：静默超过 3.5 字符时间且长度表未锁定（锁定帧以收满为准）即切帧产出。
func (f *Framer) FlushIdle(now time.Time) {
	if f.candLen > 0 || f.cur.last.IsZero() || now.Sub(f.cur.last) <= f.char35 {
		return
	}
	f.cut()
	f.cur = rtuBuf{}
}

// cut 把当前累积定界为一帧并回调（空缓冲不切）；重置 cur 与 candLen。
func (f *Framer) cut() {
	buf := f.cur.buf
	ts := f.cur.last
	f.cur = rtuBuf{}
	f.candLen = 0
	if len(buf) == 0 || f.cfg.OnFrame == nil {
		return
	}
	fr := Frame{Dir: f.cfg.Dir, TS: ts, Data: buf, Slave: buf[0]}
	if len(buf) >= 2 {
		fr.Func = buf[1]
	}
	fr.CRCOK = len(buf) >= 4 && crc16Modbus(buf[:len(buf)-2]) == crcOf(buf)
	f.cfg.OnFrame(fr)
}

// crcOf 取缓冲末两字节（小端）；不足两字节返回必不相等的 0，使 CRCOK=false。
func crcOf(buf []byte) uint16 {
	if len(buf) < 4 {
		return 0
	}
	return uint16(buf[len(buf)-2]) | uint16(buf[len(buf)-1])<<8
}
