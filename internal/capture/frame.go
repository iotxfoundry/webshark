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

// LinkType 链路层类型(libpcap LINKTYPE 值域): 帧携带该类型, 落盘侧据此声明 pcapng 接口块, Wireshark 按 DLT 选 dissector.
type LinkType uint16

// 落盘链路类型常量(libpcap LINKTYPE 值域): 接口由 daemon 按帧携带的链路类型
// 懒声明, 无需配置.
const (
	// LinkTypeUser0..3 LINKTYPE_USER0..3(147..150): 串口多协议映射
	// (modbus→USER0[sharkd user_dlts→mbrtu 深解], ft12→USER1[→iec60870_101/103],
	//  645/698/自定义→USER2/3[无内建 dissector, webshark Go 层生成协议树])
	LinkTypeUser0 = LinkType(147)
	LinkTypeUser1 = LinkType(148)
	LinkTypeUser2 = LinkType(149)
	LinkTypeUser3 = LinkType(150)
	// LinkTypeSocketCAN LINKTYPE_CAN_SOCKETCAN(227): SocketCAN 帧, Wireshark
	// 内建 dissector 深解析.
	LinkTypeSocketCAN = LinkType(227)
)

// Frame 单个采集到的原始帧: 含链路层头的字节 + 采集源产出的字段表.
type Frame struct {
	// IfaceID 采集接口 id(引擎内分配, 0 保留给未知接口).
	IfaceID   uint16
	LinkType  LinkType          // 该帧所在链路的链路层类型
	Direction Direction         // 收/发方向
	TS        time.Time         // 抓包时刻
	Data      []byte            // 含链路层头的原始帧字节
	Fields    map[string]string // 字段表(可 nil), canonical 键见包注释
}

// Direction 帧方向: 采集口观测到的收/发.
type Direction uint8

// 方向常量.
const (
	// Unknown 方向未知(源未标注).
	Unknown Direction = iota
	// TX 本机发出.
	TX
	// RX 本机接收.
	RX
)

// ErrSourceUnavailable 采集源能力探测失败(接口不存在/内核缺配置/权限不足等), 调用方应降级跳过该源而非中断整体采集.
type ErrSourceUnavailable struct {
	// Kind 源显示名("CAN"/"串口").
	Kind string
	// Reason 不可用原因.
	Reason string
}

// Error 与历史两类错误文案逐字节一致: 拉丁显示名后接空格(CAN 采集源不可用),
// 中文显示名直接相连(串口采集源不可用).
func (e *ErrSourceUnavailable) Error() string {
	sep := ""
	if e.Kind == "CAN" {
		sep = " "
	}
	return e.Kind + sep + "采集源不可用: " + e.Reason
}
