# webshark 接口契约

本文是整个仓库的**唯一契约事实源**。后端（`internal/**`）、前端（`web/src`）、测试三方必须与此一致；单方变更即破坏兼容。

服务端不生产 pcapng，只消费外部写入的 pcapng 分片目录（spool，通常由 `dumpcap -b filesize:... -b files:... -w <spool>/net.pcapng` 滚动产生），并通过外部 `sharkd` daemon 做协议解析。

## 1. 通用约定

- 所有接口同源，无路径前缀（前端用相对路径 `./api/...`），可挂任意反向代理子路径。
- 鉴权：服务端未配置 token 时全部开放；配置后
  - 普通请求（含 `POST`）：`Authorization: Bearer <token>`
  - WebSocket 握手与文件下载（浏览器无法注入 header）：`?token=<token>`
  - 缺失或错误一律 `401`。
- 错误应答统一为 JSON：

```json
{"code": 401, "message": "未认证"}
```

`code` 为 sharkd envelope 的 `code`（透传时）或 HTTP 状态码；`message` 为可读文案。成功应答不经此结构。

- 分片名（`shard`）只接受 spool 目录内的纯文件名，含路径分隔符一律拒绝（防目录穿越）。

## 2. HTTP 接口

### GET /api/status

sharkd 可达性 + 环窗（最近 `-ring` 片，默认 16）分片清单（`mtime` 降序，同 `mtime` 按名字降序）。

```json
{
  "connected": true,
  "files": [{"name": "net_00001_20260101120000.pcapng", "size": 8388608, "mtime": "2026-01-01T12:00:00Z"}]
}
```

`connected=false` 时附 `"reason"`（sharkd socket 不可拨的具体路径）。探活方式为拨号后立即关闭，不 load 任何文件。

### GET /api/download

把环窗（最近 `-ring` 片，默认 16）内的全部分片按 `mtime` 升序逐文件流式拼接为单一 pcapng 响应（pcapng 允许多 SHB/IDB 段共存，纯字节拼接即合法文件）。不设 `Content-Length`（滚动分片随时增删），走 chunked；单文件打开失败（轮换瞬间被删）跳过；无任何分片可读时 `503`。

- 成功：`200`，`Content-Type: application/octet-stream`，`Content-Disposition: attachment; filename="webshark-YYYYMMDDhhmmss.pcapng"`
- spool 无分片：`404`

### POST /api/subscribe

建流/保活并回读流状态。`handle` 由前端生成并长期复用（重连沿用），服务端幂等。

请求：`{"handle": "st-xxxx", "filter": "ip", "paused": false}`（`filter`/`paused` 可省略；用于重连时原子地重建流状态，免得数据先于 `_ctl` 到达）

应答：

```json
{"ok": true, "handle": "st-xxxx", "paused": false, "filter": "", "shard": "net_00001_....pcapng", "cold": false}
```

`cold=true` 表示服务端**新建**了会话（该 handle 不存在或已被淘汰）：追帧位置丢失，新会话锚定最新分片、不回放历史。前端据此清空本地视图（复用 `reset` 语义）后再渲染。

流数达上限（16）时 `429`。

### GET /api/frame

单帧详情，参数 `handle`、`shard`、`num`、`proto`、`bytes`，结果原样透传 sharkd `frame` 的 `result`。
路由规则：**一律**走进程级分片缓存实例（按显式 `shard` 按需 load，LRU 12 片）。不走流实例：worker 换分片时 `load` 与当前分片游标更新非原子，按「是否等于当前分片」分流会在轮换瞬间取到另一文件的同号帧。`shard` 缺省时取会话当前分片，仍为空则 `409`。

### POST /api/follow

`{"handle": "...", "follow": "TCP", "filter": "..."}` → 透传 sharkd `follow` 的 `result`。

### POST /api/tap

`{"handle": "...", "tap0": "expert"}` → 透传 sharkd `tap` 的 `result`，并在同级补注 `"shard"`：tap 实际执行时实时实例载入的分片名。参数键是 sharkd 的 `tap0`（`tap` 会被拒 `-32600`）。

```json
{"shard": "net_00001_....pcapng", "truncated": 0, "taps": [{"tap": "expert", "type": "expert", "details": []}]}
```

`shard` 为空串表示 tap 时未载入分片，或调用期间发生过换片（服务端无法判定实际文件，宁可留空也不给错分片）。前端据此定位专家条目的帧。

明细按帧序最多保留 5000 条（跨 tap 共享预算）；被丢弃的总条数在同级 `"truncated"` 披露（0 = 未截断）。界面上万条明细无法人工阅读，超出部分由 Wireshark 桌面端查看。

### GET|POST /api/check

`{"filter": "ip.addr == 1.2.3.4"}` → 透传 sharkd `check`。走进程级轻查询实例，不 load 文件。

校验失败**不是 HTTP 错误**：dfilter 非法时应答 `200` + `{"code": <sharkd envelope code>, "message": "<可读原因>"}`（如 `{"code": -5001, "message": "Filter invalid - ..."}`），前端按 `code` 非 0 判定无效；合法时透传 sharkd 结果（`err:0` / `warning` 形态）。传输失败（sharkd 不可达）仍 `503`。

