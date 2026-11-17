//go:build linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/iotxfoundry/webshark/internal/capture"
	"github.com/iotxfoundry/webshark/internal/capture/pcapng"
)

// stubSource 测试桩源: startErr 非空时 Start 即失败; 否则运行 run(缺省阻塞至
// ctx 取消), 记录 stopCalled 供取消路径断言.
type stubSource struct {
	name       string
	startErr   error
	run        func(ctx context.Context, push func(capture.Frame))
	stopCalled bool
}

func (s *stubSource) Name() string { return s.name }

func (s *stubSource) Start(ctx context.Context, push func(capture.Frame)) error {
	if s.startErr != nil {
		return s.startErr
	}
	if s.run != nil {
		s.run(ctx, push)
		return nil
	}
	<-ctx.Done()
	return nil
}

func (s *stubSource) Stop() error {
	s.stopCalled = true
	return nil
}

func TestParseSerial(t *testing.T) {
	ports, err := parseSerial("/dev/ttyUSB0@115200,/dev/ttyUSB1@9600")
	require.NoError(t, err)
	assert.Equal(t, []serialPort{{tty: "ttyUSB0", baud: 115200}, {tty: "ttyUSB1", baud: 9600}}, ports)

	// 无 @波特率 → 缺省 115200; 空段忽略; 无 /dev/ 前缀原样作 tty 名.
	ports, err = parseSerial(" ,/dev/ttyS1@9600,ttyS2,")
	require.NoError(t, err)
	assert.Equal(t, []serialPort{{tty: "ttyS1", baud: 9600}, {tty: "ttyS2", baud: defaultBaud}}, ports)

	_, err = parseSerial("/dev/ttyS0@abc")
	assert.Error(t, err)
	_, err = parseSerial("/dev/ttyS0@0")
	assert.Error(t, err)

	assert.Empty(t, splitNames("  , ,"))
	assert.Equal(t, []string{"can0", "can1"}, splitNames("  ,can0,, can1 ,"))
}

// TestRunSourcesAllUnavailable 全部源启动探测失败(环境不可用类) → 记 WARN 跳过,
// 无致命错误且 started=0, main 侧据此以退出码 1 结束.
func TestRunSourcesAllUnavailable(t *testing.T) {
	srcs := []source{
		&stubSource{name: "ttyUSB0", startErr: &capture.ErrSourceUnavailable{Kind: "串口", Reason: "装载失败"}},
		&stubSource{name: "can0", startErr: &capture.ErrSourceUnavailable{Kind: "CAN", Reason: "接口不存在"}},
	}
	started, err := runSources(context.Background(), testLogger(), func(capture.Frame) {}, srcs)
	assert.NoError(t, err)
	assert.Equal(t, 0, started)
}

// TestRunSourcesFatalCancelsOthers 首个非不可用启动错误 → 取消其余源(Stop 被调)
// 并作为致命错误返回.
func TestRunSourcesFatalCancelsOthers(t *testing.T) {
	good := &stubSource{name: "can0"}
	bad := &stubSource{name: "ttyUSB0", startErr: errors.New("权限拒绝")}
	_, err := runSources(context.Background(), testLogger(), func(capture.Frame) {}, []source{good, bad})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ttyUSB0")
	assert.True(t, good.stopCalled, "致命错误后其余源应被取消停止")
}

func TestFrameSinkWrite(t *testing.T) {
	w, err := pcapng.NewRolling(t.TempDir(), 1<<20, 2)
	require.NoError(t, err)
	sid, err := w.AddInterface(capture.LinkTypeSocketCAN, "can0")
	require.NoError(t, err)
	uid, err := w.AddInterface(capture.LinkTypeUser0, "ttyUSB0")
	require.NoError(t, err)

	sink := &frameSink{w: w, cancel: func() {}, log: testLogger()}
	now := time.Now()
	// CAN 帧: 内核序 can_id, push 内换大端落盘.
	sink.push(capture.Frame{IfaceID: sid, LinkType: capture.LinkTypeSocketCAN, TS: now,
		Data: []byte{0x01, 0x02, 0x03, 0x04, 0x03, 0xAA, 0xBB}})
	// 串口帧: 原样落盘.
	sink.push(capture.Frame{IfaceID: uid, LinkType: capture.LinkTypeUser0, TS: now, Data: []byte{0x11, 0x03, 0x02}})
	assert.NoError(t, sink.firstErr())
	assert.NoError(t, w.Close())

	shards := w.Files()
	require.Len(t, shards, 1)
	b, err := os.ReadFile(shards[0])
	require.NoError(t, err)
	// 落盘字节序: 内核序 can_id 字节 01 02 03 04(=值 0x04030201)换大端后为
	// 04 03 02 01, 内核序帧字节不应出现.
	assert.Greater(t, bytes.Index(b, []byte{0x04, 0x03, 0x02, 0x01, 0x03, 0xAA, 0xBB}), -1,
		"CAN 帧 can_id 应按大端落盘")
	assert.Equal(t, -1, bytes.Index(b, []byte{0x01, 0x02, 0x03, 0x04, 0x03, 0xAA, 0xBB}), "不应残留内核序 can_id 帧")
	assert.Greater(t, bytes.Index(b, []byte{0x11, 0x03, 0x02}), -1, "串口帧应原样落盘")
}

// testLogger 安静日志器(测试输出只留断言).
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
