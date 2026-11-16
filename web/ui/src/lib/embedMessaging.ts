// embedMessaging.ts - iframe 嵌入「宿主注入令牌与主题」postMessage 协议(键名固定):
// 宿主 → iframe: {type:"webshark:set-token", token:string}(空串=清除)
//               {type:"webshark:set-theme", theme:"dark"|"light"}
// iframe → 宿主: {type:"webshark:ready"}(监听器就绪, 宿主此后才发 set-*; 也兼容宿主在
//               iframe onload 后直接发) / {type:"webshark:unauthorized"}(401, 宿主应刷新
//               令牌并重发 set-token)
// 非上述类型/缺字段一律忽略; 不校验 event.origin(恶意宿主最多注入错误令牌导致自身视图
// 401, 无信息泄露面).
// 时序保证: message 监听在模块加载时即注册(bundle 执行早于 iframe load 事件), 早于
// initEmbedMessaging 的消息进入待处理队列, init 时按序重放 —— 宿主在任意时刻发送的
// set-* 都不会丢失(React passive effect 注册存在晚于宿主首条消息的竞态).
import { setToken } from "../api/token";
import { useThemeStore } from "../stores/theme";

/** 宿主上行消息形态(仅协议约定字段, 其余忽略) */
interface HostMessage {
  type?: unknown;
  token?: unknown;
  theme?: unknown;
}

/** init 前到达的消息队列(封顶 32, 防异常场景无界增长) */
const pending: unknown[] = [];
let started = false;

function apply(d: HostMessage): void {
  if (d.type === "webshark:set-token" && typeof d.token === "string") {
    setToken(d.token);
  } else if (d.type === "webshark:set-theme" && (d.theme === "light" || d.theme === "dark")) {
    useThemeStore.getState().setTheme(d.theme);
  }
  // 其余类型/缺字段: 一律忽略(向前兼容)
}

function onMessage(e: MessageEvent): void {
  const d = e.data as HostMessage | null;
  if (d === null || typeof d !== "object") return;
  if (!started) {
    if (pending.length < 32) pending.push(d);
    return; // init 前不应用(协议入口尚未对宿主宣告), init 时按序重放
  }
  apply(d);
}

window.addEventListener("message", onMessage);

/** 宿主上行: 对父窗口发消息 */
function postToHost(msg: { type: string }): void {
  window.parent.postMessage(msg, "*");
}

/** 监听器就绪: 宿主此后才能发 set-* */
export function notifyReady(): void {
  postToHost({ type: "webshark:ready" });
}

/** 401 通知: 宿主应刷新令牌并重发 set-token */
export function notifyUnauthorized(): void {
  postToHost({ type: "webshark:unauthorized" });
}

/** 注册协议入口: 重放 init 前排队的宿主消息并挂载监听, 返回清理函数(可再次 init 重入) */
export function initEmbedMessaging(): () => void {
  started = true;
  for (const d of pending.splice(0)) apply(d as HostMessage);
  window.addEventListener("message", onMessage);
  return () => {
    window.removeEventListener("message", onMessage);
    started = false;
  };
}
