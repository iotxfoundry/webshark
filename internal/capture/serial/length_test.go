// length_test.go RX 长度表回归：FC0B 定长 8；FC0C 变长不入表（静默兜底不切坏长帧）。
package serial

import (
	"testing"
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFrameLenFC0B FC0B（取通讯事件计数）响应 = addr+fc+状态2B+事件计数2B+CRC2
// 共 8 字节，按表锁长 8 收满即切且 CRC 通过（此前误锁 9 会把 8 字节真帧留成半帧）。
func TestFrameLenFC0B(t *testing.T) {
	body := []byte{0x01, 0x0B, 0x00, 0x00, 0xD4, 0x07}
	crc := crc16Modbus(body)
	fc0b := append(body, byte(crc), byte(crc>>8))
	n, ok := frameLen(capture.RX, fc0b)
	require.True(t, ok)
	assert.Equal(t, 8, n)

	var got []Frame
	f := NewFramer(FramerConfig{Baud: 115200, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(fc0b, base)
	require.Len(t, got, 1, "锁长 8 收满应立即切帧")
	assert.Equal(t, fc0b, got[0].Data)
	assert.True(t, got[0].CRCOK)
}

// TestFramerFC0CLongFrameSilentFallback FC0C（取通讯事件记录）响应含变长事件字节
// （0-64），不入表：长帧（28 字节）+后续帧经静默兜底全部完整产出（旧表锁 7 会把长帧切碎）。
func TestFramerFC0CLongFrameSilentFallback(t *testing.T) {
	// addr+fc+bc（0x18）+24 事件字节+CRC2 = 28 字节。
	body := append([]byte{0x01, 0x0C, 0x18}, make([]byte, 24)...)
	for i := range body[3:] {
		body[3+i] = byte(i + 1)
	}
	crc := crc16Modbus(body)
	fc0c := append(append([]byte{}, body...), byte(crc), byte(crc>>8))

	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(fc0c, base)                          // 前缀不足以锁长：交静默兜底
	f.FlushIdle(base.Add(5 * time.Millisecond)) // >t35 切出完整长帧
	f.Feed(fc03resp, base.Add(6*time.Millisecond))
	require.Len(t, got, 2, "FC0C 长帧与后续帧都应完整产出")
	assert.Equal(t, fc0c, got[0].Data)
	assert.True(t, got[0].CRCOK)
	assert.Equal(t, fc03resp, got[1].Data)
	assert.True(t, got[1].CRCOK)
}
