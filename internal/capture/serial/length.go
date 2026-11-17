package serial

import "github.com/iotxfoundry/webshark/internal/capture"

// crc16Modbus Modbus RTU CRC16：初始 0xFFFF，多项式 0xA001（反射形式），低字节在前（小端在线）。
func crc16Modbus(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// frameLen 按功能码长度表推断帧总长（含 2 字节 CRC）；前缀不足以判定时 ok=false。
// dir 决定请求/响应两套表；N=计数字段值（请求侧单位见条目注释）。
//
// 长度表（addr + fc + ... + CRC2；变长项标注计数字段下标）：
//
//	TX 请求：FC01/02/03/04 = 8；FC05/06 = 8；FC0F（写多线圈）= 9+N，N=[6]；
//	         FC10（写多寄存器）= 9+N，N=[6]；FC11/12 = 4；FC17（读写多寄存器）= 13+N，N=[10]。
//	RX 响应：FC01/02/03/04 = 5+N，N=[2]（字节计数）；FC05/06/0F/10 = 8（回显）；FC07 = 5；
//	         FC0B = 8；FC11（报从机标识）变长不锁；FC17 = 5+N，N=[2]；FC08 = 8。
//	         FC0C（取通讯事件记录）响应事件字节变长（0-64）不入表，交静默兜底。
//	两方向异常帧（FC|0x80）= 5。
//
// 罕见功能码与表外形态交 3.5 字符静默切帧兜底，不误切。
func frameLen(dir Direction, buf []byte) (int, bool) {
	if len(buf) < 2 {
		return 0, false
	}
	fc := buf[1]
	if fc&0x80 != 0 { // 异常帧：addr + fc|0x80 + 异常码 + CRC2
		return 5, true
	}
	if dir == capture.TX {
		switch fc {
		case 0x01, 0x02, 0x03, 0x04, 0x05, 0x06:
			return 8, true
		case 0x0F, 0x10: // N=数据字节数在 [6]
			if len(buf) < 7 {
				return 0, false
			}
			return 9 + int(buf[6]), true
		case 0x11, 0x12: // 报从机标识/读通信事件记录：请求定长 4
			return 4, true
		case 0x17: // 读写多寄存器：固定头 11 + bc[10] + CRC2 = 13+N
			if len(buf) < 11 {
				return 0, false
			}
			return 13 + int(buf[10]), true
		}
		return 0, false
	}
	switch fc {
	case 0x01, 0x02, 0x03, 0x04: // 响应：bc 在 [2]
		if len(buf) < 3 {
			return 0, false
		}
		return 5 + int(buf[2]), true
	case 0x05, 0x06, 0x0F, 0x10, 0x08: // 回显请求/诊断：定长 8
		return 8, true
	case 0x07: // 读异常状态：响应 1 字节状态
		return 5, true
	case 0x0B: // 取通讯事件计数：addr+fc+状态2B+事件计数2B = 6+CRC2 = 8
		return 8, true
	// FC0C（取通讯事件记录）响应含 16 位事件字节计数（0-64）的变长载荷，前缀锁不住
	// 帧尾（锁短会把长帧切碎），不入表交 3.5 字符静默兜底。
	case 0x17: // 读写多寄存器响应：读寄存器字节计数在 [2]
		if len(buf) < 3 {
			return 0, false
		}
		return 5 + int(buf[2]), true
	}
	return 0, false
}
