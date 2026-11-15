# webshark 部署

生产形态为三个进程：`dumpcap`（抓包写分片）→ `sharkd`（协议解析）→ `webshark`（HTTP 服务），另有反向代理终止 TLS。本文以 systemd 为例，unit 文件在 [deploy/systemd/](../deploy/systemd/)。

## 目录与权限

| 路径 | 归属 | 说明 |
|---|---|---|
| `/var/lib/webshark` | `webshark:webshark`（730） | spool 分片目录；dumpcap 需写，webshark 需读 |
| `/run/webshark` | `webshark:webshark`（750） | 运行时目录：sharkd UDS socket |
| `/usr/local/bin/webshark` | root | 二进制（`make build` 产物拷入） |
| `/etc/webshark/token` | `webshark:webshark`（600） | token 文件，可选 |

```sh
sudo id dumpcap 2>/dev/null || sudo useradd -r -s /usr/sbin/nologin dumpcap
sudo id webshark 2>/dev/null || sudo useradd -r -s /usr/sbin/nologin webshark
sudo mkdir -p /var/lib/webshark /run/webshark /etc/webshark
sudo chown webshark:webshark /var/lib/webshark /run/webshark /etc/webshark
sudo chmod 730 /var/lib/webshark
sudo chown root:dumpcap /usr/bin/dumpcap   # 发行版默认已带 filecap，可跳过
sudo getcap /usr/bin/dumpcap               # 期望：cap_net_admin,cap_net_raw+eip
```

抓包权限二选一：

- **方案 A（默认 unit 采用）：`User=dumpcap` 专用用户**。dumpcap 由发行版安装时已赋 `cap_net_admin,cap_net_raw` filecap，专用无登录用户 + 文件能力即可抓包，无 root。需把 spool 目录对 dumpcap 可写（见下方 unit 的 `ExecStartPre` 授组，或直接把目录组设为 `dumpcap`）。
- **方案 B：root 运行 + `AmbientCapabilities=CAP_NET_RAW CAP_NET_ADMIN`**。适用于不想建专用用户的场景：去掉 unit 中 `User=dumpcap`，加一行 `AmbientCapabilities=CAP_NET_RAW CAP_NET_ADMIN`，能力由 systemd 注入而非 filecap。两方案效果等价，取其一。

## systemd unit

三个 unit 拷到 `/etc/systemd/system/` 后 `sudo systemctl daemon-reload`。依赖顺序：dumpcap 与 sharkd 先起，webshark 随后。

### dumpcap.service

```ini
[Unit]
Description=dumpcap 环形抓包写 webshark spool 分片
After=network.target
Before=webshark.service

[Service]
# 方案 A：专用用户（发行版 filecap 已含 cap_net_admin,cap_net_raw）。
# 方案 B 替代：User=root 且加 AmbientCapabilities=CAP_NET_RAW CAP_NET_ADMIN
User=dumpcap
ExecStartPre=/usr/bin/setfacl -m u:dumpcap:rwx /var/lib/webshark
ExecStart=/usr/bin/dumpcap -i eth0 -b filesize:8388608 -b files:16 -w /var/lib/webshark/net.pcapng
Restart=always
RestartSec=3

# dumpcap 只需抓包与写 spool，收紧其余权限
NoNewPrivileges=no
ProtectSystem=strict
ReadWritePaths=/var/lib/webshark
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

- `-i eth0`：按实际网卡改；`-b filesize:8388608`（8MiB/片）与 `-b files:16` 对应 webshark 默认环窗 `-ring 16`，改一处须同步另一处。
- 若系统无 `setfacl`，改为 `chown` 或把 spool 目录组设为 dumpcap。

### sharkd.service

```ini
[Unit]
Description=sharkd daemon (Wireshark 协议解析)
Before=webshark.service

[Service]
User=webshark
RuntimeDirectory=webshark
RuntimeDirectoryMode=0750
ExecStart=/usr/bin/sharkd -a unix:/run/webshark/sharkd.sock --foreground
Restart=always
RestartSec=3

# sharkd 只需读分片与写 socket
ProtectSystem=strict
ReadOnlyPaths=/var/lib/webshark
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

### webshark.service

```ini
[Unit]
Description=webshark - Wireshark 的 Web 前端
After=network.target sharkd.service dumpcap.service
Requires=sharkd.service

[Service]
User=webshark
EnvironmentFile=-/etc/webshark/token
ExecStart=/usr/local/bin/webshark -listen 127.0.0.1:8080 -sharkd /run/webshark/sharkd.sock -spool /var/lib/webshark
Restart=always
RestartSec=3

# 对外暴露时必须配 token：写入 /etc/webshark/token 一行 `WEBSHARK_TOKEN=<随机串>`
NoNewPrivileges=yes
ProtectSystem=strict
ReadOnlyPaths=/var/lib/webshark
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

`EnvironmentFile=-/etc/webshark/token` 的前导 `-` 表示文件可缺省（本机 loopback 使用可不配 token）。

## 反向代理

webshark 含 WebSocket（`/api/ws`）与流式下载（`/api/download` 为 chunked），代理必须透传 upgrade 与禁缓冲。

### Caddy

```caddy
capture.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy 默认透传 WebSocket 与流式响应，无需额外配置；自动签发证书。

### nginx

```nginx
server {
    listen 443 ssl;
    server_name capture.example.com;
    # ssl_certificate / ssl_certificate_key 按需配置

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 3600s;    # 长连接：WS 与流式下载不被掐断
        proxy_buffering off;         # /api/download chunked 直通
    }
}
```

webshark 全部接口同源无前缀，可挂任意反代子路径，无需重写。

## token 与 TLS 建议

- **token 生成**：`openssl rand -hex 32`。写入 `/etc/webshark/token`：`WEBSHARK_TOKEN=<值>`，`chmod 600`、归属 `webshark:webshark`。
- **安全阀**：监听非 loopback 且未配 token 时 webshark 拒绝启动。对外暴露的正确姿势是：webshark 只听 `127.0.0.1:8080`，TLS 与公网入口交给反代，token 经 `WEBSHARK_TOKEN` 注入。
- **TLS**：sharkd 的 UDS 与 webshark 的 HTTP 都不要直接暴露公网（sharkd 官方同样如此建议）；公网侧只有反代的 443。

## 常见故障

| 现象 | 原因与排查 |
|---|---|
| `/api/status` 返回 `connected: false` 带 `reason` | sharkd socket 不可拨。确认 sharkd 在跑、路径与 `-sharkd` 一致、socket 文件归属与 webshark 用户可读（`ls -l /run/webshark/sharkd.sock`）；sharkd 崩溃由 `Restart=always` 拉起 |
| spool 无分片 / 列表空 | dumpcap 未起或权限不足写不进 spool；`ls -l /var/lib/webshark`，确认文件属 `*.pcapng` 且 mtime 在更新；`journalctl -u dumpcap` 看抓包权限报错 |
| 界面一直 401 | token 配置后 `/api/*` 必须带 token：浏览器先在录入页输入；WS 与下载走 `?token=`。核对前端录入值与 `WEBSHARK_TOKEN` 一致 |
| 界面空白/提示未构建 | 二进制里内嵌的是旧/空 `web/dist.zip`。`make web && make build` 重新打包，或开发态用 `-web-dir web/dist` 指向刚构建的目录 |
| 有分片但看不到新行 | dumpcap 与 `-ring` 不匹配导致旧片不轮换；或 sharkd 版本过老。确认 `-b files:16` 与 `-ring 16` 对齐 |
