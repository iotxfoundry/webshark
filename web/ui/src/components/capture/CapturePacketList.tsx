// CapturePacketList.tsx - 包列表(虚拟滚动): No./时间/源/目的/协议/长度/Info 七列.
// 行类型 = 聚合视图行(CaptureViewRow): No. 列显示聚合器自增序号 no(跨分片连续, 与分片内
// 原始帧号/查询键 ref 解耦); 其余列直接映射 c[](顺序 = sharkd status.columns);
// bg/fg 行染色一律以服务端为准透出(前端零配色逻辑, sharkd "%06x" 无 # 前缀 → 消费侧补 #,
// fg 缺省不覆盖文字色); 点行 → onRowSelect(隐藏查询键路由查详情). !following 时右下角浮动
// 小圆钮(absolute 覆层, 不参与列表布局)恢复跟随.
// 滚尾(autoscroll 纯前端显示开关): following 态 followOutput=true(恒跟) + following/rows
// 变化即 scrollToIndex 归底(恢复跟随立即回底/新批追加滚新末行; "auto" 的近底判定会让页头
// 点恢复永不满足); 关闭时数据照常追加仅不滚动(Wireshark 同名语义).
// jsdom(测试)回退普通滚动列表(isTestEnv, virtuoso 需真实视口测量, 同 CaptureTreeView).
import { useCallback, useEffect, useRef, type Ref, type RefObject } from "react";
import { Virtuoso, type VirtuosoHandle } from "react-virtuoso";
import { Button, Tooltip, makeStyles, mergeClasses, tokens } from "@fluentui/react-components";
import { ArrowDown24Regular } from "@fluentui/react-icons";
import type { CaptureViewRow } from "../../hooks/captureAggregator";
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
});

export const CAPTURE_COLUMNS = ["No.", "时间", "源", "目的", "协议", "长度", "Info"] as const;

/** 列头 className 选择: 按 CAPTURE_COLUMNS 下标绑定(改列名不再静默错列) */
function headClass(i: number, styles: ReturnType<typeof useStyles>): string {
  switch (i) {
    case 0: return styles.cNo;
    case 1: return styles.cTime;
    case 2:
    case 3: return styles.cAddr;
    case 4: return styles.cProto;
    case 5: return styles.cLen;
    default: return styles.cInfo;
  }
}

/** 包列表行组件(虚拟滚动 itemContent 亦用) */
export function CapturePacketRow({ row, colCount, selected, onSelect, styles, monoS }: {
  row: CaptureViewRow;
  colCount: number;
  selected: boolean;
  onSelect: (r: CaptureViewRow) => void;
  styles: ReturnType<typeof useStyles>;
  monoS: ReturnType<typeof useMonoStyles>;
}) {
  // 列投影: No. = 聚合器自增序号 no(跨分片连续); c[] 第 1 列起为时间/源/目的/协议/长度/Info
  // (sharkd 默认列集去 No.), 后续按位置填充, 缺列以空串补齐, 第 7 列起并进 Info.
  const c = row.c;
  const time = c[1] ?? "";
  const src = c[2] ?? "";
  const dst = c[3] ?? "";
  const proto = c[4] ?? "";
  const len = c[5] ?? "";
  const info = colCount > 6 ? c.slice(6).join(" ") : (c[6] ?? "");
  // 色彩滤镜行染色: sharkd "%06x" 无 # 前缀 → 补 #; fg 缺省不覆盖文字色(降级默认色, 不产生非法 "#inherit")
  const withHash = (v: string) => (v.startsWith("#") ? v : `#${v}`);
  const rowStyle = row.bg ? { backgroundColor: withHash(row.bg), ...(row.fg ? { color: withHash(row.fg) } : {}) } : undefined;
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
      <span className={mergeClasses(styles.cNo, monoS.mono)}>{row.no}</span>
      <span className={mergeClasses(styles.cTime, monoS.mono)}>{time}</span>
      <span className={mergeClasses(styles.cAddr, monoS.mono)} title={src}>{src}</span>
      <span className={mergeClasses(styles.cAddr, monoS.mono)} title={dst}>{dst}</span>
      <span className={mergeClasses(styles.cProto, monoS.mono)}>{proto}</span>
      <span className={mergeClasses(styles.cLen, monoS.mono)}>{len}</span>
      <span className={mergeClasses(styles.cInfo, monoS.mono)}>{info}</span>
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
  const colCount = rows[0]?.c.length ?? 7;
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
      selected={rows[i].no === selectedNum}
      onSelect={onSelect}
      styles={styles}
      monoS={monoS}
    />
  );

  return (
    <div className={styles.root}>
      <div className={styles.head}>
        {CAPTURE_COLUMNS.map((h, i) => (
          <span key={h} className={headClass(i, styles)}>{h}</span>
        ))}
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
