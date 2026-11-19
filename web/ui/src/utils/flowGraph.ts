// flowGraph.ts - 流图布局构建(纯函数, 无图库): 从包列表行 c[](8 列契约, 见
// captureColumns)提取 endpoint 去重成 lane 节点与流向条目; 取末尾 FLOW_LIMIT 窗口
// (参考实现同量级上限, 超长流不冻结渲染). ref 复合键透传, 点击行回查帧详情.
import type { CaptureViewRow } from "../hooks/captureAggregator";
import { COL } from "./captureColumns";

/** 流图最大绘制行数(取窗口末尾, 与参考实现一致) */
export const FLOW_LIMIT = 500;

export interface FlowItem {
  /** 时间显示串: 绝对日期时间(本地时区)截取时分秒, 短串原样 */
  t: string;
  /** [源节点下标, 目的节点下标] */
  n: [number, number];
  /** 协议 · Info(Info 空则仅协议) */
  c: string;
  /** 聚合行号(列表选中高亮用) */
  no: number;
  /** 复合查询键(点击回查帧详情) */
  ref: CaptureViewRow["ref"];
}

export function buildFlow(rows: CaptureViewRow[], limit = FLOW_LIMIT): { nodes: string[]; flows: FlowItem[] } {
  const nodes: string[] = [];
  const idx = (name: string): number => {
    const i = nodes.indexOf(name);
    if (i >= 0) return i;
    nodes.push(name);
    return nodes.length - 1;
  };
  const flows: FlowItem[] = [];
  for (const row of rows.slice(-limit)) {
    const c = row.c;
    const src = c[COL.src] ?? "";
    const dst = c[COL.dst] ?? "";
    if (src === "" || dst === "" || src === dst) continue; // 无向行/同端点不产流
    const abs = c[COL.abs] ?? "";
    const proto = c[COL.proto] ?? "";
    const info = c[COL.info] ?? "";
    flows.push({
      t: abs.length > 10 ? abs.slice(11) : abs,
      n: [idx(src), idx(dst)],
      c: info !== "" ? `${proto} · ${info}` : proto,
      no: row.no,
      ref: row.ref,
    });
  }
  return { nodes, flows };
}
