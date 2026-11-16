// captureTree.ts - 协议树详情纯逻辑: 树扁平化(懒展开行投影) + 行选择映射回节点.
// 渲染由 CaptureTreeView 组件消费(react-virtuoso 行列表).
import type { SharkdTreeNode } from "../api/sharkdTypes";

/** 扁平行: depth 缩进层级, node 引用, hasChildren 决定展开箭头, key = 稳定路径键 */
export interface TreeRow {
  node: SharkdTreeNode;
  depth: number;
  hasChildren: boolean;
  key: string;
}

/** 稳定路径段: f 值优先(同字段跨帧稳定), 缺省回退 fn(字段缩写), 再回退兄弟下标 */
const seg = (n: SharkdTreeNode, i: number): string => n.f ?? n.fn ?? String(i);

/**
 * flattenTree: 按展开集(expanded, 稳定路径键 Set, 如 "eth/eth.src")投影可见行.
 * 懒展开: 未展开节点不递归子级. 路径键 = 父链各节点段以 "/" 相接(如 "0/eth.dst/0"),
 * 换帧(节点对象全新)后同字段路径键不变, 展开态可跨帧按键保留; 新树中不存在的键自然忽略.
 */
export function flattenTree(roots: SharkdTreeNode[], expanded: Set<string>): TreeRow[] {
  const out: TreeRow[] = [];
  const walk = (nodes: SharkdTreeNode[], depth: number, parent: string) => {
    for (let i = 0; i < nodes.length; i++) {
      const n = nodes[i];
      const hasChildren = (n.n?.length ?? 0) > 0;
      const key = parent === "" ? seg(n, i) : `${parent}/${seg(n, i)}`;
      out.push({ node: n, depth, hasChildren, key });
      if (hasChildren && expanded.has(key)) walk(n.n!, depth + 1, key);
    }
  };
  walk(roots, 0, "");
  return out;
}

/** 顶层可展开节点的路径键(首屏默认展开协议级, 同 Wireshark) */
export function topLevelKeys(roots: SharkdTreeNode[]): string[] {
  const out: string[] = [];
  for (let i = 0; i < roots.length; i++) {
    if ((roots[i].n?.length ?? 0) > 0) out.push(seg(roots[i], i));
  }
  return out;
}

/** 测试环境(jsdom)判定: 虚拟滚动回退普通列表用(virtuoso 需真实视口测量).
 *  函数而非常量: 调用时读 MODE, 保 vi.stubEnv 切环境的测试语义 */
export const isTestEnv = (): boolean => import.meta.env.MODE === "test";
