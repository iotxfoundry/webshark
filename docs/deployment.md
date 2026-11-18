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

## Docker 部署与抓包

镜像三合一（dumpcap + sharkd + webshark），单容器即完整 appliance。构建见 README；本节回答「抓包时网络与权限怎么给」。

**原理先行——网卡不需要挂载**：容器没有独立物理网卡，dumpcap 经 `AF_PACKET` 原始套接字抓包，看见哪些网卡由**网络模式**决定，能不能抓由 **capabilities** 决定，两者都不涉及设备/卷挂载（特殊总线除外，见下表末行）。缺能力时的现象是 dumpcap 报 `You don't have permission to capture on that device`，容器日志每 2s 重试。

| 抓什么 | 网络模式 | capabilities | 卷挂载 |
|---|---|---|---|
| 宿主机物理/ VLAN 网卡 | `--network host` | `--cap-add NET_ADMIN --cap-add NET_RAW` | spool 建议持久卷 |
| 另一容器的流量 | `--network container:<目标容器名>`（共享其网络栈，抓它看到的 `eth0`） | 同上 | spool 建议持久卷 |
| 默认 bridge 网络下容器自身流量 | 默认 bridge（只能看到容器自己的 `eth0`/veth） | 同上 | spool 建议持久卷 |
| 不抓包，只分析已有 pcap | 任意（bridge 即可） | **无需**任何能力 | 分片目录挂卷（只读 `:ro` 即可） |
| 特殊总线（USB usbmon 等） | 任意 | 同上 + `--device /dev/usbmon0` 等设备挂载 | 同上 |

### 场景 A：抓宿主机网卡（最常用）

`--network host` 下容器与宿主共享网络栈，`DUMPCAP_IFACE` 直接填宿主网卡名（`ip -br link` 查看）；端口映射失效（端口本就在宿主栈上），`LISTEN` 建议只听 loopback 交给反代：

```sh
docker run -d --name webshark \
  --network host \
  --cap-add NET_ADMIN --cap-add NET_RAW \
  -e WEBSHARK_TOKEN="$(openssl rand -hex 32)" \
  -e LISTEN=127.0.0.1:8080 \
  -e DUMPCAP_IFACE=eth0 \
  -e DUMPCAP_FILTER="port 443 or port 80" \
  -v /var/lib/webshark:/var/lib/webshark \
  ghcr.io/iotxfoundry/webshark:latest
```

要点：物理网卡在 host 网络下**原名可见**（eth0/ens192/…），不是挂载进来的；`--privileged` 能跑但授权过宽，不要用。宿主防火墙（nftables/iptables）照常作用于容器流量。

### 场景 B：抓另一个容器

`--network container:<名>` 让 webshark 进入目标容器的网络命名空间——`DUMPCAP_IFACE=eth0` 抓到的就是目标容器看到的全量流量（含它在 bridge 网络里的进出）：

```sh
docker run -d --name webshark-target \
  --network container:myapp \
  --cap-add NET_ADMIN --cap-add NET_RAW \
  -e WEBSHARK_TOKEN="$(openssl rand -hex 32)" \
  -e DUMPCAP_IFACE=eth0 \
  -v /var/lib/webshark:/var/lib/webshark \
  ghcr.io/iotxfoundry/webshark:latest
```

注意：此模式下**没有独立网络栈**，`LISTEN` 监听的 8080 是目标容器的 8080（端口冲突/访问路径按目标容器算）；多数部署把它当旁路抓包器，用 `LISTEN=127.0.0.1:8080` 后经目标容器内访问并不现实——需要 UI 时推荐改用场景 A 抓宿主网卡，或 docker0 网桥上的宿主侧流量。

### 场景 C：默认 bridge 抓容器自身

什么都不加的 `-p 8080:8080` 只能抓 webshark 容器自己的进出流量（调试用）；抓不到宿主或其它容器。

### 场景 D：纯分析（不抓包）

```sh
docker run -d --name webshark -p 8080:8080 \
  -e DUMPCAP_IFACE=none \
  -e WEBSHARK_TOKEN="$(openssl rand -hex 32)" \
  -v "$PWD/pcap:/var/lib/webshark:ro" \
  ghcr.io/iotxfoundry/webshark:latest
```

无需任何 capabilities 与 host 网络。分片/pcap 放进挂载目录即出现在环窗列表（命名 `net_*.pcapng` 与 mtime 排序规则见 README）。

### 核验与排障

