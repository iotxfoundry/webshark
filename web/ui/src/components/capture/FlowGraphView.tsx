// FlowGraphView.tsx - 流图(序列图, 参考实现 lane 布局移植, 纯 div 无图库): 左列时间,
// 右侧按 endpoint 均分泳道(lifeline 竖线), 箭头按流向绝对定位 % 宽度(|dst-src| 最小 8%
// 保证可见), 源下标 > 目的下标时箭头朝左; 颜色按流内容稳定 hash 取色. 数据源=内存 rows
// (当前筛选结果, 天然联动 filter 与实时推送), sharkd 零改动; 点击行回选该帧复用详情管线.
// 行数上限 FLOW_LIMIT(末尾窗口, 见 utils/flowGraph), 不虚拟滚动(500 行 div 可承受).
import { useMemo, useState } from "react";
import {
  Badge, Button, Drawer, DrawerBody, DrawerHeader, DrawerHeaderTitle, Text,
  makeStyles, mergeClasses, tokens,
} from "@fluentui/react-components";
import { Dismiss24Regular } from "@fluentui/react-icons";
import type { CaptureViewRow } from "../../hooks/captureAggregator";
import { useMonoStyles } from "../../styles/mono";
import { hintDrawer } from "../../styles/hint";
import { buildFlow, FLOW_LIMIT, type FlowItem } from "../../utils/flowGraph";

const TIME_W = 110;

