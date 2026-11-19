# Changelog

格式参考 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/),版本号遵循语义化版本。

## [0.1.2] - 2026-11-19

### 新增
- **api**: frames 显式 8 列列集(含绝对日期时间 %Yt)——消除对 sharkd profile 默认列的隐性依赖,推送行 `c[]` 列序成为前后端契约
- **web**: 包列表时间格式切换(绝对日期时间/相对秒)与列显隐——双时间列恒推送,前端即时生效,localStorage 持久化
- **web**: 流图抽屉——当前筛选 rows 纯前端构建泳道序列图,天然跟随筛选与实时推送,点击流行定位帧

### 修复
- **api**: frames 源列编号改 37——sharkd 4.6.9 枚举无 %Rct,38 实为源端口;升级 sharkd 须对照 column.c slist 复核
- **deploy**: sharkd.service 固定 TZ=Asia/Shanghai——绝对时间列按本地时区渲染,系统时区为 UTC 时不再显示 UTC
- **web**: 流图箭头独立成行——标签置顶不再遮挡箭头线;时间列 22ch 完整显示纳秒并垂直居中,补「时间」表头
- **web**: check 校验契约类型对齐 SharkdCheckWarning——清除历史 typecheck 错误

### 其他
- 前端产物随包重建

## [0.1.1] - 2026-11-19

### 新增
- **api**: check 校验失败业务化——改走 200 + code 判定,不再返回 HTTP 标准错误;前端产物随包重建
- **web**: 嵌入态启动门控与品牌收起——首屏请求压到宿主令牌注入之后,消灭 401 噪音
- **deploy**: 色彩规则收编——包列表行色开箱即用,不再依赖主机碰巧装了 wireshark

### 修复
- **deploy**: dumpcap `-b` filesize 单位更正——kB 而非字节,原 8388608 实为 8 GiB 永不轮换
- **deploy**: sharkd 改经典模式,根治会话子进程僵尸
- **web**: 下拉失焦即收起,还原初值不再挂死
- **web**: 抓包表格列宽把手恒显 1px 分隔线,悬停/聚焦/按下转品牌色(可供性可见)

### 其他
- 曾尝试的 SPA 缓存策略(no-cache / immutable)已回退

## [0.1.0] - 2026-11-18

- 初始发布:网络/串口/CAN 多源抓包,FT1.2、IEC 101/103、DL/T 645、DL/T 698.45 协议解析,arm64 板载部署
