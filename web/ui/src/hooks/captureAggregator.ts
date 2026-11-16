// captureAggregator.ts - 跨分片增量聚合器(纯函数化): rows 是唯一视图真相(普通/筛选两模式
// 同一消费路径). 显示 No 与查询键分离: no = 聚合器自增序号(跨分片连续递增, 只增不重排, 上限
// 截断不影响已发序号, 筛选结果跳号保留原聚合序号); ref(分片名+原始帧号)为行内隐藏查询键
// (点行查详情用, 用户无感). 上限 10 万条, 达上限一次性截断最老一半(键随截断淘汰, 低频稳定,
// 不做逐条淘汰——虚拟滚动头部删行会跳滚动位置).

import type { CaptureRowWire } from "../api/captureStream";

export type { CaptureRowWire };

/** 聚合视图行: 原始字段透传 + 显示序号 no + 隐藏查询键 ref */
export interface CaptureViewRow extends CaptureRowWire {
  no: number;
}

/** 聚合上限(条): ≈20MB@10 万行内存视图 */
export const CAPTURE_ROW_CAP = 100_000;

export interface CaptureAggregator {
  /** 追加微批: ref 去重(无重) + no 自增分配; batchShard 供行缺 ref 时回退拼键 */
  append(rows: CaptureRowWire[], batchShard: string): void;
  /** 清空(reset 事件后服务端全量重推对齐): 编号从 1 重启 */
  reset(): void;
  /** 当前视图(追加后返回新数组, 供 React 渲染) */
  view(): CaptureViewRow[];
  size(): number;
}

export function createCaptureAggregator(cap: number = CAPTURE_ROW_CAP): CaptureAggregator {
  let list: CaptureViewRow[] = [];
  const seen = new Set<string>();
  let nextNo = 1;
  const key = (ref: { shard: string; num: number }) => `${ref.shard}\n${ref.num}`;
  return {
    append(rows: CaptureRowWire[], batchShard: string) {
      const fresh: CaptureViewRow[] = [];
      for (const r of rows) {
        const ref = r.ref ?? { shard: batchShard, num: r.num };
        const k = key(ref);
        if (seen.has(k)) continue; // 重放边界重叠去重(无重)
        seen.add(k);
        fresh.push({ ...r, ref, no: nextNo++ });
      }
      if (fresh.length > 0) list = [...list, ...fresh];
      if (list.length >= cap) {
        // 达上限一次性截断最老一半; 键同步淘汰(截断后陈行重推可重新入列, 不复活旧 no)
        const half = Math.floor(cap / 2);
        for (const d of list.slice(0, half)) seen.delete(key(d.ref));
        list = list.slice(half);
      }
    },
    reset() {
      list = [];
      seen.clear();
      nextNo = 1;
    },
    view() {
      return list;
    },
    size() {
      return list.length;
    },
  };
}
