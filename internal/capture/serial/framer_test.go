package serial

import (
	"testing"
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 黄金向量（手编 + CRC16 自算）：FC03 请求/响应、FC16 请求/响应、异常帧、FC06 请求。
var (
	fc03req = []byte{0x01, 0x03, 0x00, 0x00, 0x00, 0x0A, 0xC5, 0xCD}
	// FC03 响应：10 寄存器 = 20 数据字节，计数 0x14。
	fc03resp = []byte{
		0x01, 0x03, 0x14, 0x00, 0x03, 0x06, 0x09, 0x0C, 0x0F, 0x12, 0x15,
		0x18, 0x1B, 0x1E, 0x21, 0x24, 0x27, 0x2A, 0x2D, 0x30, 0x33, 0x36, 0x39, 0xC9, 0x13,
	}
	// FC16 请求：写 2 寄存器，字节计数 4。
	fc16req  = []byte{0x01, 0x10, 0x00, 0x01, 0x00, 0x02, 0x04, 0x00, 0x0A, 0x00, 0x0B, 0x53, 0xA6}
	fc16resp = []byte{0x01, 0x10, 0x00, 0x01, 0x00, 0x02, 0x10, 0x08}
	// 异常帧：FC03|0x80，异常码 02（非法数据地址）。
	excFrame = []byte{0x01, 0x83, 0x02, 0xC0, 0xF1}
	// FC06 请求：写单寄存器。
	fc06req = []byte{0x01, 0x06, 0x00, 0x01, 0x00, 0x05, 0x18, 0x09}
	// FC03 响应 CRC 低位错 1 bit（0xC9→0xC8）：CRCOK=false 但帧仍产出。
	fc03respBadCRC = []byte{
		0x01, 0x03, 0x14, 0x00, 0x03, 0x06, 0x09, 0x0C, 0x0F, 0x12, 0x15,
		0x18, 0x1B, 0x1E, 0x21, 0x24, 0x27, 0x2A, 0x2D, 0x30, 0x33, 0x36, 0x39, 0xC8, 0x13,
	}
)

// base 测试基准时刻。
var base = time.Unix(0, 0)

// feedSplit 把帧按段切分喂入（段间模拟字节连续，段尾留静默），返回切出的帧。
func feedSplit(t *testing.T, dir Direction, baud int, chunks [][]byte, gap time.Duration) []Frame {
	t.Helper()
	var got []Frame
	f := NewFramer(FramerConfig{Baud: baud, Dir: dir, OnFrame: func(fr Frame) { got = append(got, fr) }})
	now := base
	for _, c := range chunks {
		f.Feed(c, now)
		now = now.Add(gap)
	}
	// 等静默超时切尾帧。
	f.FlushIdle(now.Add(gap))
	return got
}

func TestFramerFC03Request(t *testing.T) {
	got := feedSplit(t, capture.TX, 9600, [][]byte{fc03req}, time.Millisecond)
	require.Len(t, got, 1)
	assert.Equal(t, capture.TX, got[0].Dir)
	assert.True(t, got[0].CRCOK)
	assert.EqualValues(t, 0x01, got[0].Slave)
	assert.EqualValues(t, 0x03, got[0].Func)
	assert.Equal(t, fc03req, got[0].Data)
}

func TestFramerFC03ResponseSplit3(t *testing.T) {
	// 同帧拆 3 段喂入：FC03 响应前 3 字节即锁长 5+0x14=25，段间无论隔多久都不得误切，
	// 收满 25 字节立即产出（半帧受长度表保护，静默切帧只作用于表外流）。
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	now := base
	for _, c := range [][]byte{fc03resp[:8], fc03resp[8:16], fc03resp[16:]} {
		f.Feed(c, now)
		now = now.Add(time.Second) // 段间隔远超 t35
		if len(got) < 1 {
			assert.Empty(t, got, "半帧不产出")
		}
	}
	require.Len(t, got, 1)
	assert.True(t, got[0].CRCOK)
	assert.Equal(t, fc03resp, got[0].Data)
}

func TestFramerFC16RequestSplit2(t *testing.T) {
	got := feedSplit(t, capture.TX, 115200, [][]byte{fc16req[:5], fc16req[5:]}, 100*time.Microsecond)
	require.Len(t, got, 1)
	assert.True(t, got[0].CRCOK)
	assert.EqualValues(t, 0x10, got[0].Func)
	assert.Equal(t, fc16req, got[0].Data)
}

func TestFramerExceptionFrame(t *testing.T) {
	got := feedSplit(t, capture.RX, 9600, [][]byte{excFrame}, time.Millisecond)
	require.Len(t, got, 1)
	assert.True(t, got[0].CRCOK)
	assert.EqualValues(t, 0x83, got[0].Func)
	assert.Equal(t, excFrame, got[0].Data)
}

func TestFramerFC06Request(t *testing.T) {
	got := feedSplit(t, capture.TX, 9600, [][]byte{fc06req}, time.Millisecond)
	require.Len(t, got, 1)
	assert.True(t, got[0].CRCOK)
	assert.Equal(t, fc06req, got[0].Data)
}

func TestFramerCRCFailStillEmitted(t *testing.T) {
	got := feedSplit(t, capture.RX, 9600, [][]byte{fc03respBadCRC}, time.Millisecond)
	require.Len(t, got, 1)
	assert.False(t, got[0].CRCOK, "CRC 错 1 bit 必须判 fail")
	assert.Equal(t, fc03respBadCRC, got[0].Data, "坏帧不丢")
}

func TestFramerTwoFramesNoGap(t *testing.T) {
	// 连续字节流含两帧（无静默间隔）：同方向（RX 响应）长度表逐帧锁长切帧。
	// FC03 响应（锁 5+0x14=25）+ 异常帧（锁 5）；同流请求/响应混排不成立
	// （一条 tty 流一个方向，两帧向量取同方向表内帧）。
	stream := append(append([]byte{}, fc03resp...), excFrame...)
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 115200, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(stream, base)
	require.Len(t, got, 2, "长度表应连续切 2 帧")
	assert.Equal(t, fc03resp, got[0].Data)
	assert.Equal(t, excFrame, got[1].Data)
}

func TestFramerHighBaudGap(t *testing.T) {
	// >19200bps 固定 1.75ms：FC03 请求按表锁长 8，收满立即产出与静默无关；
	// 表外功能码（FC08 请求侧不锁）才靠 1.75ms 静默切。
	raw := []byte{0x01, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 115200, Dir: capture.TX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(raw[:4], base)
	assert.Empty(t, got)
	f.Feed(raw[4:], base.Add(100*time.Microsecond)) // < 1.75ms 不切
	assert.Empty(t, got, "t35=1.75ms 内不切")
	f.FlushIdle(base.Add(2 * time.Millisecond))
	require.Len(t, got, 1)
	assert.Equal(t, raw, got[0].Data)
}

func TestFramerLowBaudGapFormula(t *testing.T) {
	// 9600bps：t35 = 3.5*11/9600 ≈ 4.01ms；3ms 间隔不切，8ms 后 FlushIdle 切。
	// 用表外功能码（FC08 TX 不锁长），排除「锁长收满即切」的干扰，纯验静默公式。
	raw := []byte{0x01, 0x08, 0x00, 0x00, 0xA5, 0x37, 0x00, 0x00}
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.TX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(raw[:4], base)
	f.Feed(raw[4:], base.Add(3*time.Millisecond))
	assert.Empty(t, got, "t35≈4.01ms 内不切")
	f.FlushIdle(base.Add(8 * time.Millisecond))
	require.Len(t, got, 1)
	assert.Equal(t, raw, got[0].Data)
}

func TestFramerSilentCutWithoutLengthTable(t *testing.T) {
	// 表外功能码（如 FC08 诊断 TX 侧不锁长）：纯静默切帧。
	raw := []byte{0x01, 0x08, 0x00, 0x00, 0xA5, 0x37}
	raw = append(raw, 0x00, 0x00) // 随手补齐，CRC 不验真伪，只验切帧行为
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.TX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(raw, base)
	assert.Empty(t, got)
	f.FlushIdle(base.Add(5 * time.Millisecond))
	require.Len(t, got, 1)
	assert.Equal(t, raw, got[0].Data)
}

func TestFramerGarbageShortFragment(t *testing.T) {
	// 噪声碎片（2 字节，表外）：静默切帧，不丢不崩。
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed([]byte{0x7F, 0x3D}, base)
	f.FlushIdle(base.Add(5 * time.Millisecond))
	require.Len(t, got, 1)
	assert.False(t, got[0].CRCOK)
	assert.EqualValues(t, 0x7F, got[0].Slave)
}

func TestCRC16ModbusGolden(t *testing.T) {
	// 黄金值（官方检查表 + 边界）："01030000000A"→0xCDC5；"8000"→0x7060；空→0xFFFF（初值）。
	assert.EqualValues(t, 0xCDC5, crc16Modbus(fc03req[:6]))
	assert.EqualValues(t, 0x7060, crc16Modbus([]byte{0x80, 0x00}))
	assert.EqualValues(t, 0xFFFF, crc16Modbus(nil))
}

// TestFramerListen 监听混流模式：跳过方向长度表，仅按 3.5 字符静默切帧。
// 请求+响应混合流在 RX 表下会被误切（请求 buf[2] 是起始地址高位，被当字节计数锁短），
// Listen 模式须完整保帧。
func TestFramerListen(t *testing.T) {
	req := append([]byte{0x01, 0x03, 0x00, 0x0A, 0x00, 0x02}, []byte{}...)
	crc := crc16Modbus(req)
	req = append(req, byte(crc), byte(crc>>8))
	resp := fc03resp

	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.RX, Listen: true,
		OnFrame: func(fr Frame) { got = append(got, fr) }})
	// t35（9600）≈ 4.01ms：两帧间留 10ms 静默，由下一帧首字节触发静默切帧；
	// 尾帧由 FlushIdle 切出。
	f.Feed(req, base)
	f.Feed(resp, base.Add(10*time.Millisecond))
	f.FlushIdle(base.Add(20 * time.Millisecond))

	require.Len(t, got, 2)
	assert.Equal(t, req, got[0].Data)
	assert.Equal(t, resp, got[1].Data)
	assert.True(t, got[0].CRCOK)
	assert.True(t, got[1].CRCOK)
}
