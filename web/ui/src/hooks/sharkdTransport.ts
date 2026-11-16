// sharkdTransport.ts - 抓包 WS 传输管理(纯推送 + _ctl 上行, 会话逻辑解耦便于 mock 测试):
// live 建流: POST /api/subscribe(幂等, Bearer)先行 → WS 握手 ?handle=<流句柄>&token=<token>
// (docs/api.md §2/§3; 未配置鉴权时 tokenQuery 为空). 断线指数退避重连(重连同样先 subscribe
// 确保会话在, 流句柄跨重连复用; 401 由 subscribe 路径抛出→录入页兜底).
// JSON Lines 下行可能一条 WS 消息多行, 逐行分路: 顶层含 type 键 = 推送事件(captureStream
// 契约, 交 onEvent); 其余行(噪声/非推送)丢弃——交互查询已全部走 HTTP, WS 无 JSON-RPC 通道.
import { withBase } from "../base";
import { tokenQuery } from "../api/token";
import { getStreamHandle, subscribeCaptureStream } from "../api/capture";
import { parseCaptureLine, type CaptureWireEvent } from "../api/captureStream";

/** 连接状态: connecting=subscribe/连接中, open=会话可用, closed=退避重连中 */
export type SharkdConn = "connecting" | "open" | "closed";

/** WS 断开后重试上限(ms)与基数 */
const RETRY_MAX_MS = 30_000;
const RETRY_BASE_MS = 1000;

export interface TransportEvents {
  onConn(conn: SharkdConn): void;
  /** 推送事件(hello/rows/rotated/reset/error) */
  onEvent?(e: CaptureWireEvent): void;
  /** 连接就绪后注入上行通道(_ctl 专用; WS 未就绪时 send 抛错, 调用方按 conn 门控) */
  onReady?(send: (line: string) => void): void;
}

/** 建流并连接; 返回清理函数(关 WS + 停重试). subscribe 失败/WS 关闭同走退避重试路径. */
export function openSharkdTransport(ev: TransportEvents): () => void {
  let ws: WebSocket | null = null;
  let cancelled = false;
  let retryTimer: ReturnType<typeof setTimeout> | null = null;
  let failures = 0;

  const retry = () => {
    if (cancelled || retryTimer !== null) return;
    failures += 1;
    const delay = Math.min(RETRY_MAX_MS, RETRY_BASE_MS * 2 ** Math.min(failures - 1, 5));
    retryTimer = setTimeout(() => { retryTimer = null; void open(); }, delay);
  };

  const open = async () => {
    ev.onConn("connecting");
    // 建流先行: subscribe 幂等(重连时确保会话在宽限期内); 失败(网络/429)同断线退避.
    // cold=true = 会话已被淘汰重建(锚定最新分片不重放历史), 本地聚合器旧行作废 →
    // 复用 reset 契约清空对齐(首连聚合器本就为空, 清一次无副作用)
    const handle = getStreamHandle();
    try {
      const r = await subscribeCaptureStream(handle);
      if (r.cold) ev.onEvent?.({ type: "reset", reason: "session-recreated" });
    } catch {
      if (!cancelled) { ev.onConn("closed"); retry(); }
      return;
    }
    if (cancelled) return;
    const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
    // ?handle=<流句柄>&token=<token>(未配置鉴权时仅 handle)
    const auth = tokenQuery();
    const query = `${auth !== "" ? `${auth}&` : ""}handle=${encodeURIComponent(handle)}`;
    ws = new WebSocket(`${proto}//${window.location.host}${withBase("/api/ws")}?${query}`);
    ws.binaryType = "arraybuffer";
    ws.onopen = () => {
      failures = 0;
      ev.onReady?.((line) => {
        if (ws?.readyState === WebSocket.OPEN) ws.send(line);
        else throw new Error("WS 未就绪");
      });
      ev.onConn("open"); // ready 注入后通知(onConn 内可立即上行, 如筛选态重断言)
    };
    ws.onmessage = (e) => {
      if (typeof e.data !== "string") return;
      for (const line of e.data.split("\n")) {
        const msg = parseCaptureLine(line);
        if (msg !== null) ev.onEvent?.(msg);
      }
    };
    ws.onclose = () => {
      ev.onConn("closed");
      retry();
    };
  };
  void open();

  return () => {
    cancelled = true;
    if (retryTimer !== null) clearTimeout(retryTimer);
    ws?.close();
  };
}
