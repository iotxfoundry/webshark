// CapturePage.tsx - 抓包页(单条工具栏承载品牌/状态徽章/操作, 自撑满 100dvh):
// 工具栏形态: standalone 显示品牌文字; iframe 嵌入(embedded)隐藏品牌, 其余一致.
// 数据面: 服务端后台追帧 → rows 微批推送(行含 ref 复合查询键) → 跨分片聚合器追加渲染(显示 No =
// 聚合自增序号); rotated 信息性; reset 清空聚合器按服务端全量重推对齐; 断线重连(同 handle)按
// 游标续推无重无漏.
// 交互面: frame 详情/check/complete/follow/expert 走 HTTP 请求-响应.
// 控制: autoscroll = 纯前端吸底显示开关(关=数据照常追加仅不滚动, Wireshark 同名语义);
// 暂停/恢复抓包 = WS 上行 _ctl{op:"capture"} 流控制(冻结推送, 落盘 spool 不受影响).
// 分片策略: 永远实时跟随(服务端自动选片), 导出 = spool 全部历史合并单一 pcapng.
// 布局: 上=包列表+filter 条; 下=协议树详情|原始字节 hex(左右分割, 可拖).
// (Wireshark 三面板数据面) 每流一条 WS → 服务端推送消费(纯推送, 零轮询).
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Badge, Button, MessageBar, MessageBarBody, MessageBarTitle, Spinner, Tab, TabList,
  Text, makeStyles, mergeClasses, tokens, Tooltip,
} from "@fluentui/react-components";
import {
  ArrowDownload24Regular, Chat16Regular, Info16Regular, Key16Regular,
  Pause16Regular, Play16Regular, WeatherMoon16Regular, WeatherSunny16Regular,
} from "@fluentui/react-icons";
import { useCaptureStream } from "../hooks/useCaptureStream";
import { useCaptureStatus } from "../hooks/useCaptureStatus";
import { initialFilter, persistFilter, syncFilterUrl } from "../env";
import { setFilterHandler } from "../lib/embedMessaging";
import type { CaptureViewRow } from "../hooks/captureAggregator";
import { downloadCaptureAll } from "../api/capture";
import { getToken } from "../api/token";
import { useThemeStore } from "../stores/theme";
import type { SharkdFrameDetail, SharkdTreeNode } from "../api/sharkdTypes";
import { CaptureFilterBar } from "../components/capture/CaptureFilterBar";
import { CapturePacketList } from "../components/capture/CapturePacketList";
import { CaptureTreeView } from "../components/capture/CaptureTreeView";
import { CaptureHexView, type HexMode } from "../components/capture/CaptureHexView";
import { FollowStreamView } from "../components/capture/FollowStreamView";
import { ExpertPanel } from "../components/capture/ExpertPanel";
import { useMonoStyles } from "../styles/mono";
import { hintPane, hintMuted } from "../styles/hint";
import { errMsg } from "../api/client";

/** follow 类型映射: f 串协议前缀 → follow type(协议短名大写, sharkd 实测小写被拒 -12001) */
const FOLLOW_TYPES: Record<string, string> = { tcp: "TCP", udp: "UDP", tls: "TLS", http: "HTTP", http2: "HTTP2", quic: "QUIC" };

