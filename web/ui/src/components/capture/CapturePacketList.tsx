// CapturePacketList.tsx - 包列表(虚拟滚动): No./时间/源/目的/协议/长度/Info 七列(8 列投影,
// 契约见 utils/captureColumns). 行类型 = 聚合视图行(CaptureViewRow): No. 列显示聚合器自增序号
// no(跨分片连续, 与分片内原始帧号/查询键 ref 解耦); 其余列按 COL 下标映射 c[](时间列在
// 相对秒/绝对日期时间间切换, 纯前端选择); bg/fg 行染色一律以服务端为准透出(前端零配色逻辑,
// sharkd "%06x" 无 # 前缀 → 消费侧补 #, fg 缺省不覆盖文字色); 点行 → onRowSelect(隐藏查询键
// 路由查详情). !following 时右下角浮动小圆钮(absolute 覆层, 不参与列表布局)恢复跟随.
// 表头右端列设置钮(Popover): 时间格式(abs/rel)与显示列显隐, localStorage 持久化.
// 滚尾(autoscroll 纯前端显示开关): following 态 followOutput=true(恒跟) + following/rows
// 变化即 scrollToIndex 归底(恢复跟随立即回底/新批追加滚新末行; "auto" 的近底判定会让页头
// 点恢复永不满足); 关闭时数据照常追加仅不滚动(Wireshark 同名语义).
// jsdom(测试)回退普通滚动列表(isTestEnv, virtuoso 需真实视口测量, 同 CaptureTreeView).
import { useCallback, useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type Ref, type RefObject } from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { Button, Checkbox, Popover, PopoverSurface, PopoverTrigger, Radio, RadioGroup, Tooltip, makeStyles, mergeClasses, tokens } from "@fluentui/react-components";
import { ArrowDown24Regular, Settings20Regular } from "@fluentui/react-icons";
import type { CaptureViewRow } from "../../hooks/captureAggregator";
import { COL, DISPLAY_COLUMNS, timeIndex, type ColKey, type ColumnDef, type TimeMode } from "../../utils/captureColumns";
import { isTestEnv } from "../../utils/captureTree";
import { useMonoStyles } from "../../styles/mono";

