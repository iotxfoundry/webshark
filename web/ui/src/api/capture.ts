// capture.ts - 抓包页面 HTTP API(docs/api.md §2 对接面):
// 流句柄 handle(前端生成, 幂等 subscribe 建流) + subscribe 建流 + 交互查询全家
// (frame 详情按复合键 / check 校验 / complete 补全 / follow 流跟随 / tap 专家信息) +
// pcapng 导出下载 + status 探活. WS 上行只剩 _ctl 流控制, 交互查询全部走 HTTP.

import { withBase } from "../base";
import { getJSON, postJSON } from "./client";
import { tokenQuery } from "./token";
import type {
  SharkdCheckWarning, SharkdCompletion, SharkdExpertResponse,
  SharkdFollowResult, SharkdFrameDetail,
} from "./sharkdTypes";

/** 流句柄(页面会话内稳定): 客户端生成, subscribe 幂等 getOrCreate, 断线重连复用同一句柄
 *  (服务端宽限 60s 内追帧位置随流保留; 超宽限服务端重建, 前端无感走 reset 全量对齐) */
let streamHandle = "";
export function getStreamHandle(): string {
  if (streamHandle === "") streamHandle = `st-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
  return streamHandle;
}

/** subscribe 应答(服务端幂等 getOrCreate 后回读流状态; cold=true = 旧会话已被淘汰重建,
 *  锚定最新分片不重放历史, 前端须 reset 清空聚合器对齐, 否则静默跳段) */
export interface CaptureSubscribeResult {
  ok: boolean;
  handle: string;
  paused: boolean;
  filter: string;
  shard: string;
  /** true = 服务端新建了会话(原 handle 不存在/已被淘汰), false = 复用 */
  cold: boolean;
}

/** GET /api/status: sharkd 可达性 + spool 分片清单(顶栏状态展示用) */
export interface CaptureStatus {
  connected: boolean;
  files: { name: string; size: number; mtime: string }[];
  reason?: string;
}

export async function fetchCaptureStatus(): Promise<CaptureStatus> {
  return await getJSON<CaptureStatus>("/api/status");
}

/** POST /api/subscribe {handle}: 建流/保活; 重连前先 subscribe 一次确保会话在 */
export async function subscribeCaptureStream(handle: string): Promise<CaptureSubscribeResult> {
  return await postJSON<CaptureSubscribeResult>("/api/subscribe", { handle });
}

/** GET /api/frame?handle=&shard=&num=: 单帧详情按复合键(ref)路由;
 *  404 = 行属历史/已滚动分片(分片缓存实例按需 load 后可查) */
export async function fetchCaptureFrame(shard: string, num: number): Promise<SharkdFrameDetail> {
  // proto/bytes: sharkd frame 裸 num 只回瘦响应, 布尔字面量标志请求协议树/字节
  const q = new URLSearchParams({
    handle: getStreamHandle(),
    shard,
    num: String(num),
    proto: "true",
    bytes: "true",
  });
  return await getJSON<SharkdFrameDetail>(`/api/frame?${q.toString()}`);
}

/** POST /api/check {filter}: dfilter 编译校验(轻查询实例);
 *  envelope 错误 400 透传 sharkd 原文案(校验横幅 invalid 路径) */
export async function checkCaptureFilter(filter: string): Promise<SharkdCheckWarning> {
  return await postJSON<SharkdCheckWarning>("/api/check", { filter });
}

/** POST /api/complete {prefix}: 补全(field 在前 pref 在后合并, 描述键 d 回退 n) */
export async function completeCaptureFilter(prefix: string): Promise<SharkdCompletion[]> {
  const r = await postJSON<{ field?: SharkdCompletion[]; pref?: SharkdCompletion[] }>(
    "/api/complete", { prefix });
  const norm = (items?: SharkdCompletion[]): SharkdCompletion[] =>
    (items ?? []).map((it) => ({ ...it, d: it.d ?? it.n ?? "" }));
  // field 与 pref 对同一前缀可能返回同名项(实测 prefix=icmp 两分支均含 icmp): 按 f 去重, 保留首次出现
  const merged = [...norm(r?.field), ...norm(r?.pref)];
  const seen = new Set<string>();
  return merged.filter((it) => !seen.has(it.f) && seen.add(it.f));
}

/** POST /api/follow {handle, follow, filter}: 流跟随重组; 空流字段缺省归一补齐 */
export async function followCaptureStream(type: string, filter: string): Promise<SharkdFollowResult> {
  const r = await postJSON<Partial<SharkdFollowResult> | null>("/api/follow",
    { handle: getStreamHandle(), follow: type, filter });
  return {
    shost: r?.shost ?? "", sport: r?.sport ?? "", sbytes: r?.sbytes ?? 0,
    chost: r?.chost ?? "", cport: r?.cport ?? "", cbytes: r?.cbytes ?? 0,
    payloads: r?.payloads ?? [],
  };
}

/** POST /api/tap {handle, tap0: "expert"}: 专家信息汇总(响应透传; sharkd 参数键为 tap0 不是 tap) */
export async function tapCaptureExpert(): Promise<SharkdExpertResponse> {
  return await postJSON<SharkdExpertResponse>("/api/tap", { handle: getStreamHandle(), tap0: "expert" });
}

/** 导出 spool 全部分片为合并单一 pcapng: a.download 直连 GET /api/download?token=;
 *  服务端按 mtime 升序流式拼接. 401 由浏览器导航处理(直链无法拦响应体, 录入页兜底). */
export async function downloadCaptureAll(): Promise<void> {
  const auth = tokenQuery();
  const a = document.createElement("a");
  a.href = withBase(`/api/download${auth !== "" ? `?${auth}` : ""}`);
  a.download = "webshark.pcapng";
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
}

