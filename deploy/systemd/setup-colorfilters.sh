#!/bin/sh
# Wireshark 色彩规则部署: 包列表行色由 sharkd 色彩滤镜驱动, 规则缺失时列表无配色.
# 安装到 sharkd 个人配置目录(HOME=/opt/webshark → /opt/webshark/.config/wireshark/colorfilters),
# sharkd 启动时读取, 改文件后 systemctl restart sharkd 生效. 幂等, 重复执行只覆盖规则文件.
set -e
CF="${HOME:-/opt/webshark}/.config/wireshark"
mkdir -p "$CF"
install -m 0644 "$(dirname "$0")/colorfilters" "$CF/colorfilters"
chown -R webshark:webshark "$CF" 2>/dev/null || true
echo "installed: ${CF}/colorfilters"
