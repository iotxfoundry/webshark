package serial

import (
	"testing"
	"time"

	"github.com/iotxfoundry/webshark/internal/capture"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试向量刻意取多协议混合字节（FT1.2 固定帧 / DL/T 645 / 噪声碎片）：
// 成帧器只看字节时序不认协议，帧内容对它是透明的。
var (
	fixedFrame = []byte{0x10, 0x40, 0x05, 0x6B, 0x16}                               // FT1.2 固定帧形态
	varFrame   = []byte{0x68, 0x0A, 0x0A, 0x68, 1, 2, 3, 4, 5, 6, 7, 8, 0x53, 0x16} // FT1.2 可变帧形态
	junkBytes  = []byte{0x7F, 0x3D, 0x00, 0xA5}                                     // 噪声碎片
)

// base 测试基准时刻。
var base = time.Unix(0, 0)

// feedGap 依时序喂入字节段（chunks[i] 到达时刻 = base + i*gap），返回切出的帧。
func feedGap(t *testing.T, dir Direction, baud int, chunks [][]byte, gap time.Duration) []Frame {
	t.Helper()
	var got []Frame
	f := NewFramer(FramerConfig{Baud: baud, Dir: dir, OnFrame: func(fr Frame) { got = append(got, fr) }})
	for i, c := range chunks {
		f.Feed(c, base.Add(time.Duration(i)*gap))
	}
	// 静默超时驱动尾帧落袋。
	f.FlushIdle(base.Add(time.Duration(len(chunks))*gap + 10*time.Millisecond))
	return got
}

func TestFramerGapSplitsFrames(t *testing.T) {
	// 帧间留静默（9600bps t35≈4.01ms，取 10ms）：静默切帧，帧序与内容完整保留。
	got := feedGap(t, capture.RX, 9600, [][]byte{fixedFrame, varFrame}, 10*time.Millisecond)
	require.Len(t, got, 2)
	assert.Equal(t, fixedFrame, got[0].Data)
	assert.Equal(t, varFrame, got[1].Data)
	assert.Equal(t, capture.RX, got[0].Dir)
	assert.Equal(t, capture.RX, got[1].Dir)
}

func TestFramerNoGapConcatenates(t *testing.T) {
	// 两帧字节无间隙连续到达：成帧器无协议知识不判帧界，粘连为单帧（语义变化明示：
	// 粘连帧由上层分发器按结构特征拒绝后整体落兜底，采集层不再预拆）。
	stream := append(append([]byte{}, fixedFrame...), varFrame...)
	got := feedGap(t, capture.RX, 115200, [][]byte{stream}, time.Millisecond)
	require.Len(t, got, 1)
	assert.Equal(t, stream, got[0].Data)
}

func TestFramerSplitAcrossFeedsRejoins(t *testing.T) {
	// 同一帧拆 3 段喂入，段间隔远小于 t35：字节续拼为单帧，不产半帧。
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.TX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	now := base
	for _, c := range [][]byte{varFrame[:5], varFrame[5:9], varFrame[9:]} {
		f.Feed(c, now)
		now = now.Add(time.Millisecond) // 段间 1ms < t35≈4.01ms，不切
		assert.Empty(t, got, "半帧不产出")
	}
	f.FlushIdle(now.Add(10 * time.Millisecond))
	require.Len(t, got, 1)
	assert.Equal(t, varFrame, got[0].Data)
}

func TestFramerFlushIdleDrivesTailFrame(t *testing.T) {
	// 尾帧无后续字节驱动：静默窗口内 FlushIdle 不切，超窗切出；再次 FlushIdle 不重复出帧。
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(fixedFrame, base)
	f.FlushIdle(base.Add(3 * time.Millisecond)) // < t35≈4.01ms
	assert.Empty(t, got, "静默窗口内不切")
	f.FlushIdle(base.Add(5 * time.Millisecond)) // > t35
	require.Len(t, got, 1)
	assert.Equal(t, fixedFrame, got[0].Data)
	assert.Equal(t, base, got[0].TS, "帧时刻=末字节到达时刻")
	f.FlushIdle(base.Add(20 * time.Millisecond))
	assert.Len(t, got, 1, "缓冲已空，重复 FlushIdle 不出帧")
}

func TestFramerIdleFlushWithoutBytes(t *testing.T) {
	// 空缓冲（无字节喂入）：FlushIdle 不出帧不回调。
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.FlushIdle(base.Add(time.Second))
	assert.Empty(t, got)
}

func TestFramerJunkFragmentKept(t *testing.T) {
	// 噪声碎片照常成帧落袋（现场证据不丢），内容不做任何协议判定。
	got := feedGap(t, capture.RX, 9600, [][]byte{junkBytes}, time.Millisecond)
	require.Len(t, got, 1)
	assert.Equal(t, junkBytes, got[0].Data)
}

func TestFramerBaudSilenceFormula(t *testing.T) {
	// 波特率只决定静默时长：9600（t35≈4.01ms）3ms 间隙不切、10ms 后由 FlushIdle 切；
	// >19200bps 固定 1.75ms，2ms 即切。
	got := feedGap(t, capture.TX, 9600, [][]byte{junkBytes, junkBytes}, 3*time.Millisecond)
	assert.Len(t, got, 1, "3ms < t35(9600)≈4.01ms：两段续拼单帧")

	got = feedGap(t, capture.TX, 115200, [][]byte{junkBytes, junkBytes}, 2*time.Millisecond)
	assert.Len(t, got, 2, "2ms > t35(115200)=1.75ms：静默切成两帧")
}

func TestFramerNextByteTriggersCut(t *testing.T) {
	// 尾帧未等 FlushIdle、下一帧首字节先到：间隙超窗即在新字节到达时切出上一帧。
	var got []Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.RX, OnFrame: func(fr Frame) { got = append(got, fr) }})
	f.Feed(fixedFrame, base)
	f.Feed(varFrame, base.Add(10*time.Millisecond)) // 间隙 10ms > t35
	require.Len(t, got, 1, "上一帧由新字节到达触发切出")
	assert.Equal(t, fixedFrame, got[0].Data)
	f.FlushIdle(base.Add(20 * time.Millisecond))
	require.Len(t, got, 2)
	assert.Equal(t, varFrame, got[1].Data)
}
