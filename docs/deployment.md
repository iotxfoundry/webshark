# webshark 部署

生产形态为三个进程：`dumpcap`（抓包写分片）→ `sharkd`（协议解析）→ `webshark`（HTTP 服务），另有反向代理终止 TLS。本文以 systemd 为例，unit 文件在 [deploy/systemd/](../deploy/systemd/)。

## 目录与权限

| 路径 | 归属 | 说明 |
|---|---|---|
| `/var/lib/webshark` | `webshark:webshark`（750） | spool 分片目录；三进程同用户，读写无隔离 |
| `/run/webshark` | `webshark:webshark`（750） | 运行时目录：sharkd UDS socket |
| `/usr/local/bin/webshark` | root | 二进制（`make build` 产物拷入） |
| `/etc/webshark/token` | `webshark:webshark`（600） | token 文件，可选 |

```sh
sudo id webshark 2>/dev/null || sudo useradd -r -s /usr/sbin/nologin webshark
sudo mkdir -p /var/lib/webshark /run/webshark /etc/webshark
sudo chown webshark:webshark /var/lib/webshark /run/webshark /etc/webshark
sudo chmod 750 /var/lib/webshark
sudo getcap /usr/bin/dumpcap               # 期望：cap_net_admin,cap_net_raw+eip（发行版默认已带 filecap，可跳过）
```

抓包权限：unit 采用与 sharkd/webshark **同一用户 `webshark`**，dumpcap 的抓包权限来自发行版安装时赋予二进制的 `cap_net_admin,cap_net_raw` filecap，与运行用户无关，因此无权限提升。同一用户写/读 spool，分片文件（0600）天然可被 sharkd/webshark 读取。**若必须分离用户**：0600 捕获文件默认不可被其他用户读取，需额外 ACL 或轮转后 chmod，本仓不提供。

## systemd unit

三个 unit 拷到 `/etc/systemd/system/` 后 `sudo systemctl daemon-reload`。依赖顺序：dumpcap 与 sharkd 先起，webshark 随后。

### dumpcap.service

```ini
[Unit]
Description=dumpcap 环形抓包写 webshark spool 分片
After=network.target
Before=webshark.service

[Service]
# 与 sharkd/webshark 同一用户：同一用户写/读 spool，避免 0600 分片属主隔离。
# 抓包权限来自发行版 filecap（cap_net_admin,cap_net_raw），与运行用户无关。
User=webshark
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
- dumpcap 需要原始能力，`NoNewPrivileges` 不设 yes（见 unit 注释）。

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
        # 必须显式透传原始 Host：nginx 默认发 $proxy_host（上游地址），
        # 服务端 WS 同源校验会因 Origin 与 Host 不符回 403
        proxy_set_header Host $host;
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
- **传输形态**：webshark 仅支持 Unix socket 形态的 sharkd（路径型 `unix:<path>` 与抽象型 `unix:@<name>`，后者 `-sharkd` 直接传 `@<name>` 即可）。sharkd 的 `tcp:<ip>:<port>` 形态在 Linux 发行版构建中编译期禁用（协议无鉴权，官方视为不安全），本项目亦不支持；Windows 版 sharkd 仅提供 TCP 形态，而 webshark 不面向 Windows。

## 常见故障

| 现象 | 原因与排查 |
|---|---|
| `/api/status` 返回 `connected: false` 带 `reason` | sharkd socket 不可拨。确认 sharkd 在跑、路径与 `-sharkd` 一致、socket 文件归属与 webshark 用户可读（`ls -l /run/webshark/sharkd.sock`）；sharkd 崩溃由 `Restart=always` 拉起 |
| spool 无分片 / 列表空 | dumpcap 未起或权限不足写不进 spool；`ls -l /var/lib/webshark`，确认文件属 `*.pcapng` 且 mtime 在更新；`journalctl -u dumpcap` 看抓包权限报错 |
| 界面一直 401 | token 配置后 `/api/*` 必须带 token：浏览器先在录入页输入；WS 与下载走 `?token=`。核对前端录入值与 `WEBSHARK_TOKEN` 一致 |
| 界面空白/提示未构建 | 二进制里内嵌的是旧/空 `web/dist.zip`。`make web && make build` 重新打包，或开发态用 `-web-dir web/dist` 指向刚构建的目录 |
| 有分片但看不到新行 | dumpcap 与 `-ring` 不匹配导致旧片不轮换；或 sharkd 版本过老。确认 `-b files:16` 与 `-ring 16` 对齐 |
| 有分片但界面空白（`/api/download` 导出 503「分片均不可读」） | 分片文件属主与 sharkd 运行用户不一致（如旧部署 `User=dumpcap` 写出 0600 属主 dumpcap 的文件）。`ls -l /var/lib/webshark` 确认属主是 `webshark`；旧文件 `chown webshark:webshark` 即可 |