const useStyles = makeStyles({
  // 100dvh: App 无可见外壳(iframe 嵌入形态亦然), 页面自撑满视口; body 不滚, 内部面板滚
  root: { display: "flex", flexDirection: "column", gap: tokens.spacingVerticalS, height: "100dvh", minHeight: 0, padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalL}` },
  toolbar: {
    display: "flex", alignItems: "center", gap: tokens.spacingHorizontalS,
    flexShrink: 0,
  },
  btn: { minWidth: "72px", whiteSpace: "nowrap" },
  // 上下大分割: 上列表区(flex 按高度比例) + 下部详情区
  vSplit: { display: "flex", flexDirection: "column", flex: 1, minHeight: 0 },
  topPane: { display: "flex", flexDirection: "column", minHeight: "0", gap: tokens.spacingVerticalS },
  hDivider: { flexShrink: 0, height: "6px", cursor: "row-resize", background: tokens.colorNeutralStroke2, touchAction: "none" as const },
  hDividerActive: { background: tokens.colorBrandBackground },
  bottomSplit: { display: "flex", flex: 1, minHeight: 0, alignItems: "stretch" },
  treePane: { display: "flex", flexDirection: "column", flex: 1, minWidth: "360px", border: `1px solid ${tokens.colorNeutralStroke2}`, borderRadius: tokens.borderRadiusMedium, overflow: "hidden", background: tokens.colorNeutralBackground1 },
  vDivider: { flexShrink: 0, width: "6px", cursor: "col-resize", background: tokens.colorNeutralStroke2, touchAction: "none" as const },
  hexPane: { display: "flex", flexDirection: "column", border: `1px solid ${tokens.colorNeutralStroke2}`, borderRadius: tokens.borderRadiusMedium, overflow: "hidden", background: tokens.colorNeutralBackground1, flexShrink: "0" as const },
  hexTabs: {
    selectors: {
      "& .fui-Tab__content": { fontSize: tokens.fontSizeBase200, fontWeight: tokens.fontWeightSemibold },
    },
  },
  paneHead: { padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`, fontWeight: tokens.fontWeightSemibold, fontSize: tokens.fontSizeBase200, background: tokens.colorNeutralBackground2, borderBottom: `1px solid ${tokens.colorNeutralStroke1}`, flexShrink: 0, display: "flex", alignItems: "center", justifyContent: "space-between" },
  // 详情面板常挂内容容器(换帧在途降透明, 旧内容带标记可见不卸载重挂)
  paneBody: { display: "flex", flexDirection: "column", flex: 1, minHeight: 0 },
  dimmed: { opacity: 0.45, transition: "opacity .15s" },
  parseHint: { ...hintPane, minHeight: "120px" },
  paneHint: { padding: tokens.spacingVerticalS, ...hintMuted },
});

/** 详情区占位(等待实时数据流, 替换上一帧旧字节避免张冠李戴) */
function PaneHint({ text }: { text: string }) {
  const styles = useStyles();
  return <div className={styles.paneHint}>{text}</div>;
}

/** 页面私有分割条: 上下(v=包列表高度)/左右(h=字节视图宽度)两条共用同一 pointer 五件套
 *  (按下捕获/移动/抬起/取消/双击复位), 仅轴向样式、取值函数与复位目标由调用方给出 */
function SplitDivider({ axis, drag, setDrag, onMove, onReset, title }: {
  axis: "v" | "h";
  drag: "v" | "h" | null;
  setDrag: (d: "v" | "h" | null) => void;
  onMove: (e: React.PointerEvent<HTMLDivElement>) => void;
  onReset: () => void;
  title: string;
}) {
  const styles = useStyles();
  return (
    <div
      className={mergeClasses(axis === "v" ? styles.hDivider : styles.vDivider,
        drag === axis && styles.hDividerActive)}
      onPointerDown={(e) => { e.preventDefault(); e.currentTarget.setPointerCapture(e.pointerId); setDrag(axis); }}
      onPointerMove={onMove}
      onPointerUp={() => setDrag(null)}
      onPointerCancel={() => setDrag(null)}
      onDoubleClick={onReset}
      title={title}
    />
  );
}