### GET|POST /api/complete

补全候选。前端统一传当前输入前缀 `prefix`；sharkd 没有 `prefix` 成员（发它会拒 `-32600 "not a valid member name"`），只有 `field`（字段补全）与 `pref`（协议/首选项补全）两个补全域，服务端因此展开为同值双键：

```json
{"prefix": "icmp."}   // → sharkd {"field":"icmp.","pref":"icmp."}
```

应答中 `field` 与 `pref` 两个数组都由前端合并展示（`d` 缺省回退 `n`）。显式传 `field` 时覆盖字段域取值、`pref` 仍取 `prefix`。GET 与 POST 两种形态等价，空值不携键。走轻查询实例。

### GET /api/ws

WebSocket 升级，`?handle=<handle>&token=<token>`。见第 3 节。

## 3. WebSocket 线格式

一条流一条连接；下行 JSON Lines，上行仅流控制。

### 下行

| 帧 | 载荷 |
|---|---|
| `hello` | `{"type":"hello","mode":"live"}` |
| `rows` | `{"type":"rows","mode":"live\|filter","shard":"...","rows":[row,...]}` |
| `rotated` | `{"type":"rotated","shard":"..."}` |
| `reset` | `{"type":"reset","reason":"..."}` |
| `error` | `{"type":"error","reason":"..."}` |

`row` 为 sharkd `frames` 行的原样字段（`num`、`t`、`s`、`l`、`p`、`i`、`fg`、`bg` 等）追加一个服务端生成的复合键：

```json
{"num": 12, "...": "...", "ref": {"shard": "net_00001_....pcapng", "num": 12}}
```

`ref` 是前端点行查详情的查询键。`rows` 为微批：批满 32 行或 80ms 兜底即发，跨分片边界必冲刷（同一批不混分片）。

服务端以显式列集（frames 请求 `column0..column7`）拉取，推送行 `c[]` 固定 8 列：No.（%m）、相对秒（%Rt）、绝对日期时间（%Yt）、源（%s）、目的（%d）、协议（%p）、长度（%L）、Info（%i）——列序是前端契约，不随 sharkd profile 默认列变化。

### 上行

```json
{"type":"_ctl","op":"capture","state":"pause"}   // 或 "resume"
{"type":"_ctl","op":"filter","filter":"ip"}      // 空串 = 退出筛选, 回普通实时流
```

非 `_ctl` 或坏 JSON 一律忽略，不断连接。流控制走同一连接是为了与数据流保序（HTTP 改筛选会与在途数据竞态）。

## 4. 流行为契约

这些是前端可依赖的语义，也是测试的断言点。

- **冷启动**：锚定 `mtime` 最新分片，不回放环窗历史（历史阅读走筛选模式或导出）。
- **追帧**：150ms 节拍按 `skip` 游标拉取（单次上限 2000 行）；活动分片字节增长时重 `load`（`sharkd load` 是内存快照，不重 load 看不到新行），重 load 节流 1s。
- **轮换**：新分片出现 → 切到新分片并从第 1 行起推，发 `rotated`。
- **暂停 / 无连接**：会话冻结，不追不推，位置随会话保留。
- **恢复 / 重连**：从保存位置续推；同 `handle` 二次接入顶替旧连接（旧连接被关闭），不重不漏。
- **位置出环窗**（分片已被环形清理）：发 `reset` 并从当前状态全量重推。
- **筛选模式**：先经轻查询实例校验 dfilter；非法 → `error` 帧且保持原状态；合法 → 位置归零，自环窗起点逐片走查推送（`mode:"filter"`），新分片持续按筛选推送，不暂停不丢包。
- **背压**：出站队列有界（256 帧），满则生产端阻塞（推送速率由消费端决定），不丢帧。
- **宽限**：连接断开后会话保留 60s，期间重连续推；超时由 janitor（5s 节拍）回收。**容量满时会提前淘汰最旧的断线会话**腾位（否则刷新 16 次即全员 429），被淘汰者重连得到新会话，应答带 `cold=true`。
- **上限**：并发流 16；全部 16 条都处于连接中时建流才返回 `429`。

## 5. 进程内 sharkd 实例拓扑

| 实例 | 数量 | 职责 | load |
|---|---|---|---|
| 流实例 | 每流一个 | 自动 load 最新分片 + 追帧推送；`/api/follow`、`/api/tap` | 当前分片 |
| 轻查询实例 | 进程级 1 个 | `check` / `complete`（协议注册表全局可用） | 不 load |
| 分片缓存实例 | 进程级 1 个 | 历史帧详情，LRU 12 片 | 按查询 load |

轻查询与追帧隔离：补全/校验永不被大分片解析阻塞。

## 6. 前端契约要点

- 单页应用，HashRouter，深色工业主题为默认；Fluent UI v9 + react-virtuoso。
- token 存 `localStorage`；收到 401 时清 token 并展示录入页。
- 行去重按 `ref`（`shard` + `num`）；显示序号（No）为前端聚合自增，不是 sharkd 帧号。
- `reset` 清空本地聚合器后按后续推送重建。
