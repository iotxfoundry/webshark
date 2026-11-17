//go:build linux

package can

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// classicFrame 手编 struct can_frame(16B): can_id u32(LE) + len + pad[2] + data[8].
func classicFrame(rawID uint32, dlc int, data ...byte) []byte {
	b := make([]byte, 16)
	binary.LittleEndian.PutUint32(b[0:4], rawID)
	b[4] = byte(dlc)
	copy(b[8:], data)
	return b
}

// fdFrame 手编 struct canfd_frame(72B): can_id u32(LE) + len + flags + res[2] + data[64].
func fdFrame(rawID uint32, length int, flags byte, data ...byte) []byte {
	b := make([]byte, 72)
	binary.LittleEndian.PutUint32(b[0:4], rawID)
	b[4] = byte(length)
	b[5] = flags
	copy(b[8:], data)
	return b
}

func TestParseClassicStandard(t *testing.T) {
	f, err := ParseCanFrame(classicFrame(0x123, 3, 1, 2, 3))
	require.NoError(t, err)
	assert.Equal(t, "can", f["proto"])
	assert.Equal(t, "0x123", f["can.id"])
	assert.Equal(t, "false", f["can.ext"])
	assert.Equal(t, "false", f["can.rtr"])
	assert.Equal(t, "false", f["can.fd"])
	assert.Equal(t, "3", f["can.dlc"])
	assert.Equal(t, "3", f["can.len"])
}

func TestParseClassicEFF(t *testing.T) {
	// 扩展帧: 29 位 id + 0x80000000 标志.
	f, err := ParseCanFrame(classicFrame(0x80000000|0x1ABCDEF, 8, make([]byte, 8)...))
	require.NoError(t, err)
	assert.Equal(t, "0x1ABCDEF", f["can.id"])
	assert.Equal(t, "true", f["can.ext"])
	assert.Equal(t, "8", f["can.len"])
}

func TestParseClassicRTR(t *testing.T) {
	// 远程帧: 线上无数据字节, len 出 0, DLC 保留请求长度.
	f, err := ParseCanFrame(classicFrame(0x40000000|0x123, 1))
	require.NoError(t, err)
	assert.Equal(t, "true", f["can.rtr"])
	assert.Equal(t, "1", f["can.dlc"])
	assert.Equal(t, "0", f["can.len"])
}

func TestParseFD(t *testing.T) {
	f, err := ParseCanFrame(fdFrame(0x456, 12, 0x01, make([]byte, 12)...))
	require.NoError(t, err)
	assert.Equal(t, "can", f["proto"])
	assert.Equal(t, "0x456", f["can.id"])
	assert.Equal(t, "true", f["can.fd"])
	assert.Equal(t, "9", f["can.dlc"]) // 12B 档 → DLC 码 9
	assert.Equal(t, "12", f["can.len"])
}

func TestParseFDMax(t *testing.T) {
	f, err := ParseCanFrame(fdFrame(0x1FFFFFFF|0x80000000, 64, 0x06, make([]byte, 64)...))
	require.NoError(t, err)
	assert.Equal(t, "0x1FFFFFFF", f["can.id"])
	assert.Equal(t, "true", f["can.ext"])
	assert.Equal(t, "15", f["can.dlc"]) // 64B 档 → DLC 码 15
	assert.Equal(t, "64", f["can.len"])
}

func TestParseFDDlcLadder(t *testing.T) {
	// DLC 码阶梯: 非档位长度(如 10)落到下一档 12(DLC 9).
	f, err := ParseCanFrame(fdFrame(0x111, 10, 0, make([]byte, 10)...))
	require.NoError(t, err)
	assert.Equal(t, "9", f["can.dlc"])
	assert.Equal(t, "10", f["can.len"])
}

func TestParseInvalidLengths(t *testing.T) {
	// 经典帧 DLC>8 / FD 帧长度>64 / 截断帧 / 超长帧 → 全部报错.
	_, err := ParseCanFrame(classicFrame(0x123, 9))
	assert.ErrorContains(t, err, "DLC 非法")
	_, err = ParseCanFrame(fdFrame(0x123, 65, 0))
	assert.ErrorContains(t, err, "FD 帧长度非法")
	_, err = ParseCanFrame(classicFrame(0x123, 3)[:10]) // 截断
	assert.ErrorContains(t, err, "长度非法")
	_, err = ParseCanFrame(append(classicFrame(0x123, 1), 0xFF)) // 超长
	assert.ErrorContains(t, err, "长度非法")
	_, err = ParseCanFrame(nil)
	assert.Error(t, err)
}

// ParseCanFrameMust ParseCanFrame 的测试辅助: 解析失败即 panic.
func ParseCanFrameMust(b []byte) map[string]string {
	f, err := ParseCanFrame(b)
	if err != nil {
		panic(err)
	}
	return f
}

func TestStartUnavailableWithoutCANIface(t *testing.T) {
	// 本机无该 CAN 接口: Start 返回带原因的 ErrCANUnavailable, 不 panic.
	s := NewCANSource("nosuchcan0", 0)
	err := s.Start(context.Background(), func(capture.Frame) {})
	require.Error(t, err)
	var ue *ErrCANUnavailable
	require.True(t, errors.As(err, &ue), "应返回 *ErrCANUnavailable, 得到 %T: %v", err, err)
	assert.NotEmpty(t, ue.Reason)
	require.NoError(t, s.Stop()) // 失败路径 Stop 幂等清理
}

func TestStartStopIdempotent(t *testing.T) {
	// Start/Stop 幂等: 重复 Start 报错; 重复 Stop 无副作用. 本机若有真实 can 接口
	// 则真走 bind+读循环冒烟; 没有则 Start 失败, 只验 Stop 幂等.
	s := NewCANSource("nosuchcan0", 0)
	_ = s.Start(context.Background(), func(capture.Frame) {})
	require.NoError(t, s.Stop())
	require.NoError(t, s.Stop()) // 幂等
	err := s.Start(context.Background(), func(capture.Frame) {})
	// stopped 置位后 Start 拒绝, 无论首 Start 成败.
	require.Error(t, err)
}

// TestCANFrameIfaceID CAN 源帧携带分配的采集接口号(frameOf 为 readLoop 推帧的构造点).
func TestCANFrameIfaceID(t *testing.T) {
	s := NewCANSource("can0", 5)
	f := s.frameOf(ParseCanFrameMust(classicFrame(0x123, 3, 1, 2, 3)), classicFrame(0x123, 3, 1, 2, 3))
	assert.Equal(t, uint16(5), f.IfaceID)
	assert.EqualValues(t, 227, f.LinkType)
	assert.Equal(t, "can0", f.Fields["iface"])
}
