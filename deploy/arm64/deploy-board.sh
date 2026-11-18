#!/bin/sh
# webshark arm64 板载部署脚本(在目标板上以 root 执行)
# 前提: /opt/webshark/sharkd46/ 内为 Lua 启用的 sharkd 4.6.9(PPA noble 构建,
# 含 lib/ 库树 + share/ 数据 + init.lua 加载器; Debian 官方构建禁用了 sharkd
# 的 Lua 初始化, 不可用; 组装方法见 docs/deployment.md)
set -e
# 部署前: 停旧服务与实验残留进程
systemctl stop webshark.service pktdump-webshark.service sharkd-webshark.service 2>/dev/null || true
pkill -x sharkd 2>/dev/null || true
pkill -x compat-probe 2>/dev/null || true
sleep 0.5
id webshark >/dev/null 2>&1 || useradd -r -d /opt/webshark -s /usr/sbin/nologin webshark
usermod -aG dialout webshark 2>/dev/null || true
mkdir -p /opt/webshark/bin /opt/webshark/lua /opt/webshark/spool /opt/webshark/.local/lib/wireshark/plugins
mv /tmp/webshark /opt/webshark/bin/ && mv /tmp/pktdump /opt/webshark/bin/
mv /tmp/webshark_serial.lua /opt/webshark/lua/
cp /tmp/*.service /etc/systemd/system/
cp /tmp/webshark.env /opt/webshark/env && chmod 640 /opt/webshark/env
cp /tmp/setup-lua.sh /opt/webshark/setup-lua.sh && chmod +x /opt/webshark/setup-lua.sh
chown -R webshark:webshark /opt/webshark
chmod 750 /opt/webshark /opt/webshark/bin /opt/webshark/lua /opt/webshark/spool
systemctl daemon-reload
systemctl enable --now sharkd-webshark.service pktdump-webshark.service webshark.service
systemctl --no-pager --lines=0 status sharkd-webshark pktdump-webshark webshark | grep -E "Active|●"
# 清理实验残留: 全局目录的 Lua 副本与探针(与个人目录重复注册会冲突)
rm -f /usr/lib/aarch64-linux-gnu/wireshark/plugins/webshark_serial.lua \
      /usr/lib/aarch64-linux-gnu/wireshark/plugins/4.6.9/webshark_serial.lua \
      /usr/lib/aarch64-linux-gnu/wireshark/plugins/4.6.9/loud.lua \
      /usr/lib/aarch64-linux-gnu/wireshark/plugins/loud.lua \
      /usr/lib/aarch64-linux-gnu/wireshark/plugins/4.6.9/loud2.lua \
      /root/.config/wireshark/plugins/loud2.lua \
      /root/.local/lib/wireshark/plugins/loud.lua \
      /root/.local/lib/wireshark/plugins/4.6.9/loud.lua 2>/dev/null || true
