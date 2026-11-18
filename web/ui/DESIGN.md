# webshark 前端设计说明

Wireshark 的 Web 前端: 实时抓包列表 + 协议树详情 + 原始字节 hex 三面板, 布局对齐 Wireshark 桌面端。

## 品牌与主题

- 品牌色: 工业深色蓝(`styles/theme.ts` 的 `websharkBrand`, 10→160 蓝阶), 交互主色 `#136AB2` / `#2286E2`。
- **深色为默认主题**(契约 §6), 浅色主题保留, 切换按钮在顶栏; 持久化 key `webshark-theme`。
- 全站字体: Fira Code(`@fontsource/fira-code` 自托管, 400/600), 中文走系统 fallback。

## 色板

| token | 值 | 用途 |
|---|---|---|
| brand-abyss | `#060C14` | 品牌最深阶/页面氛围 |
| brand-interactive | `#136AB2` | 主按钮/激活态 |
| brand-interactive-dark | `#2286E2` | 高亮/焦点 |
| status-red / warning / green | Fluent palette | 专家信息级别、行染色 |

## 布局

- 顶部工具栏: 品牌 `webshark`(仅 standalone; embedded 不渲染, 宿主系统头已示标题) + sharkd 在线状态徽章 + spool 分片数 + 令牌/主题按钮。
- 主体三面板(可拖分割): 包列表(上, 虚拟滚动) | 协议树详情(左下) | hex 字节视图(右下, hex/bin/ascii 三态)。
- 抽屉覆层: 专家信息汇总、流跟随对话视图。
- 401 → TokenGate 录入页(输入框 + 连接按钮, 与主界面同主题)。

## iframe 嵌入协议(宿主注入令牌与主题与筛选)

页面作为 iframe 嵌入宿主控制台时(运行时判定 `env.isEmbedded()`), 令牌与主题由宿主管,
自身不提供切换入口; 实现在 `lib/embedMessaging.ts`。

宿主 → iframe(postMessage, 键名固定):

| type | 字段 | 语义 |
|---|---|---|
| `webshark:set-token` | `token: string` | 设置令牌(空串=清除); 内存态不落盘, 触发连接层以新令牌重连 |
| `webshark:set-theme` | `theme: "dark"\|"light"` | 立即切换主题, 不持久化 |
| `webshark:set-filter` | `filter: string` | 立即应用显示过滤器(空串=清除); 与输入条 Enter 同通道, 同步地址栏 `?filter=` 与 localStorage |

iframe → 宿主:

| type | 语义 |
|---|---|
| `webshark:ready` | 监听器就绪, 宿主此后才能发 set-*(也兼容宿主在 iframe onload 后直接发) |
| `webshark:unauthorized` | 收到 401, 宿主应刷新令牌并重发 set-token; 页面保持断连横幅, 不弹 TokenGate |

约定: 非上述类型/缺字段的消息一律忽略; 不校验 `event.origin`(恶意宿主最多注入错误令牌
导致自身视图 401, 无信息泄露面); 上行对 `window.parent` post, `targetOrigin: "*"`。
初始主题优先级: URL `?theme=light|dark`(embedded)> localStorage(standalone)> dark。
初始筛选优先级: URL `?filter=<串>` > localStorage 上次筛选 > 空(standalone/嵌入式同规则;
筛选持久化含嵌入式——宿主可用 `?filter=`/set-filter 显式覆盖; 刷新与重开网页均还原)。


## 设计 token 命名

全部以 `webshark` 前缀(`websharkBrand` / `websharkDarkTheme` / `websharkLightTheme`),
localStorage key 前缀 `webshark-`。
