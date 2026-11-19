// captureColumns.ts - 包列集契约(与 internal/stream/worker.go framesColumns 对应).
// 服务端 frames 调用显式传 column0..N, 编号 = sharkd COL_* 枚举(4.6.9 实测):
// 32=%m No., 36=%Rt 相对秒, 0=%Yt 绝对日期时间(本地时区), 38=%s 源, 11=%d 目的,
// 35=%p 协议, 34=%L 长度, 25=%i Info. 双时间列恒推送, 时间格式切换/列显隐为
// 纯前端选择(零重推, localStorage 持久化).
export const COL = { no: 0, rel: 1, abs: 2, src: 3, dst: 4, proto: 5, len: 6, info: 7 } as const;
export type ColKey = keyof typeof COL;

/** 显示列定义(No. 恒显; 时间列按 timeMode 取 rel/abs 之一渲染, 共用一个表头位) */
export interface ColumnDef {
  key: ColKey | "time";
  label: string;
}

/** 显示列序(8 列推送, 7 个显示位: 时间位在 rel/abs 间切换) */
export const DISPLAY_COLUMNS: ColumnDef[] = [
  { key: "no", label: "No." },
  { key: "time", label: "时间" },
  { key: "src", label: "源" },
  { key: "dst", label: "目的" },
  { key: "proto", label: "协议" },
  { key: "len", label: "长度" },
  { key: "info", label: "Info" },
];

/** 时间格式: abs=绝对日期时间(默认, c[2]) / rel=相对秒(c[1]) */
export type TimeMode = "abs" | "rel";

/** timeMode → 实际取值的 c 下标 */
export function timeIndex(mode: TimeMode): 1 | 2 {
  return mode === "abs" ? COL.abs : COL.rel;
}
