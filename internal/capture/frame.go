// Package capture 采集侧共享契约(瘦包): 帧模型 Frame 与链路类型 LinkType.
// 采集源(can)与落盘(pcapng)两包共用. 采集源是纯生产者: 只读探测、单协程推帧、
// 幂等 Stop; 落盘侧按帧携带的 LinkType 声明 pcapng 接口块.
//
// Frame.Fields 的 canonical 键约定(采集源产出, 消费方按需读取; 未覆盖的键不出现):
//
//	proto  传输/链路层协议小写: "can"/...
//	iface  采集接口名(如 "can0") — 备注: Frame.IfaceID 已是数值 id, 此键供展示
//
// 其余键由各采集源按需追加(如 "can.id"), 命名全小写点分层级.
package capture

import "time"

// LinkType 链路层类型(libpcap LINKTYPE 值域): 帧携带该类型, 落盘侧据此
// 声明 pcapng 接口块, Wireshark 按 DLT 选 dissector.
type LinkType uint16

// Frame 单个采集到的原始帧: 含链路层头的字节 + 采集源产出的字段表.
type Frame struct {
	IfaceID   uint16            // 采集接口 id(引擎内分配, 0 保留给未知接口)
	LinkType  LinkType          // 该帧所在链路的链路层类型
	Direction Direction         // 收/发方向
	TS        time.Time         // 抓包时刻
	Data      []byte            // 含链路层头的原始帧字节
	Fields    map[string]string // 字段表(可 nil), canonical 键见包注释
}

// Direction 帧方向: 采集口观测到的收/发.
type Direction uint8

const (
	// Unknown 方向未知(源未标注).
	Unknown Direction = iota
	// TX 本机发出.
	TX
	// RX 本机接收.
	RX
)