```sh
docker exec <容器> dumpcap -D        # 列容器可见网卡——为空/缺目标网卡即网络模式不对
docker logs <容器> 2>&1 | grep -i permission   # 缺 NET_ADMIN/NET_RAW 的典型报错
```

- capabilities 是运行时授予、不写入镜像；换宿主机/换编排工具（compose 的 `cap_add:`/`network_mode:` 同名项）都要重给。
- docker compose 等价：`network_mode: host`、`cap_add: [NET_ADMIN, NET_RAW]`、`volumes:` 与 `environment:` 按 docker run 对应。
- 远程抓包（`DUMPCAP_IFACE=rpcap://<host>/<iface>`）依赖目标机 rpcapd，且明文传输，内网排查才考虑。

### 串口与 CAN（pktdump）

镜像内的 `pktdump` 在 dumpcap 之外补两类工业总线采集：串口（eBPF 挂内核 tty 层旁听任意进程的串口字节流，3.5 字符静默成帧——**协议无关**，采集层不含协议知识，落 LINKTYPE_USER0=147）与 CAN（SocketCAN RAW socket，落 LINKTYPE_CAN_SOCKETCAN=227）。串口帧采集落 `SPOOL` 目录（`cap-*.pcapng`），环窗一并可见；串口的字段级深度解析需 Lua 启用的 sharkd（PPA 构建，Debian 官方构建的 sharkd 不加载 Lua——arm64 板载按上文二进制部署章节部署即可），容器内 Debian 构建 sharkd 下 USER0 帧呈现为通用帧 hex；`SERIAL_PORTS` 与 `CAN_IFACES` 都为空时 pktdump 整个不起。

串口多协议支持矩阵：

| 协议 | 解析位置 | 深度 |
|---|---|---|
| Modbus RTU | 内建 dissector `mbrtu`（Lua 分发器 CRC16 校验通过后链回） | 完整字段树（功能码/寄存器/异常码） |
| FT1.2（IEC 60870-5-101/103） | Lua 分发器形态识别后整帧链回内建 dissector，链回内建 `iec60870_101`（如需按 103 语义解析，改分发器一行链回 `iec60870_5_103`） | 101/103 链路帧形态相同（起始 10/68、L、C、A、CS、16），仅 ASDU 语义不同 |
| DL/T 645-2007 | Lua 树级解析（地址/控制码/数据标识 DI/数据区还原/校验和） | 控制码拆方向/从动/后续/功能码（读数据 0x11、读后续 0x12、写数据 0x14 等）；数据区 -0x33 还原 + BCD→数值 + 量纲（常用 DI 子集：组合有功总电能 9010、正有功总电能 9011、正有功费率电能 010101xx、相电压 020101xx、相电流 030101xx、日期时间 9001 等约 20 条）；未知 DI 显示 `DI=xxxxxxxx (BCD 原值)`；校验错标 `(错)` 不丢帧 |
| DL/T 698.45 | Lua 链路层骨架树（`68 H L(2B 小端) SA CA SA_D CA_D APDU CS1 CS2 16`） | H 控制域逐位（方向/存在 PC/功能码高低位）、L、SA/CA/SA_D/CA_D、APDU 原始字节（A-XDR 未解）、CS1（68..CA_D）/CS2（H..APDU）校验标记，错标 `(错)` 不丢帧 |

