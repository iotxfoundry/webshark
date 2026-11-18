#!/bin/sh
# FT1.2 链回内建 dissector 注入(SERIAL_FT12=101|103); Lua 固定于 /opt/webshark/lua/ 不移动
set -e
case "$SERIAL_FT12" in
  103) D=iec60870_5_103 ;;
  *)   D=iec60870_101 ;;
esac
sed -i "s/__FT12_DISSECTOR__/$D/" /opt/webshark/lua/webshark_serial.lua