const useStyles = makeStyles({
  body: { display: "flex", flexDirection: "column", minHeight: 0, padding: 0 },
  headRow: {
    display: "flex", alignItems: "center", gap: tokens.spacingHorizontalS,
    padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}`,
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`, flexShrink: 0,
  },
  list: { flex: 1, overflowY: "auto", minHeight: 0 },
  // 泳道表头: 与行内 lanes 同宽同 marginLeft, 保证 host 名对准 lifeline
  hosts: {
    display: "flex", position: "sticky", top: 0, zIndex: 1,
    marginLeft: `${TIME_W}px`, background: tokens.colorNeutralBackground2,
    borderBottom: `1px solid ${tokens.colorNeutralStroke1}`,
    padding: `${tokens.spacingVerticalXXS} 0`,
  },
  host: {
    flex: 1, textAlign: "center", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
    fontSize: tokens.fontSizeBase200, fontWeight: tokens.fontWeightSemibold, padding: "0 4px",
  },
  row: {
    display: "flex", alignItems: "stretch", cursor: "pointer",
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`,
    ":hover": { backgroundColor: tokens.colorNeutralBackground1Hover },
    ":focus-visible": { outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorBrandStroke1, outlineOffset: "-1px" },
  },
  rowSel: {
    backgroundColor: tokens.colorNeutralBackground1Selected,
    outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorBrandStroke1, outlineOffset: "-1px",
  },
  time: {
    width: `${TIME_W}px`, flexShrink: 0, padding: `${tokens.spacingVerticalXS} ${tokens.spacingHorizontalS}`,
    fontSize: tokens.fontSizeBase100, color: tokens.colorNeutralForeground3,
    overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
  },
  lanes: { flex: 1, position: "relative", minHeight: "26px" },
  vline: {
    position: "absolute", top: 0, bottom: 0, width: "1px",
    backgroundColor: tokens.colorNeutralStroke2, transform: "translateX(-50%)",
  },
  arrowBox: {
    position: "absolute", top: "50%", transform: "translateY(-50%)",
    display: "flex", alignItems: "center", gap: "4px", minWidth: 0,
  },
  arrowLabel: {
    flexShrink: 1, minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap",
    fontSize: tokens.fontSizeBase100,
  },
  arrowLine: { flexShrink: 0, position: "relative", height: "1px", backgroundColor: "currentColor", minWidth: "8px" },
  hint: hintDrawer,
});

/** 流内容 → 稳定色相(djb2 hash 取 360°, 饱和/亮度定值, 明暗主题均可见) */
export function flowColor(key: string): string {
  let h = 5381;
  for (let i = 0; i < key.length; i++) h = ((h << 5) + h + key.charCodeAt(i)) | 0;
  return `hsl(${Math.abs(h) % 360}, 70%, 45%)`;
}

/** 泳道中心 %(index 0..count-1), 与 hosts 表头 flex:1 等分对齐 */
function laneCenter(index: number, count: number): number {
  return ((index + 0.5) / Math.max(1, count)) * 100;
}

/** 箭头几何: left/width(%) + 反向标记; 最小宽 8% 保证单向短流可见 */
function arrowGeom(n: [number, number], count: number): { left: number; width: number; reverse: boolean } {
  const c0 = laneCenter(n[0], count);
  const c1 = laneCenter(n[1], count);
  return { left: Math.min(c0, c1), width: Math.max(Math.abs(c1 - c0), 8), reverse: n[0] > n[1] };
}

function FlowArrow({ item, count }: { item: FlowItem; count: number }) {
  const styles = useStyles();
  const g = arrowGeom(item.n, count);
  const color = flowColor(item.c);
  return (
    <div className={styles.arrowBox}
      style={{ left: `${g.left}%`, width: `${g.width}%`, color, flexDirection: g.reverse ? "row-reverse" : "row" }}>
      <span className={styles.arrowLabel}>{item.c}</span>
      <div className={styles.arrowLine}>
        {/* 箭头尖: 线端三角(反向时朝左) */}
        <span style={{
          position: "absolute", top: "-3px", width: 0, height: 0,
          [g.reverse ? "left" : "right"]: "-1px",
          borderTop: "3px solid transparent", borderBottom: "3px solid transparent",
          [g.reverse ? "borderRight" : "borderLeft"]: "5px solid currentColor",
        }} />
      </div>
    </div>
  );
}

export function FlowGraphView({ rows, onSelect, onClose }: {
  /** 包列表行(当前筛选结果, 实时追加) */
  rows: CaptureViewRow[];
  /** 点击流行 → 回选该帧(复用帧详情管线) */
  onSelect: (r: CaptureViewRow) => void;
  onClose: () => void;
}) {
  const styles = useStyles();
  const monoS = useMonoStyles();
  const [selNo, setSelNo] = useState<number | null>(null);
  const { nodes, flows } = useMemo(() => buildFlow(rows), [rows]);
  // ref 复合键 → 行(点击流行回选原行对象: 分片名+帧号路由不可按 no 反查)
  const rowByRef = useMemo(() => {
    const m = new Map<string, CaptureViewRow>();
    for (const r of rows) m.set(`${r.ref.shard}#${r.ref.num}`, r);
    return m;
  }, [rows]);

  const pick = (f: FlowItem) => {
    setSelNo(f.no);
    const r = rowByRef.get(`${f.ref.shard}#${f.ref.num}`);
    if (r) onSelect(r);
  };

  return (
    <Drawer open onOpenChange={(_, s) => { if (!s.open) onClose(); }} position="end" size="large">
      <DrawerHeader>
        <DrawerHeaderTitle action={
          <Button appearance="subtle" size="small" icon={<Dismiss24Regular />}
            aria-label="关闭流图" title="关闭" onClick={onClose} />
        }>
          流图 <Text size={200} style={{ fontWeight: tokens.fontWeightRegular, marginLeft: 8 }}>当前筛选 · 点击行定位帧</Text>
        </DrawerHeaderTitle>
      </DrawerHeader>
      <DrawerBody className={styles.body}>
        {flows.length === 0 ? (
          <div className={styles.hint}>当前筛选下无可绘制的流（等待数据或调整筛选）</div>
        ) : (
          <>
            <div className={styles.headRow}>
              <Badge appearance="filled" color="informative">{nodes.length} 端点</Badge>
              <Badge appearance="tint" color="informative">{flows.length} 流</Badge>
              {rows.length > FLOW_LIMIT && (
                <Badge appearance="tint" color="warning">显示最近 {FLOW_LIMIT} 帧</Badge>
              )}
            </div>
            <div className={styles.list}>
              {/* 泳道表头(滚动随行, sticky 吸顶): host 名与下方 lifeline 列等分对齐 */}
              <div className={styles.hosts}>
                {nodes.map((h) => (
                  <div key={h} className={styles.host} title={h}>{h}</div>
                ))}
              </div>
              {flows.map((f) => (
                <div key={f.no} role="button" tabIndex={0}
                  className={mergeClasses(styles.row, selNo === f.no && styles.rowSel)}
                  onClick={() => pick(f)}
                  onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") pick(f); }}>
                  <span className={mergeClasses(styles.time, monoS.mono)} title={f.t}>{f.t}</span>
                  <div className={styles.lanes}>
                    {nodes.map((h, i) => (
                      <div key={h} className={styles.vline} style={{ left: `${laneCenter(i, nodes.length)}%` }} />
                    ))}
                    <FlowArrow item={f} count={nodes.length} />
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
      </DrawerBody>
    </Drawer>
  );
}
