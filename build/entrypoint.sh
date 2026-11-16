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
set -eu

SPOOL="${SPOOL:-/var/lib/webshark}"
SHARKD_SOCK="${SHARKD_SOCK:-/run/webshark/sharkd.sock}"
LISTEN="${LISTEN:-0.0.0.0:8080}"
DUMPCAP_IFACE="${DUMPCAP_IFACE:-eth0}"
DUMPCAP_FILTER="${DUMPCAP_FILTER:-}"
DUMPCAP_FILESIZE="${DUMPCAP_FILESIZE:-8388608}"
DUMPCAP_FILES="${DUMPCAP_FILES:-16}"

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
				-w "$SPOOL/net.pcapng" || true
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
