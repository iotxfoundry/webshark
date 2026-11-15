// CaptureTreeView.tsx - 协议树详情(懒展开): flattenTree 行投影 + react-virtuoso 虚拟滚动.
// 页头由 CapturePage treePane paneHead 唯一承担(修复前组件内部自渲染头与页头叠加成两个头).
// 交互: 点行 → onNodeSelect(hex 高亮该节点 h/i 区间); 点"作为过滤器"钮 → onFilter 应用节点 f 串.
// 展开态: 以稳定路径键(父链 f 串+兄弟下标, 见 captureTree)保留——换帧节点对象全新但同字段
// 路径键不变, 展开态跨帧继承不全塌; 每棵新树顶层协议级默认展开(同 Wireshark).
// 测试环境(jsdom 无布局)回退为普通滚动列表(isTestEnv): react-virtuoso 依赖真实视口测量, jsdom 首帧渲染不出行.
import { useCallback, useMemo, useState } from "react";
import { Virtuoso } from "react-virtuoso";
import { Button, makeStyles, mergeClasses, tokens, Tooltip } from "@fluentui/react-components";
import { Filter16Regular, ChevronRight16Regular, ChevronDown16Regular } from "@fluentui/react-icons";
import { flattenTree, topLevelKeys, isTestEnv, type TreeRow } from "../../utils/captureTree";
import type { SharkdTreeNode } from "../../api/sharkdTypes";
import { useMonoStyles } from "../../styles/mono";

const useStyles = makeStyles({
  root: { display: "flex", flexDirection: "column", minHeight: "0", flex: "1" },
  row: { display: "flex", alignItems: "baseline", gap: "4px", padding: `1px ${tokens.spacingHorizontalS}`, fontSize: tokens.fontSizeBase200, cursor: "default" as const, borderBottom: `1px solid ${tokens.colorNeutralStroke2}` },
  rowHover: { ":hover": { backgroundColor: tokens.colorNeutralBackground1Hover } },
  rowSel: { backgroundColor: tokens.colorNeutralBackground1Selected },
  caret: { flexShrink: 0, width: "16px", display: "inline-flex", justifyContent: "center", cursor: "pointer" as const, color: tokens.colorNeutralForeground3 },
  label: { flex: 1, overflowWrap: "anywhere" as const },
  filterBtn: { flexShrink: 0, opacity: 0, transition: "opacity .15s" },
  rowHoverBtn: { "&:hover .treeFilterBtn, &:focus-within .treeFilterBtn": { opacity: 1 } },
  sevError: { color: tokens.colorPaletteRedForeground1 },
  sevWarn: { color: tokens.colorPaletteMarigoldForeground1 },
});

/** 测试环境回退普通滚动列表 */
const virtual = !isTestEnv();

export function CaptureTreeView({ tree, selected, onNodeSelect, onFilter }: {
  tree: SharkdTreeNode[];
  selected?: SharkdTreeNode;
  onNodeSelect: (n: SharkdTreeNode) => void;
  /** 点节点 f 串 → filter 输入插入 */
  onFilter: (f: string) => void;
}) {
  const styles = useStyles();
  const monoS = useMonoStyles();
  // 懒展开: 展开态以稳定路径键保留(换帧对象全新, 同字段路径键不变 → 跨帧继承不全塌);
  // 每棵新树顶层协议级默认展开(同 Wireshark): 状态只存用户手工折叠键, 有效展开集 =
  // (顶层键 ∪ 手工展开键) − 手工折叠键, 渲染期 useMemo 派生, 无 effect setState 级联.
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set());
  const [manuallyOpened, setManuallyOpened] = useState<Set<string>>(new Set());
  const toggle = useCallback((key: string) => {
    const tops = topLevelKeys(tree);
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
    setManuallyOpened((prev) => {
      if (tops.includes(key)) return prev; // 顶层键默认展开, 不入手工集
      const next = new Set(prev);
      next.add(key);
      return next;
    });
  }, [tree]);
  const tops = useMemo(() => topLevelKeys(tree), [tree]);
  const rows = useMemo(() => {
    const expanded = new Set([...tops, ...manuallyOpened]);
    for (const k of collapsed) expanded.delete(k);
    return flattenTree(tree, expanded);
  }, [tree, collapsed, tops, manuallyOpened]);

  const renderRow = useCallback((row: TreeRow) => {
    const n = row.node;
    const sev = n.s;
    const isOpen = !collapsed.has(row.key);
    return (
      <div
        key={row.key}
        className={mergeClasses(styles.row, styles.rowHover, styles.rowHoverBtn, n === selected && styles.rowSel)}
        style={{ paddingLeft: `${8 + row.depth * 16}px` }}
        onClick={() => onNodeSelect(n)}
        role="button"
        tabIndex={0}
        onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") onNodeSelect(n); }}
      >
        <span className={styles.caret} onClick={(e) => { e.stopPropagation(); if (row.hasChildren) toggle(row.key); }}>
          {row.hasChildren ? (isOpen ? <ChevronDown16Regular /> : <ChevronRight16Regular />) : null}
        </span>
        <span className={mergeClasses(styles.label, monoS.mono, sev && (sev.toLowerCase().includes("error") ? styles.sevError : sev.toLowerCase().includes("warn") ? styles.sevWarn : undefined))}>
          {n.l}
        </span>
        {n.f && (
          <Tooltip content={`作为过滤器: ${n.f}`} relationship="label">
            <Button
              size="small" appearance="subtle" className="treeFilterBtn" icon={<Filter16Regular />}
              aria-label={`作为过滤器 ${n.f}`}
              onClick={(e) => { e.stopPropagation(); onFilter(n.f!); }}
            />
          </Tooltip>
        )}
      </div>
    );
  }, [styles, monoS, selected, collapsed, toggle, onNodeSelect, onFilter]);

  return (
    <div className={styles.root}>
      {virtual ? (
        <Virtuoso
          style={{ flex: 1 }}
          totalCount={rows.length}
          itemContent={(i) => renderRow(rows[i])}
        />
      ) : (
        <div style={{ flex: 1, overflowY: "auto" }}>
          {rows.map((r) => renderRow(r))}
        </div>
      )}
    </div>
  );
}
