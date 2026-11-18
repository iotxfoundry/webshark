#!/bin/sh
# sharkd profile 准备: Lua 分发器入个人插件目录(HOME=/opt/webshark), 注入 FT1.2 链回目标
set -e
PLUGDIR="${HOME}/.local/lib/wireshark/plugins"
mkdir -p "$PLUGDIR"
cp /opt/webshark/lua/webshark_serial.lua "$PLUGDIR/"
case "$SERIAL_FT12" in
  103) D=iec60870_5_103 ;;
  *)   D=iec60870_101 ;;
esac
sed -i "s/__FT12_DISSECTOR__/$D/" "$PLUGDIR/webshark_serial.lua"