export default function CapturePage({ embedded = false, onOpenToken }: { embedded?: boolean; onOpenToken?: () => void }) {
  const styles = useStyles();
  const monoS = useMonoStyles();
  const { theme, toggle } = useThemeStore();
  const { status } = useCaptureStatus();
  const fileCount = status?.files?.length ?? 0;
  const [filterInput, setFilterInput] = useState(initialFilter); // 初始: URL ?filter= > 上次筛选
  const [paused, setPausedUi] = useState(false);
  // autoscroll = 纯前端显示开关(数据照常追加, 仅控制吸底), Wireshark 同名语义
  const [following, setFollowingUi] = useState(true);
  const [detail, setDetail] = useState<SharkdFrameDetail | null>(null);
  const [selRow, setSelRow] = useState<CaptureViewRow | null>(null);
  const [selNode, setSelNode] = useState<SharkdTreeNode | undefined>(undefined);
  const [frameErr, setFrameErr] = useState("");
  const [frameLoading, setFrameLoading] = useState(false);
  const [topH, setTopH] = useState(45); // 上面板高度百分比
  const [hexMode, setHexMode] = useState<HexMode>("hex");
  const [hexW, setHexW] = useState(420); // hex 面板宽度 px
  const [drag, setDrag] = useState<"v" | "h" | null>(null);
  const rootRef = useRef<HTMLDivElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);

  const session = useCaptureStream();
  const { conn, rows, filter, error, setCapturePaused, applyFilter,
    fetchFrame, fetchFollow, fetchExpert, checkFilter, complete } = session;

  // 抓包暂停/恢复: WS 上行 _ctl 流控制; conn open 时按当前态断言(重连后不丢暂停态)
  useEffect(() => {
    if (conn === "open") setCapturePaused(paused);
  }, [conn, paused, setCapturePaused]);

  // 流跟随入口: 选中树节点 f 串匹配 <proto>.stream == N 才可用(协议前缀映射 follow 类型)
  const [followStream, setFollowStream] = useState<{ type: string; filter: string } | null>(null);
  const followStreamMatch = useMemo(() => {
    const f = selNode?.f;
    const m = f !== undefined ? /^(tcp|udp|tls|http2?|quic)\.stream == \d+/.exec(f) : null;
    return m !== null && f !== undefined ? { type: FOLLOW_TYPES[m[1]], filter: f } : null;
  }, [selNode]);

  // filter 应用(Enter/清除/宿主注入/URL 还原): WS 上行 _ctl{op:"filter"} 进出筛选模式
  // (空串退出); 清选中态; 同步 localStorage(重开网页保留)与地址栏(刷新/分享凭 URL 还原)
  const onApplyFilter = useCallback((f: string) => {
    setFilterInput(f);
    applyFilter(f);
    persistFilter(f);
    syncFilterUrl(f);
    setSelRow(null); setSelNode(undefined); setDetail(null);
  }, [applyFilter]);

  // 初始还原 + 宿主注入(webshark:set-filter): 两者都汇入 onApplyFilter 单通道;
  // applyFilter 未连接仅存态、连接后重断言, 故挂载即应用安全. 初始为空不发请求.
  useEffect(() => {
    const f0 = filterInput;
    if (f0 !== "") onApplyFilter(f0);
    setFilterHandler((f) => onApplyFilter(f));
    return () => setFilterHandler(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps -- filterInput 仅取初始种子(经 useState 惰性初始化)
  }, [onApplyFilter]);

  // 行选择: frame 详情(树+hex), 查询键 = 行内隐藏复合键 ref(分片名+帧号, HTTP 按复合键路由).
  // 换帧保留旧详情渲染(降透明 + 面板头「加载帧详情…」标记, 旧内容「带标记的可见」而非占位
  // 替换), 新详情到达原地替换; 失败清 detail(空态 + 错误横幅透出服务端消息, 历史 404 =
  // 「分片不在实时实例视图」诚实呈现), 旧帧数据不在无标记下冒充新帧.
  // 串帧防护: 冷分片请求慢/缓存命中快, 迟到的旧应答不得覆盖新帧——序号不匹配即丢弃.
  const detailSeq = useRef(0);
  const onRowSelect = useCallback(async (r: CaptureViewRow) => {
    const seq = ++detailSeq.current;
    setSelRow(r);
    setSelNode(undefined);
    setFrameLoading(true);
    try {
      const d = await fetchFrame(r.ref);
      if (seq !== detailSeq.current) return;
      setDetail(d);
      setFrameErr("");
    } catch (e) {
      if (seq !== detailSeq.current) return;
      setDetail(null);
      setFrameErr(`帧 ${r.no} 详情失败: ${errMsg(e)}`);
    } finally {
      if (seq === detailSeq.current) setFrameLoading(false);
    }
  }, [fetchFrame]);

  // 树节点 f 串 → filter 输入插入
  const onNodeFilter = useCallback((f: string) => {
    setFilterInput((prev) => (prev.trim() === "" ? f : `${prev.trim()} && ${f}`));
  }, []);

  // 专家信息开关(抽屉覆层). 定位分片以 tap 应答为准(轮换后也可信), 打开面板无需快照
  const [expertOpen, setExpertOpen] = useState(false);

  // 专家信息行点击 → 选中该帧并拉详情. 最小实现: 不强求包列表滚动定位; 命中已持有行
  // (按 tap 分片+帧号匹配)则复用行选择获得列表高亮, 否则以 tap 分片+帧号合成行仅拉详情
  // (显示 No 以原始帧号兜底); tap 应答未载入分片(shard 空)不查询, 透出提示
  const onExpertFrame = useCallback((n: number, shard: string) => {
    if (shard === "") {
      setFrameErr("实时实例尚未就绪，无法定位帧");
      return;
    }
    const r = rows.find((x) => x.ref.shard === shard && x.ref.num === n)
      ?? { c: [], num: n, no: n, ref: { shard, num: n } };
    void onRowSelect(r);
  }, [rows, onRowSelect]);

  // 分割拖动: 上下(topH%) 与 左右(hexW px), pointer capture 复用; 拖动期间移动事件只派发给
  // 持有捕获的分割条, 故按 drag 轴向取值与两处独立 handler 语义等价
  const onDragMove = useCallback((e: React.PointerEvent<HTMLDivElement>) => {
    if (drag === "v") {
      const rect = rootRef.current?.getBoundingClientRect();
      if (!rect) return;
      setTopH(Math.min(80, Math.max(15, ((e.clientY - rect.top) / rect.height) * 100)));
    } else if (drag === "h") {
      const rect = bottomRef.current?.getBoundingClientRect();
      if (!rect) return;
      setHexW(Math.min(Math.max(rect.right - e.clientX, 240), Math.max(rect.width - 360, 240)));
    }
  }, [drag]);

  // 详情区门控: 连接就绪但尚无数据(首连等待推送/reset 后服务端全量重推重建中)整区占位;
  // 帧详情在途(frameLoading)不吞占位——面板常挂, 旧内容降透明 + 头部提示标记
  const parseWindow = conn === "open" && rows.length === 0;
  // 空态三态文案: 暂停(服务端停推) / 筛选中(等匹配报文) / 默认(等实时流)
  const waitLabel = paused ? "抓包已暂停" : filter !== "" ? "等待匹配报文…" : "正在等待实时数据流…";
  return (
    <div className={styles.root} ref={rootRef}>
      {/* 顶部横幅: 连接中断 / 控制指令未生效 / 帧详情失败(条件各自独立, 按固定顺序展示) */}
      {[
        { when: conn === "closed", title: "实时流已断开", body: "连接中断, 自动重连中(续推游标保留, 已有数据不丢)…" },
        { when: error !== "", title: "控制指令未生效", body: error },
        { when: frameErr !== "", title: "帧详情", body: frameErr },
      ].filter((m) => m.when).map((m) => (
        <MessageBar key={m.title} intent="warning">
          <MessageBarBody><MessageBarTitle>{m.title}</MessageBarTitle>{m.body}</MessageBarBody>
        </MessageBar>
      ))}

      {/* 工具栏: [品牌(嵌入时隐藏)] 状态徽章(sharkd HTTP 探活 / WS 流 / 暂停) + 弹性空隙 +
          操作(专家信息 / 暂停恢复 / 导出 / 主题 / 令牌). sharkd 探活(HTTP)与 WS 连接是两个
          不同状态, 均保留各出现一次. */}
      <div className={styles.toolbar}>
        {/* 品牌: standalone 显示; embedded 用 visibility 隐藏(占位不跳高度) */}
        <Text weight="semibold" size={500} style={{ visibility: embedded ? "hidden" : "visible" }}>
          Webshark
        </Text>
        <Badge appearance="tint" color={status?.connected ? "success" : "danger"}
          title={status?.connected ? "sharkd 已连接" : `sharkd 不可达${status?.reason ? `: ${status.reason}` : ""}`}>
          {status?.connected ? "sharkd 在线" : "sharkd 离线"}
        </Badge>
        <Badge appearance="tint" color="informative" title="spool 分片数">{fileCount} 分片</Badge>
        <Badge appearance="tint" color={conn === "open" ? "success" : conn === "connecting" ? "warning" : "danger"}>
          {conn === "open" ? "已连接" : conn === "connecting" ? "连接中…" : "重连中…"}
        </Badge>
        {paused && <Badge appearance="tint" color="warning">已暂停</Badge>}
        <div style={{ flex: 1 }} />
        <Button className={styles.btn} icon={<Info16Regular />}
          onClick={() => setExpertOpen(true)}>
          专家信息
        </Button>
        {/* 暂停/恢复抓包: WS 上行 _ctl 流控制(冻结推送; 落盘 spool 不受影响) */}
        <Tooltip content="暂停后界面停止接收新报文, 磁盘记录不受影响; 断线期间的切换将在重连后生效" relationship="description">
          <Button className={styles.btn} icon={paused ? <Play16Regular /> : <Pause16Regular />} onClick={() => setPausedUi(!paused)}>
            {paused ? "恢复抓包" : "暂停抓包"}
          </Button>
        </Tooltip>
        {/* 导出 = spool 全部历史按时间序合并为单一 pcapng(服务端拼接) */}
        <Tooltip content="导出全部历史为单一 pcapng 文件" relationship="label">
          <Button className={styles.btn} icon={<ArrowDownload24Regular />} disabled={conn !== "open"}
            onClick={() => void downloadCaptureAll()}>
            导出 pcapng
          </Button>
        </Tooltip>
        {/* 主题切换 / 访问令牌: 图标钮(locked 状态在 App, 令牌录入页经回调由 App 切换);
            embedded 下两者隐藏(主题/令牌归宿主管, 经 postMessage 注入) */}
        {!embedded && (
          <Tooltip content={theme === "dark" ? "切换浅色主题" : "切换深色主题"} relationship="label">
            <Button appearance="subtle" size="small"
              icon={theme === "dark" ? <WeatherSunny16Regular /> : <WeatherMoon16Regular />}
              aria-label="切换主题" onClick={toggle} />
          </Tooltip>
        )}
        {!embedded && onOpenToken !== undefined && (
          <Tooltip content={getToken() === "" ? "设置访问令牌" : "更换访问令牌"} relationship="label">
            <Button appearance="subtle" size="small" icon={<Key16Regular />}
              aria-label="访问令牌" onClick={onOpenToken} />
          </Tooltip>
        )}
      </div>

      {/* filter 条(全宽): 提交即进出筛选模式(_ctl 上行); activeFilter = 已生效筛选(角标+高亮) */}
      <CaptureFilterBar value={filterInput} onChange={setFilterInput} onApply={onApplyFilter}
        check={checkFilter} complete={complete} activeFilter={filter} />

      {/* 上下分割: 包列表 | 详情区 */}
      <div className={styles.vSplit}>
        <div className={styles.topPane} style={{ height: `${topH}%` }}>
          {parseWindow ? (
            // 等待实时数据反馈: 连接就绪但服务端尚未推来首批/reset 重建中
            <div className={styles.parseHint}>
              <Spinner label={waitLabel} />
            </div>
          ) : (
            <CapturePacketList
              rows={rows}
              selectedNum={selRow?.no}
              onSelect={(r) => void onRowSelect(r)}
              following={following}
              onFollowCancel={() => setFollowingUi(false)}
              onFollowResume={() => { if (!following) setFollowingUi(true); }}
            />
          )}
        </div>
        <SplitDivider axis="v" drag={drag} setDrag={setDrag} onMove={onDragMove}
          onReset={() => setTopH(45)} title="拖动调整包列表高度, 双击复位" />
        <div className={styles.bottomSplit} ref={bottomRef}>
          <div className={styles.treePane}>
            <div className={styles.paneHead}>
              <span>协议树详情{selRow ? ` — 帧 ${selRow.no}` : ""}</span>
              <span style={{ display: "flex", alignItems: "center", gap: 4, flexShrink: 0 }}>
                {frameLoading && selRow && (
                  <span style={{ color: tokens.colorNeutralForeground3, fontSize: tokens.fontSizeBase200, fontWeight: tokens.fontWeightRegular }}>加载帧详情…</span>
                )}
                {/* 流跟随: 选中树节点 f 串为 <proto>.stream == N 时可用 */}
                <Tooltip content={followStreamMatch !== null ? `跟随流: ${followStreamMatch.filter}` : "选中树节点含 .stream 过滤串(如 tcp.stream == 0)后可用"} relationship="label">
                  <Button size="small" appearance="subtle" icon={<Chat16Regular />} disabled={followStreamMatch === null}
                    onClick={() => { if (followStreamMatch !== null) setFollowStream(followStreamMatch); }}>
                    跟随流
                  </Button>
                </Tooltip>
              </span>
            </div>
            {parseWindow ? (
              <PaneHint text={waitLabel} />
            ) : selRow === null ? (
              <div style={{ padding: tokens.spacingVerticalS, color: tokens.colorNeutralForeground3, fontSize: tokens.fontSizeBase200 }}>
                点击上方列表行查看报文详情
              </div>
            ) : (
              // 选中帧: 面板常挂, 在途旧内容降透明(data-loading 标记), 新详情原地替换
              <div className={mergeClasses(styles.paneBody, frameLoading && styles.dimmed)} data-loading={frameLoading || undefined}>
                {detail?.tree ? (
                  <CaptureTreeView tree={detail.tree} selected={selNode} onNodeSelect={setSelNode} onFilter={onNodeFilter} />
                ) : (
                  <div style={{ padding: tokens.spacingVerticalS, color: tokens.colorNeutralForeground3, fontSize: tokens.fontSizeBase200 }}>
                    {frameErr === "" ? "解析中…" : ""}
                  </div>
                )}
              </div>
            )}
          </div>
          <SplitDivider axis="h" drag={drag} setDrag={setDrag} onMove={onDragMove}
            onReset={() => setHexW(420)} title="拖动调整字节视图宽度, 双击复位" />
          <div className={styles.hexPane} style={{ width: hexW }}>
            <div className={styles.paneHead}>
              <TabList size="small" className={styles.hexTabs} aria-label="字节显示模式" selectedValue={hexMode}
                onTabSelect={(_, d) => setHexMode((d.value as HexMode) ?? "hex")}>
                <Tab value="hex">十六进制</Tab>
                <Tab value="bin">二进制</Tab>
                <Tab value="ascii">文本</Tab>
              </TabList>
              <span style={{ flex: 1 }} />
              {selNode?.h && <span className={monoS.mono} style={{ color: tokens.colorNeutralForeground3, fontSize: tokens.fontSizeBase200 }}>选区 [{selNode.h[0]}, {selNode.h[1]}]</span>}
              {selRow && !parseWindow && <span className={monoS.mono} style={{ color: tokens.colorNeutralForeground3, fontSize: tokens.fontSizeBase200 }}>帧 {selRow.no}</span>}
            </div>
            {parseWindow ? (
              <PaneHint text={waitLabel} />
            ) : (
              <div className={mergeClasses(styles.paneBody, frameLoading && styles.dimmed)} data-loading={frameLoading || undefined}>
                <CaptureHexView bytesB64={detail?.bytes} selNode={selNode} mode={hexMode} />
              </div>
            )}
          </div>
        </div>
      </div>

      {/* 流跟随对话视图(抽屉覆层) */}
      {followStream !== null && (
        <FollowStreamView type={followStream.type} filter={followStream.filter}
          fetchFollow={fetchFollow} onClose={() => setFollowStream(null)} />
      )}

      {/* 专家信息汇总(抽屉覆层) */}
      {expertOpen && (
        <ExpertPanel fetchExpert={fetchExpert} onFrameSelect={onExpertFrame} onClose={() => setExpertOpen(false)} />
      )}
    </div>
  );
}
