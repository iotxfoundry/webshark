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

- 顶部工具栏: 品牌 `webshark` + sharkd 在线状态徽章 + spool 分片数 + 令牌/主题按钮。
- 主体三面板(可拖分割): 包列表(上, 虚拟滚动) | 协议树详情(左下) | hex 字节视图(右下, hex/bin/ascii 三态)。
- 抽屉覆层: 专家信息汇总、流跟随对话视图。
- 401 → TokenGate 录入页(输入框 + 连接按钮, 与主界面同主题)。

## 设计 token 命名

全部以 `webshark` 前缀(`websharkBrand` / `websharkDarkTheme` / `websharkLightTheme`),
localStorage key 前缀 `webshark-`。
