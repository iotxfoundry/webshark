// captureStream.ts - 抓包 WS 推送线格式契约(docs/api.md §3, 前端按此消费, 不得单方变更):
// 下行(服务端→前端, JSON Lines 逐条): hello 握手确认 / rows 微批行数据(fg/bg 服务端色彩规则,
// 行颜色唯一真相) / rotated 分片轮换信息性 / reset 清空聚合器全量对齐 / error 控制指令未生效.
// 上行(前端→服务端): _ctl 流控制(与数据同连接 TCP 天然保序).
// 解析分路: 下行顶层含 type 键 = 推送事件; 其余行(噪声等)由传输层丢弃.
// 交互查询(check/complete/follow/tap/frame)走 HTTP(api/capture.ts), WS 上行仅 _ctl.

import type { SharkdFrameRow } from "./sharkdTypes";

/** rows 微批单行: sharkd frames 行字段透传 + 复合键 ref(分片名+原始帧号, 服务端随行下发) */
export interface CaptureRowWire extends SharkdFrameRow {
  ref: { shard: string; num: number };
}

/** 服务端→前端推送事件 */
export type CaptureWireEvent =
  | { type: "hello"; mode: string }
  | { type: "rows"; mode: string; shard: string; rows: CaptureRowWire[] }
  | { type: "rotated"; shard: string }
  | { type: "reset"; reason: string }
  | { type: "error"; reason: string }; // 控制指令未生效(筛选失败等)

/** 一行下行 JSON 解析: 顶层含 type 键 = 推送事件; 其余(非 JSON 噪声行)→ null */
export function parseCaptureLine(line: string): CaptureWireEvent | null {
  const t = line.trim();
  if (t === "" || !t.startsWith("{")) return null;
  try {
    const m = JSON.parse(t) as { type?: unknown };
    if (typeof m.type !== "string" || m.type === "") return null;
    return m as CaptureWireEvent;
  } catch {
    return null;
  }
}

/** 上行 _ctl 抓包暂停/恢复帧(autoscroll 是纯前端显示开关, 不经上行) */
export function ctlCaptureMsg(state: "pause" | "resume"): string {
  return JSON.stringify({ type: "_ctl", op: "capture", state });
}

/** 上行 _ctl 筛选切换帧: filter 非空进入筛选模式, 空串退出(回普通实时流) */
export function ctlFilterMsg(filter: string): string {
  return JSON.stringify({ type: "_ctl", op: "filter", filter });
}
