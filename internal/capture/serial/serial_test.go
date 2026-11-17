package serial

import (
	"errors"
	"os"
	"testing"

	"github.com/iotxfoundry/webshark/internal/capture"
)

// TestSerialStartUnavailableWithoutPrivilege 非特权环境：Start 必须返回带原因的
// *capture.ErrSourceUnavailable，不 panic 不拖垮调用方（root 环境可真装载，跳过本用例）。
func TestSerialStartUnavailableWithoutPrivilege(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 环境可真加载，本用例只验非特权降级路径")
	}
	s := NewSerialSource([]string{"ttyUSB0"}, 9600, 0, nil)
	err := s.Start(t.Context(), func(capture.Frame) {})
	if err == nil {
		s.Stop()
		t.Fatal("非特权环境 Start 应失败")
	}
	var ue *capture.ErrSourceUnavailable
	if !errors.As(err, &ue) {
		t.Fatalf("应返回 *capture.ErrSourceUnavailable，得到 %T: %v", err, err)
	}
	if ue.Reason == "" {
		t.Fatal("原因不能为空")
	}
	// 文案逐字节锁定(与移植源 ErrSerialUnavailable 原文案一致, 中文显示名不接空格)
	if want := "串口采集源不可用: " + ue.Reason; ue.Error() != want {
		t.Fatalf("文案漂移: 得到 %q, 期望 %q", ue.Error(), want)
	}
}

// TestFramerFieldsMapping Frame → capture.Frame 字段映射（含 crcfail 与方向），纯用户态。
func TestFramerFieldsMapping(t *testing.T) {
	var got capture.Frame
	f := NewFramer(FramerConfig{Baud: 9600, Dir: capture.TX, OnFrame: func(fr Frame) {
		got = capture.Frame{
			LinkType:  capture.LinkTypeUser0,
			Direction: fr.Dir,
			TS:        fr.TS,
			Data:      fr.Data,
			Fields:    fieldsOf("ttyUSB0", fr),
		}
	}})
	f.Feed(fc16req, base)
	if got.LinkType != 147 {
		t.Fatalf("LinkType 应为 147（USER0），得到 %d", got.LinkType)
	}
	if got.Fields["proto"] != "modbus-rtu" {
		t.Fatalf("proto 应为 modbus-rtu，得到 %q", got.Fields["proto"])
	}
	if got.Fields["serial.tty"] != "ttyUSB0" || got.Fields["rtu.slave"] != "1" ||
		got.Fields["rtu.func"] != "16" || got.Fields["rtu.dir"] != "tx" {
		t.Fatalf("字段表不对: %v", got.Fields)
	}
	if got.Fields["iface"] != "ttyUSB0" {
		t.Fatalf("iface 应透传 tty 名，得到 %q", got.Fields["iface"])
	}
	if _, ok := got.Fields["rtu.crcfail"]; ok {
		t.Fatal("CRC 正常帧不应有 rtu.crcfail")
	}
}

// TestSerialSourceFrameIfaceID 串口源帧携带分配的采集接口号（feed→Framer OnFrame 构造点，
// 直调 feed 不走真 eBPF；feed 内部自持锁，测试侧不得外包 s.mu）。
func TestSerialSourceFrameIfaceID(t *testing.T) {
	s := NewSerialSource(nil, 9600, 7, nil)
	s.mu.Lock()
	s.fr = map[string]*framerPair{}
	s.mu.Unlock()
	var got capture.Frame
	push := func(f capture.Frame) { got = f }
	s.feed("ttyUSB0", capture.TX, fc16req, base, push)
	if got.IfaceID != 7 {
		t.Fatalf("IfaceID 应为 7（源分配号），得到 %d", got.IfaceID)
	}
	if got.LinkType != 147 {
		t.Fatalf("LinkType 应为 147（USER0），得到 %d", got.LinkType)
	}
}
