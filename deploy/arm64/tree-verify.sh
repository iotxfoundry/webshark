#!/bin/sh
# 端到端: 通过 /run/webshark 主 socket 加载 fixture, 输出五帧协议列
sudo /opt/webshark/bin/compat-probe /run/webshark/sharkd.sock /opt/webshark/spool/fixture/board-fixture.pcapng 2>/dev/null | head -8
