#!/bin/sh
# Lua 分发器部署: 替换占位并复制到 sharkd 个人插件目录(HOME=/opt/webshark
# → /opt/webshark/.local/lib/wireshark/plugins, Wireshark 自动扫描的唯一 Lua 目录)
# FT1.2 链回固定内建 iec60870_101(如需按 103 解析, 改下行 D= 即可)
set -e
PLUGDIR="${HOME}/.local/lib/wireshark/plugins"
mkdir -p "$PLUGDIR"
sed "s/__FT12_DISSECTOR__/iec60870_101/" /opt/webshark/lua/webshark_serial.lua > "$PLUGDIR/webshark_serial.lua"
