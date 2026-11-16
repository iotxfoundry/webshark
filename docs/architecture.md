# webshark 架构

webshark 不生产 pcapng、不解析协议：分片由外部 dumpcap 滚动写出，解析由外部 sharkd 承担，本项目是两者的装配层与流式推送层。接口与流行为契约见 [api.md](api.md)（唯一契约事实源），本文描述其内部结构。

## 进程与包职责

| 单元 | 职责 |
|---|---|
| `dumpcap`（外部） | 网卡抓包，`-b filesize/files` 环形滚动写 spool 分片目录 |
| `sharkd`（外部） | Wireshark daemon，JSON-RPC over UDS，承担全部协议解析 |
| `cmd/webshark` | 入口：flags 解析、token 安全阀、信号与优雅退出 |
| `webshark.go`（根包） | 对外可嵌入 API：`Config`/`New`/`Handler`/`Close`，内聚 `internal/server` |
| `internal/server` | `/api/*` 路由、token 鉴权中间件、SPA 静态站点、分片缓存实例、下载拼接 |
| `internal/stream` | 实时推送流引擎：会话管理器 + 每流 worker + 筛选模式 + WS 线格式 |
| `internal/sharkd` | JSON-RPC over UDS 客户端（懒拨号、断线重拨、互斥串行） |
| `internal/spool` | 分片枚举（mtime 降序）与分片出现检测（fsnotify + stat 兜底） |
| `web/` | 前端 SPA（React 18 + Fluent UI v9 + Vite 6），打包为单个 `dist.zip` 后 `//go:embed` 内嵌 |

## 数据流

```
dumpcap -b 滚动写
   │ 新 .pcapng 出现
   ▼
spool.Watcher（fsnotify Create 即时 + stat 2s 兜底，known 集去重）
   │ Event 扇出给全部订阅 worker
   ▼
stream.Worker（每流 1 个 goroutine，串行）
   │ 1. sharkd load 最新分片（内存快照；活动分片 size 增长 → 重 load，节流 1s）
   │ 2. frames 按 skip 游标拉增量（150ms 节拍，单次上限 2000 行）
   │ 3. 微批：批满 32 行或 80ms 兜底冲刷；跨分片边界必冲刷
   ▼
Session 出站队列（有界 256 帧，满则阻塞生产端 = 背压）
   ▼
WebSocket 下行 JSON Lines ──▶ 浏览器按 ref（shard+num）去重聚合渲染
```

关键点：

- **重 load 才能看到新行**：`sharkd load` 是内存快照，load 后分片追加字节对实例不可见。增长可见延迟 ≤ 1s（重 load 节流）+ 150ms（拉取节拍）+ 80ms（冲刷兜底）。
- **单次拉 2000 行**：sharkd 的 `skip` 每次调用从第 1 行重扫，拉取成本 O(游标) 与 limit 无关，大批次把重扫次数降一个量级。
- **冷启动锚定最新分片**，不回放环窗历史；历史阅读走筛选模式或导出。

## sharkd 实例拓扑

sharkd 单会话同一时刻只 load 一个文件，因此按职责拆成三类实例：

| 实例 | 数量 | 职责 | load |
|---|---|---|---|
| 流实例 | 每流一个，随流销毁 | 自动 load 最新分片 + 追帧推送；`/api/frame` 命中当前分片时复用 | 当前分片 |
| 轻查询实例 | 进程级 1 个 | `check`（dfilter 校验）/ `complete`（补全），协议注册表全局可用 | 不 load |
| 分片缓存实例 | 进程级 1 个 | 全部帧详情（`/api/frame` 一律走此实例，避免轮换瞬间取错文件），按查询 load，LRU 12 片 | 按查询 |

轻查询与追帧隔离：补全/校验永不被大分片解析阻塞。`/api/frame` 的路由规则：`shard` 等于该流实例当前 load 的分片 → 走流实例；否则走分片缓存实例。

## 并发模型与背压

- **单生产者/单消费者**：每流一个 worker goroutine（生产）+ 一个 WS writer（消费），中间是容量 256 的出站队列。
- **背压 = 阻塞不丢帧**：队列满即阻塞 worker。有界环窗源下「丢帧 + reset 重推」是活锁，正确机制是生产端等消费端——推送速率由消费端决定。
- **sharkd 客户端互斥串行**：一条会话一个请求在途，解析在途不叠加；断线自动重拨。

## 会话生命周期

- **建流**：`POST /api/subscribe` 以前端生成的 `handle` 建流（幂等，重连沿用）；并发上限 16，超限 `429`。
- **宽限**：WS 断开后会话保留 60s（追帧位置与专属实例随流保留），期间同 `handle` 重连续推。
- **容量淘汰**：满 16 流时先淘汰最旧的断线会话腾位（断线会话只是停放游标，重建成本是一次冷启动）；全部连接中才拒绝建流。
- **顶替**：同 `handle` 二次接入关闭旧连接，位置不重不漏。
- **回收**：janitor 每 5s 扫描，超宽限的无连接会话销毁（流实例随之断开）。
- **关闭**：`Manager.Close` 置 closed 后建流返回 `ErrClosed`（`/api/subscribe` → 503），避免 use-after-Close 泄漏 worker。
- **暂停/无连接**：会话冻结，不追不推，位置随会话保留。

## 筛选模式状态机

筛选不是独立管线，而是同一追帧管道的参数（rows 帧 `mode:"filter"`，frames 请求携 filter）。上行 `{"type":"_ctl","op":"filter","filter":...}`，状态迁移：

```
            ┌────────── 提交 filter ──────────┐
            ▼                                 │
 ┌──────────────┐   非法(error帧,保持原状态)  ┌─┴─┐
 │ live（普通流）│◀──── 退出（filter=""）──── │校验│
 └──────┬───────┘                           └─┬─┘
        │ 合法                        合法 ▼
        │              ┌──────────────────────────┐
        └─────────────▶│ filter（位置归零，自环窗   │
                       │ 起点 mtime 升序逐片走查；  │
                       │ 活动分片按增长重 load 续推）│
                       └──────────────────────────┘
```

- 同值重提 no-op 不重走查；非法 filter 发 `error` 帧并保持原状态。
- 退出 = 位置跳最新片回普通流，已收 filter 行的 ref（shard+num）恒有效，视图保留。
- 全流单一 ref 空间：详情走分片缓存实例，无派生文件体系。

## 关键取舍

- **为何桥后台化（sharkd 独立 daemon 而非进程内解析）**：纯 Go 不绑 cgo，Wireshark 的 dissector 树无法在 Go 内复刻；sharkd 是 Wireshark 官方维护的稳定 JSON-RPC 面，字段语义随版本升级自动跟进。代价是部署多一个进程与 socket（见 deployment.md），以及单会话单文件的分片视图边界。
- **为何不用前端轮询**：轮询把「检测增长→load→拉增量」搬到客户端，每次 HTTP 往返 + 重复鉴权 + 无法保序；WS 单连接下行推送把序与流控制（`_ctl` 同连接上行）绑定，筛选切换不会与在途数据竞态。
- **为何不做 WASM 前端解析**：把 pcapng 与 dissector 树搬进浏览器等于在 JS 里维护一份 Wireshark，工程量与体积都不可接受；且 spool 分片在服务端，WASM 仍要全量拉数据。服务端解析 + 行级推送的传输量只有筛选结果。