DL/T 645 数据标识映射取常用子集，移植自社区开源解析表：[lua-dlt645](https://github.com/srdgame/lua-dlt645)（MIT 许可，DI 解码/服务码表）与 [600888/dlt645](https://github.com/600888/dlt645)（Apache-2.0 许可，DI→名称/量纲映射），按本仓帧结构外壳重写。

深度解析依赖 sharkd 编入 Lua 5.4 支持（Debian wireshark-common 4.6.9 实证具备）。分发器按帧结构特征逐级嗅探（FT1.2 → DL/T 645 → DL/T 698.45 → Modbus CRC → 兜底通用帧），多协议多口乃至单口混流均可解析，识别不中不丢帧。

| 采什么 | 设备/接口 | capabilities | 宿主前提 |
|---|---|---|---|
| 串口（RS-232/485，多协议混接） | `--device /dev/ttyUSB0`（多口逗号列出，`@波特率` 逐口指定） | `--cap-add BPF --cap-add PERFMON`（内核 < 5.8 用 `--cap-add SYS_ADMIN`） | 宿主内核 ≥ 5.8 且启用 BTF（CONFIG_DEBUG_INFO_BTF，`ls /sys/kernel/btf/vmlinux` 存在）且未 lockdown BPF；目标架构 amd64/arm64/arm（eBPF 产物已按 GOARCH 入库） |
| CAN（SocketCAN） | 不挂载设备；宿主先配好比特率并 up：`ip link set can0 type can bitrate 500000 && ip link set can0 up` | `--cap-add NET_RAW` | 宿主内核编 CONFIG_CAN + CONFIG_CAN_RAW |

要点：

- 串口波特率只影响成帧的 3.5 字符静默时长（framer t35），不改设备 termios——采集在内核 tty 层旁听，线上业务进程照常独占设备，互不干扰；`@波特率` 填线上真实速率（≤19200 按 3.5×11 位/baud 计算，更高固定 1.75ms）。
- 成帧与解析分层：采集层只按字节时序静默切帧（协议无关），协议语义全部在 sharkd 侧 Lua 分发器——协议识别、字段树与校验和判定都在解析时做，采错协议不追责采集。
- CAN 比特率归宿主管，容器只建 RAW socket 收帧；经典帧与 CAN FD 帧（`CAN_RAW_FD_FRAMES`）都收。
- 每源启动探测一次：CAN 接口不存在 / eBPF 装载失败打 WARN 跳过该源（不致命），全部源不可用退出 1 → 守护循环 2s 后重试（适配设备晚就绪）；任一源落盘失败取消其余源整体退出。

真机验收 checklist（**本仓仅验证了编译与本地虚拟链路——vcan/pty 桩，真机未测**，上板前逐项过）：

- [ ] 真实 RS-485：3.5 字符静默成帧时序正确（按线上波特率切帧）、多从站请求/响应混流帧序完整、坏帧（撕裂/校验错）不丢落盘留证据；
- [ ] 多协议混接：多口分别跑 Modbus RTU / FT1.2（IEC 101，103 需改分发器一行）/ DL/T 645 / DL/T 698.45，乃至单口混流——Modbus 帧出 `mbrtu` 完整字段树，FT1.2 出内建 101/103 协议树，645 出控制码/DI/BCD 数据树，698 出链路层骨架树（各协议错帧标 `(错)` 仍呈现），识别不中的帧落通用帧 hex；USER0 帧若全部无协议树，先查 Lua 分发器加载链（`/usr/share/wireshark/init.lua` → `/opt/webshark/lua/`，setup-lua.sh 于 sharkd 启动前自动装配）；
- [ ] 真实 CAN / CAN FD：经典帧与 FD 帧均落盘且 Wireshark SocketCAN dissector 出字段，错误帧可见；
- [ ] arm64/armhf 目标板：内核 BTF 存在、kprobe 挂点（`n_tty_write` / `n_tty_receive_buf_common`）在 kallsyms 且可挂；
- [ ] 容器能力授予：BPF/PERFMON（或老内核 SYS_ADMIN）与 NET_RAW 按上表授予，`docker logs` 无 permission/kprobe 报错。

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

## arm64 板载部署（/opt/webshark，二进制部署，无 docker）

实测环境：LPB3588（RK3588）aarch64 / Ubuntu 24.04 / 内核带 BTF；二进制为
CGO_ENABLED=0 交叉编译的静态 ELF（`GOOS=linux GOARCH=arm64`）。
专用系统用户 `webshark`（家目录=/opt/webshark，加入 dialout 组）。

目录布局：

```
/opt/webshark/
├── bin/
│   ├── webshark                           服务(HTTP/WS + SPA)
│   ├── pktdump                            串口/CAN 采集器
│   └── sharkd                             sharkd 4.6.9(PPA noble 构建, Lua 启用)
├── lib/                                   sharkd 运行库树(libwireshark/libwiretap/libwsutil/libgcrypt…)
├── lua/
│   ├── init.lua                           Lua 主入口(系统 init.lua 仅一行 bootstrap 指向此文件)
│   └── webshark_serial.lua                USER0 逐帧嗅探分发器(FT1.2 链回内建 iec60870_101)
├── usr/share/wireshark/init.lua           系统数据目录的一行 bootstrap(dofile 指向 lua/init.lua)
├── webshark.env                           systemd EnvironmentFile(令牌/串口/CAN/网卡)
└── spool/                                 分片环窗(dumpcap net_* + pktdump cap-* 混排)
```

**sharkd 构建来源（关键）**：Debian 官方 wireshark 包的 sharkd 禁用了 Lua
初始化（编译期含 Lua 但运行时不加载任何用户脚本，strace 实证零 .lua 打开）
——必须用 **PPA wireshark-dev/stable 的 noble 构建**（Lua 启用，与 x86 宿主
同源），或自编译。该构建的 sharkd 以**二进制所在目录**为数据/配置根：
`lib/`（库树）、`init.lua`（**一行 bootstrap**，`dofile("/opt/webshark/lua/init.lua")`
指向 /opt/webshark 下的 Lua 主入口——全部 Lua 配置由此收敛于 /opt/webshark/lua/，
系统目录零配置）、`plugins/4.6/`（C 插件扫描目录）。

注意：PPA 构建的库树与数据文件随 deb 安装在 `/usr/lib/aarch64-linux-gnu/`
与 `/usr/share/wireshark/`（系统路径）；板载部署将其收敛进 `/opt/webshark/sharkd/`
自包含目录，运行库经 `/etc/ld.so.conf.d/webshark.conf` 注册 + `ldconfig` 生效，
数据目录按程序目录解析（strace 实证），二者兼容。

systemd 单元与脚本（与设备 `/etc/systemd/system/`、`/opt/webshark/` 逐字一致）：

**`/etc/systemd/system/sharkd.service`**

```ini
[Unit]
Description=sharkd (Wireshark 4.6.9 PPA, Lua 启用) for webshark
After=network.target

[Service]
User=webshark
Group=webshark
Environment=HOME=/opt/webshark
EnvironmentFile=-/opt/webshark/webshark.env
ExecStart=/opt/webshark/bin/sharkd -a unix:/run/webshark/sharkd.sock --foreground
RuntimeDirectory=webshark
RuntimeDirectoryMode=0755
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

> 实际部署的 ExecStart 直启二进制（无 wrapper）：
> `ExecStart=/opt/webshark/bin/sharkd -a unix:/run/webshark/sharkd.sock --foreground`

**`/etc/systemd/system/pktdump.service`**

```ini
[Unit]
Description=webshark 串口/CAN 采集器 (pktdump)
After=network.target

[Service]
User=webshark
Group=webshark
SupplementaryGroups=dialout
Environment=HOME=/opt/webshark
EnvironmentFile=-/opt/webshark/webshark.env
# CAN RAW 需 NET_RAW; 串口 eBPF 旁听需 BPF+PERFMON(内核>=5.8; 更老内核改 CAP_SYS_ADMIN)
AmbientCapabilities=CAP_NET_RAW CAP_BPF CAP_PERFMON
CapabilityBoundingSet=CAP_NET_RAW CAP_BPF CAP_PERFMON
# CAN 比特率按实际总线调整; 两源都留空则本服务空转不启动
ExecStartPre=+/sbin/ip link set can0 down
ExecStartPre=+/sbin/ip link set can0 type can bitrate ${CAN_BITRATE}
ExecStartPre=+/sbin/ip link set can0 up
ExecCondition=/bin/sh -c "[ -n \"$SERIAL_PORTS$CAN_IFACES\" ]"
ExecStart=/opt/webshark/bin/pktdump --spool /opt/webshark/spool --roll 8388608 --files 16 --serial ${SERIAL_PORTS} --can ${CAN_IFACES}
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

**`/etc/systemd/system/webshark.service`**

```ini
[Unit]
Description=webshark 报文分析服务
Requires=sharkd.service
After=sharkd.service

[Service]
User=webshark
Group=webshark
Environment=HOME=/opt/webshark
EnvironmentFile=-/opt/webshark/webshark.env
ExecStart=/opt/webshark/bin/webshark -listen 0.0.0.0:8080 -sharkd /run/webshark/sharkd.sock -spool /opt/webshark/spool -ring 16
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

**`/opt/webshark/env`**（EnvironmentFile；令牌部署时替换）

```sh
# webshark 板载部署配置(systemd EnvironmentFile)
# 令牌: 非 loopback 监听必须设置
WEBSHARK_TOKEN=<令牌>
# 串口监听口: 逗号分隔, 路径@波特率:协议; 协议可选 modbus/ft12/645/698(缺省 modbus)
SERIAL_PORTS=/dev/ttyUSB0@9600:modbus
# CAN 口: 逗号分隔; 留空=不采集 CAN
CAN_IFACES=can0
# CAN 总线比特率(需与总线上其它节点一致)
CAN_BITRATE=500000
```

**真机验收 checklist**：真实 RS-485 从站发帧验证 645/FT1.2 定界时序；CAN
需总线上至少另一节点 ACK（单板自测用 `ip link set canX type can loopback on`）；
arm64/armhf 的内核需 ≥5.8 + CONFIG_DEBUG_INFO_BTF（串口 eBPF 旁听前提）；
pktdump 需 NET_RAW（CAN RAW）与 BPF/PERFMON 能力。
