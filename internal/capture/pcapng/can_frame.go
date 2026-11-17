// can_frame.go SocketCAN 载荷字节序换转: CAN 源推帧为内核字节序, 落盘前换转.
package pcapng

import (
	"encoding/binary"
)

// CanFrameBE SocketCAN 载荷字节序换转: 内核 can_frame/canfd_frame 的 can_id/flags 字段
// 为主机序(LE), Wireshark 对 LINKTYPE_CAN_SOCKETCAN 默认按大端读 —— 前 4B 换大端,
// 其余(len/flags/data)原样. 采集源推帧为内核字节序, 落盘前换转.
func CanFrameBE(kernel []byte) []byte {
	if len(kernel) < 4 {
		return kernel
	}
	be := make([]byte, len(kernel))
	copy(be, kernel)
	binary.BigEndian.PutUint32(be, binary.LittleEndian.Uint32(kernel[:4]))
	return be
}