const useStyles = makeStyles({
  floatFollow: { position: "absolute", right: tokens.spacingHorizontalS, bottom: tokens.spacingVerticalS, zIndex: 10, minWidth: "32px", borderRadius: tokens.borderRadiusCircular, boxShadow: tokens.shadow8 },
  root: { display: "flex", flexDirection: "column", minHeight: "0", flex: 1, border: `1px solid ${tokens.colorNeutralStroke2}`, borderRadius: tokens.borderRadiusMedium, overflow: "hidden", background: tokens.colorNeutralBackground1, position: "relative" },
  head: { display: "flex", gap: tokens.spacingHorizontalS, alignItems: "baseline", padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`, fontWeight: tokens.fontWeightSemibold, fontSize: tokens.fontSizeBase200, background: tokens.colorNeutralBackground2, borderBottom: `1px solid ${tokens.colorNeutralStroke1}`, flexShrink: 0 },
  row: {
    display: "flex", gap: tokens.spacingHorizontalS, alignItems: "baseline", padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}`, borderBottom: `1px solid ${tokens.colorNeutralStroke2}`, fontSize: tokens.fontSizeBase200, cursor: "pointer" as const,
    // 键盘可达焦点轮廓(offset 负值内缩, 不撑行高; 对齐全站可点行)
    ":focus-visible": { outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorBrandStroke1, outlineOffset: "-1px" },
  },
  rowHover: { ":hover": { backgroundColor: tokens.colorNeutralBackground1Hover } },
  // 选中态: 非背景通道描边(染色行 inline bg 覆盖 class 背景, 选中标识不能走背景)
  rowSel: {
    backgroundColor: tokens.colorNeutralBackground1Selected,
    outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorBrandStroke1, outlineOffset: "-1px",
  },
  cNo: { flexShrink: 0, width: "52px", textAlign: "right" as const },
  cTime: { flexShrink: 0, width: "96px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" as const },
  cAddr: { flexShrink: 0, width: "140px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" as const },
  cProto: { flexShrink: 0, width: "70px" },
  cLen: { flexShrink: 0, width: "56px", textAlign: "right" as const },
  cInfo: { flex: 1, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" as const },
  // 列宽拖拽把手: 贴列头右缘 5px 命中区(跨列边界), 恒显 1px 中性分隔线(可供性可见),
  // 悬停/键盘聚焦/按下转品牌色(与 eplugins 表格可供性同款), col-resize 光标内建
  colHandle: { position: "absolute", top: 0, bottom: 0, right: 0, width: "5px", marginRight: "-2px", padding: "0", border: "none", background: "none", cursor: "col-resize", touchAction: "none" as const, zIndex: 1,
    ":hover": { backgroundColor: tokens.colorBrandStroke1 }, ":focus-visible": { outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorBrandStroke1 },
    "::after": { content: '""', position: "absolute", top: 0, bottom: 0, right: "2px", width: "1px", backgroundColor: tokens.colorNeutralStroke2 },
    ":hover::after": { backgroundColor: tokens.colorBrandStroke1 },
    ":focus-visible::after": { backgroundColor: tokens.colorBrandStroke1 },
    ":active::after": { backgroundColor: tokens.colorBrandStroke1 } },
  // 表头右端列设置钮 + Popover 面板(时间格式/列显隐)
  settingsBtn: { flexShrink: 0, alignSelf: "center" },
  settingsPanel: { display: "flex", flexDirection: "column", gap: tokens.spacingVerticalS, minWidth: "150px" },
});

/** 固定宽列默认宽度(px), 槽位序 = DISPLAY_COLUMNS 前缀序(no/time/src/dst/proto/len); Info 恒 flex 吸收余宽.
 *  绝对日期时间 26 字符较宽, time 默认 190. */
const COL_DEFAULT_W = [52, 190, 140, 140, 70, 56];
const COL_MIN_W = 36;
const COL_MAX_W = 600;
const COLW_KEY = "webshark.colwidth.v2";
const TIMEMODE_KEY = "webshark.timemode.v1";
const COLVIS_KEY = "webshark.colvis.v1";

/** 可勾选显隐列(No. 恒显) */
const CHECKABLE = DISPLAY_COLUMNS.filter((d) => d.key !== "no");

/** 读取持久化列宽(损坏/越界回退默认); jsdom 亦有 localStorage, 测试可注入 */
function loadColWidths(): number[] {
  try {
    const raw = localStorage.getItem(COLW_KEY);
    if (raw) {
      const arr = JSON.parse(raw) as unknown;
      if (Array.isArray(arr) && arr.length === COL_DEFAULT_W.length &&
          arr.every((w) => typeof w === "number" && w >= COL_MIN_W && w <= COL_MAX_W)) {
        return arr;
      }
    }
  } catch { /* 存储不可用(隐私模式等) */ }
  return [...COL_DEFAULT_W];
}

/** 读取时间格式(非法值回退 abs) */
function loadTimeMode(): TimeMode {
  try {
    const v = localStorage.getItem(TIMEMODE_KEY);
    if (v === "abs" || v === "rel") return v;
  } catch { /* 存储不可用 */ }
  return "abs";
}

/** 读取隐藏列 key 数组(非可勾选项过滤, No. 恒显) */
function loadColVis(): string[] {
  try {
    const raw = localStorage.getItem(COLVIS_KEY);
    if (raw) {
      const arr = JSON.parse(raw) as unknown;
      if (Array.isArray(arr)) {
        return arr.filter((k): k is string => typeof k === "string" && CHECKABLE.some((d) => d.key === k));
      }
    }
  } catch { /* 存储不可用 */ }
  return [];
}

/** key → 列样式 class(按 key 映射, 改列序不再静默错列) */
function cellClass(key: ColKey | "time", styles: ReturnType<typeof useStyles>): string {
  switch (key) {
    case "no": return styles.cNo;
    case "time": return styles.cTime;
    case "src":
    case "dst": return styles.cAddr;
    case "proto": return styles.cProto;
    case "len": return styles.cLen;
    default: return styles.cInfo;
  }
}

/** 固定宽列槽位(COL_DEFAULT_W 下标, info 无槽位 → undefined, 恒 flex) */
function fixedSlot(key: ColKey | "time"): number | undefined {
  const i = DISPLAY_COLUMNS.findIndex((d) => d.key === key);
  return i >= 0 && i < COL_DEFAULT_W.length ? i : undefined;
}

/** 包列表行组件(虚拟滚动 itemContent 亦用) */
export function CapturePacketRow({ row, colCount, colW, visible, timeMode, selected, onSelect, styles, monoS }: {
  row: CaptureViewRow;
  colCount: number;
  /** 固定宽列宽度(px), 与表头同源保证对齐; Info 列恒 flex */
  colW: number[];
  /** 可见列定义(No. 恒显; 时间列按 timeMode 取值) */
  visible: ColumnDef[];
  /** 时间格式: abs=绝对日期时间(c[2]) / rel=相对秒(c[1]) */
  timeMode: TimeMode;
  selected: boolean;
  onSelect: (r: CaptureViewRow) => void;
  styles: ReturnType<typeof useStyles>;
  monoS: ReturnType<typeof useMonoStyles>;
}) {
  // 列投影: No. = 聚合器自增序号 no(跨分片连续); 其余按 COL 契约下标取 c[], 缺列以空串补齐,
  // 第 8 列起(c[8..])并进 Info(服务端恒推 8 列, colCount 仅防御旧数据).
  const c = row.c;
  const ti = timeIndex(timeMode);
  const value = (key: ColKey | "time"): string => {
    switch (key) {
      case "no": return String(row.no);
      case "time": return c[ti] ?? "";
      case "src": return c[COL.src] ?? "";
      case "dst": return c[COL.dst] ?? "";
      case "proto": return c[COL.proto] ?? "";
      case "len": return c[COL.len] ?? "";
      default: return colCount > 7 ? c.slice(COL.info).join(" ") : (c[COL.info] ?? "");
    }
  };
  // 色彩滤镜行染色: sharkd "%06x" 无 # 前缀 → 补 #; fg 缺省不覆盖文字色(降级默认色, 不产生非法 "#inherit")
  const withHash = (v: string) => (v.startsWith("#") ? v : `#${v}`);
  const rowStyle = row.bg ? { backgroundColor: withHash(row.bg), ...(row.fg ? { color: withHash(row.fg) } : {}) } : undefined;
  const info = value("info");
  return (
    <div
      className={mergeClasses(styles.row, styles.rowHover, selected && styles.rowSel)}
      style={rowStyle}
      onClick={() => onSelect(row)}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") onSelect(row); }}
      title={info}
    >
      {visible.map((d) => {
        const v = value(d.key);
        const slot = fixedSlot(d.key);
        return (
          <span key={d.key} className={mergeClasses(cellClass(d.key, styles), monoS.mono)}
            style={slot !== undefined ? { width: colW[slot] } : undefined}
            title={d.key === "src" || d.key === "dst" ? v : undefined}>
            {v}
          </span>
        );
      })}
    </div>
  );
}

export function CapturePacketList({ rows, selectedNum, onSelect, following, onFollowCancel, onFollowResume, virtuosoRef }: {
  rows: CaptureViewRow[];
  /** 选中行(按聚合序号 no 匹配) */
  selectedNum?: number;
  onSelect: (r: CaptureViewRow) => void;
  /** 跟随最新态: true 时 Virtuoso followOutput 恒滚尾 */
  following?: boolean;
  /** 用户上滚(atBottom=false)取消跟随 */
  onFollowCancel?: () => void;
  /** 滚回底部(atBottom=true)恢复跟随 */
  onFollowResume?: () => void;
  /** Virtuoso 句柄外泄(外部视口控制), 可选 */
  virtuosoRef?: RefObject<VirtuosoHandle | null>;
}) {
  const styles = useStyles();
  const monoS = useMonoStyles();
  // 列宽状态(持久化 localStorage); 拖拽经 handle 的 pointer capture 驱动
  const [colW, setColW] = useState<number[]>(loadColWidths);
  // 时间格式/列显隐(纯前端选择, localStorage 持久化, 零重推)
  const [timeMode, setTimeMode] = useState<TimeMode>(loadTimeMode);
  const [hidden, setHidden] = useState<string[]>(loadColVis);
  // 持久化: colW 一变即写(拖拽/键盘/复位统一走此 effect, 处理器内不手动 setItem)
  useEffect(() => {
    try { localStorage.setItem(COLW_KEY, JSON.stringify(colW)); } catch { /* 存储不可用 */ }
  }, [colW]);
  useEffect(() => {
    try { localStorage.setItem(TIMEMODE_KEY, timeMode); } catch { /* 存储不可用 */ }
  }, [timeMode]);
  useEffect(() => {
    try { localStorage.setItem(COLVIS_KEY, JSON.stringify(hidden)); } catch { /* 存储不可用 */ }
  }, [hidden]);
  const visible = DISPLAY_COLUMNS.filter((d) => !hidden.includes(d.key));
  // 拖拽起点快照(渲染提交后同步, 供 pointerdown 取 w0)
  const colWRef = useRef(colW);
  useEffect(() => { colWRef.current = colW; }, [colW]);
  const resizeRef = useRef<{ i: number; x0: number; w0: number } | null>(null);
  const onColResizeStart = useCallback((i: number) => (e: ReactPointerEvent<HTMLSpanElement>) => {
    e.preventDefault();
    e.stopPropagation(); // 不触发行的点选逻辑(表头非行, 防御)
    resizeRef.current = { i, x0: e.clientX, w0: colWRef.current[i] };
    e.currentTarget.setPointerCapture?.(e.pointerId);
  }, []);
  const onColResizeMove = useCallback((e: ReactPointerEvent<HTMLSpanElement>) => {
    const d = resizeRef.current;
    if (!d) return;
    const w = Math.max(COL_MIN_W, Math.min(COL_MAX_W, d.w0 + e.clientX - d.x0));
    setColW((prev) => {
      const next = [...prev];
      next[d.i] = w;
      return next;
    });
  }, []);
  const onColResizeEnd = useCallback((e: ReactPointerEvent<HTMLSpanElement>) => {
    if (!resizeRef.current) return;
    resizeRef.current = null;
    e.currentTarget.releasePointerCapture?.(e.pointerId);
  }, []);
  const onColResizeReset = useCallback((i: number) => {
    setColW((prev) => {
      const next = [...prev];
      next[i] = COL_DEFAULT_W[i];
      return next;
    });
  }, []);

  const colCount = rows[0]?.c.length ?? 8;
  const virtual = !isTestEnv();
  const innerRef = useRef<VirtuosoHandle | null>(null);
  const handle = virtuosoRef ?? innerRef;
  // P1/P2 视口归底: following 态下 following 翻转(恢复跟随, 立即回底不等 append)或 rows
  // 变化(分片重填/追拉完成)即 scrollToIndex 末行; followOutput 用 true 恒跟(auto 的近底
  // 判定让页头点恢复永不满足), 恢复跟随也可经 atBottomStateChange(true)(用户滚到底)触发
  useEffect(() => {
    if (!following || rows.length === 0) return;
    handle.current?.scrollToIndex({ index: rows.length - 1, align: "end" });
  }, [following, rows, handle]);

  // 取消跟随 = 用户真实向上滚动(原生 scroll 方向判定): 底部状态推断(atBottomStateChange
  // false→cancel)无法区分用户上滚与初始填充/程序滚动, 三轮真机翻车(视口冻顶); 程序化
  // scrollToIndex 归底只向下滚, 方向判定永不误取消
  const lastScrollTop = useRef(0);
  const onFollowCancelRef = useRef(onFollowCancel);
  useEffect(() => { onFollowCancelRef.current = onFollowCancel; }, [onFollowCancel]);
  const detachScroller = useRef<(() => void) | null>(null);
  const attachScroller = useCallback((el: HTMLElement | null | Window) => {
    detachScroller.current?.(); // 换绑/卸载先移除旧监听
    detachScroller.current = null;
    if (!el || typeof (el as HTMLElement).scrollTop !== "number") return; // null/Window 卸载回调, 防御
    const sc = el as HTMLElement;
    lastScrollTop.current = sc.scrollTop;
    const onScroll = () => {
      const st = sc.scrollTop;
      // 内容不可滚(空列表/重填塌缩窗口): 一切 scroll 事件都是钳位噪声, 只记位置不判方向
      if (sc.scrollHeight <= sc.clientHeight) { lastScrollTop.current = st; return; }
      if (st < lastScrollTop.current - 4) onFollowCancelRef.current?.();
      lastScrollTop.current = st;
    };
    sc.addEventListener("scroll", onScroll);
    detachScroller.current = () => sc.removeEventListener("scroll", onScroll);
  }, []);

  const renderRow = (i: number) => (
    <CapturePacketRow
      key={rows[i].no}
      row={rows[i]}
      colCount={colCount}
      colW={colW}
      visible={visible}
      timeMode={timeMode}
      selected={rows[i].no === selectedNum}
      onSelect={onSelect}
      styles={styles}
      monoS={monoS}
    />
  );

  return (
    <div className={styles.root}>
      <div className={styles.head}>
        {visible.map((d) => {
          const slot = fixedSlot(d.key);
          return (
            <span key={d.key} className={cellClass(d.key, styles)} style={slot !== undefined ? { width: colW[slot], position: "relative" } : undefined}>
              {d.label}
              {slot !== undefined && (
                <button
                  type="button"
                  aria-label={`调整${d.label}列宽(拖拽, 方向键微调, 双击复位)`}
                  className={styles.colHandle}
                  onPointerDown={onColResizeStart(slot)}
                  onPointerMove={onColResizeMove}
                  onPointerUp={onColResizeEnd}
                  onLostPointerCapture={onColResizeEnd}
                  onDoubleClick={() => onColResizeReset(slot)}
                  onKeyDown={(e) => {
                    const step = e.shiftKey ? 32 : 8;
                    if (e.key === "ArrowLeft") { e.preventDefault(); setColW((prev) => { const n = [...prev]; n[slot] = Math.max(COL_MIN_W, n[slot] - step); return n; }); }
                    if (e.key === "ArrowRight") { e.preventDefault(); setColW((prev) => { const n = [...prev]; n[slot] = Math.min(COL_MAX_W, n[slot] + step); return n; }); }
                  }}
                />
              )}
            </span>
          );
        })}
        {/* 列设置钮: 时间格式(abs/rel) + 各列显隐(No. 恒显), Popover 展示 */}
        <Popover>
          <PopoverTrigger disableButtonEnhancement>
            <Button aria-label="列设置" title="列设置" size="small" appearance="subtle" className={styles.settingsBtn} icon={<Settings20Regular />} />
          </PopoverTrigger>
          <PopoverSurface aria-label="列设置">
            <div className={styles.settingsPanel}>
              <RadioGroup value={timeMode} onChange={(_, data) => setTimeMode(data.value as TimeMode)} aria-label="时间格式">
                <Radio value="abs" label="绝对日期时间" />
                <Radio value="rel" label="相对秒" />
              </RadioGroup>
              {CHECKABLE.map((d) => (
                <Checkbox key={d.key} label={d.label} checked={!hidden.includes(d.key)}
                  onChange={(_, data) => setHidden((prev) => (data.checked ? prev.filter((k) => k !== d.key) : [...prev, d.key]))} />
              ))}
            </div>
          </PopoverSurface>
        </Popover>
      </div>
      <div style={{ flex: 1, minHeight: 0 }}>
        {virtual ? (
          <Virtuoso
            ref={handle as Ref<VirtuosoHandle>}
            style={{ height: "100%" }}
            totalCount={rows.length}
            itemContent={renderRow}
            scrollerRef={attachScroller}
            followOutput={following ? true : false}
            atBottomStateChange={(atBottom) => {
              // 仅 true→恢复(用户手动滚到底自动恢复); false 不再取消——底部状态转换无法
              // 区分用户上滚与初始填充/程序滚动, 取消只由上方原生 scroll 方向判定触发
              if (atBottom) onFollowResume?.();
            }}
          />
        ) : (
          <div style={{ maxHeight: "100%", overflowY: "auto" }}>
            {rows.map((_, i) => renderRow(i))}
          </div>
        )}
      </div>
      {/* 自动跟随浮动钮(定案: 唯一跟随入口): !following 时右下角覆层, 点击恢复跟随; 滚到底亦自动恢复 */}
      {following === false && onFollowResume && (
        <Tooltip content="自动跟随" relationship="label">
          <Button aria-label="自动跟随" className={styles.floatFollow} icon={<ArrowDown24Regular />}
            appearance="primary" onClick={onFollowResume} />
        </Tooltip>
      )}
    </div>
  );
}
