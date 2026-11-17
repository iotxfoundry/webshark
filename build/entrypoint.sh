#!/bin/sh
# webshark 容器入口：单容器拉起 sharkd + dumpcap（可选）+ webshark 三件套。
# 抓宿主机物理网卡需要 --network host；dumpcap 抓包需要运行时授予
# NET_ADMIN/NET_RAW 能力（docker run --cap-add）。
#
# 配置经环境变量注入（均有默认值）：
#   SPOOL            分片目录（建议挂持久卷）            /var/lib/webshark
#   LISTEN           webshark 监听地址                   0.0.0.0:8080
#                    非 loopback 必须配 WEBSHARK_TOKEN（服务端安全阀强制）
#   WEBSHARK_TOKEN   API 鉴权 token（webshark 自行读取环境变量）
#   DUMPCAP_IFACE    抓包网卡；none = 不启动抓包（纯分析已有分片）
#   DUMPCAP_FILTER   BPF 捕获过滤串（可选）
#   DUMPCAP_FILESIZE 单分片字节数                        8388608（8MiB）
#   DUMPCAP_FILES    分片数（webshark -ring 自动对齐此值）16
#   SHARKD_SOCK      sharkd Unix socket 路径             /run/webshark/sharkd.sock
#   SERIAL_PORTS     串口采集列表, 逗号分隔
#                    "/dev/ttyUSB0@115200,/dev/ttyUSB1@9600"（@波特率缺省 115200,
#                    仅用于 Modbus RTU 3.5 字符静默成帧, 不碰 termios）; 空 = 不采串口
#   CAN_IFACES       SocketCAN 接口名, 逗号分隔 "can0,can1"; 空 = 不采 CAN
#                    两者都空则 pktdump 整个不起
#
# 串口/CAN 采集(pktdump)部署前提:
#   - 串口: 设备经 --device 传入(如 --device /dev/ttyUSB0); eBPF 在内核 tty 层旁听
#     (不占设备, 线上业务进程照常独占), 需 --cap-add BPF --cap-add PERFMON
#     (内核 < 5.8 用 --cap-add SYS_ADMIN), 宿主内核 ≥ 5.8 且启用 BTF
#     (CONFIG_DEBUG_INFO_BTF, /sys/kernel/btf/vmlinux 存在)且未 lockdown BPF;
#   - CAN: 宿主先配好比特率并 up, 容器 --cap-add NET_RAW:
#     ip link set can0 type can bitrate 500000 && ip link set can0 up
#   - pktdump 每源启动探测一次: 接口不存在/eBPF 装载失败打 WARN 跳过该源,
#     全部源不可用退出 1 → 守护循环 2s 后重试(适配设备晚就绪)
set -eu

SPOOL="${SPOOL:-/var/lib/webshark}"
SHARKD_SOCK="${SHARKD_SOCK:-/run/webshark/sharkd.sock}"
LISTEN="${LISTEN:-0.0.0.0:8080}"
DUMPCAP_IFACE="${DUMPCAP_IFACE:-eth0}"
DUMPCAP_FILTER="${DUMPCAP_FILTER:-}"
DUMPCAP_FILESIZE="${DUMPCAP_FILESIZE:-8388608}"
DUMPCAP_FILES="${DUMPCAP_FILES:-16}"
# dumpcap 默认向 stderr 打 \r 进度行（终端用, 在容器日志里与 sharkd 日志穿插成乱行）;
# 默认静音, 调试时设为 /dev/stderr 或文件路径
DUMPCAP_STDERR="${DUMPCAP_STDERR:-/dev/null}"
SERIAL_PORTS="${SERIAL_PORTS:-}"
CAN_IFACES="${CAN_IFACES:-}"

mkdir -p "$(dirname "$SHARKD_SOCK")" "$SPOOL"
rm -f "$SHARKD_SOCK"

# 三个进程均为守护循环：崩溃/退出 2s 后自动拉起（sharkd 无状态，webshark
# 按请求重连自愈；dumpcap 接口暂缺时循环重试）
(
	while :; do
		sharkd -a "unix:$SHARKD_SOCK" --foreground || true
		rm -f "$SHARKD_SOCK"
		sleep 2
	done
) &

# 等 socket 就绪（最多 5s），避免 webshark 首批探活落空
i=0
while [ ! -S "$SHARKD_SOCK" ] && [ "$i" -lt 50 ]; do
	sleep 0.1
	i=$((i + 1))
done
[ -S "$SHARKD_SOCK" ] || { echo "entrypoint: sharkd socket 未就绪" >&2; exit 1; }

if [ "$DUMPCAP_IFACE" != "none" ]; then
	# 接口暂缺/瞬时报错每 2s 重试；抓宿主机物理网卡须 --network host
	(
		while :; do
			# shellcheck disable=SC2086
			dumpcap -i "$DUMPCAP_IFACE" ${DUMPCAP_FILTER:+-f "$DUMPCAP_FILTER"} \
				-b filesize:"$DUMPCAP_FILESIZE" -b files:"$DUMPCAP_FILES" \
				-w "$SPOOL/net.pcapng" 2> "$DUMPCAP_STDERR" || true
			sleep 2
		done
	) &
fi

# 串口/CAN 采集: 任一非空即拉起 pktdump(空参数的源在其内部跳过);
# 崩溃/全部源不可用退出后 2s 自动拉起
if [ -n "$SERIAL_PORTS" ] || [ -n "$CAN_IFACES" ]; then
	(
		while :; do
			pktdump --spool "$SPOOL" --roll "$DUMPCAP_FILESIZE" --files "$DUMPCAP_FILES" \
				--serial "$SERIAL_PORTS" --can "$CAN_IFACES" || true
			sleep 2
		done
	) &
fi

# PID1 = webshark：SIGTERM 走优雅退出；DUMPCAP_FILES 与 -ring 自动对齐
exec webshark \
	-listen "$LISTEN" \
	-sharkd "$SHARKD_SOCK" \
	-spool "$SPOOL" \
	-ring "$DUMPCAP_FILES"
