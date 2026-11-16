// token.ts - 访问令牌存储(webshark): standalone 存 localStorage; embedded(iframe 嵌入)走
// 内存态——令牌由宿主经 postMessage 注入, 生命周期归宿主管, 不落盘防宿主登出后残留.
// 空 token = 服务端未配置鉴权(请求直通). 所有 fetch 带 Authorization: Bearer; WS 与下载直链带 ?token=.
// 401 → clearToken(); standalone 由 App 切 token 录入页, embedded 通知宿主刷新令牌.
// 令牌有效变化会递增版本号并通知订阅方(onTokenChange), 供连接层(WS)以新令牌重建.
import { isEmbedded } from "../env";

const TOKEN_KEY = "webshark-token";

/** embedded 模式的内存令牌(不持久化) */
let memToken = "";

/** 令牌版本号: 每次有效变化 +1 */
let version = 0;
const subs = new Set<() => void>();

/** 当前令牌版本(变化即递增), 供连接层作为重建依赖 */
export function getTokenVersion(): number {
  return version;
}

/** 订阅令牌变化, 返回退订函数 */
export function onTokenChange(cb: () => void): () => void {
  subs.add(cb);
  return () => { subs.delete(cb); };
}

function notifyChange(): void {
  version += 1;
  for (const cb of subs) cb();
}

/** 当前 token(未配置时为空串) */
export function getToken(): string {
  if (isEmbedded()) return memToken;
  try {
    return localStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

export function setToken(token: string): void {
  const prev = getToken();
  if (isEmbedded()) {
    memToken = token;
  } else {
    try {
      if (token === "") localStorage.removeItem(TOKEN_KEY);
      else localStorage.setItem(TOKEN_KEY, token);
    } catch { /* 存储禁用/写满: 忽略 */ }
  }
  // 有效态未变(如存储写失败)不通知, 防连接层空转重建
  if (getToken() !== prev) notifyChange();
}

export function clearToken(): void {
  setToken("");
}

/** fetch 用鉴权头(空 token 不带头) */
export function authHeaders(): Record<string, string> {
  const t = getToken();
  return t !== "" ? { Authorization: `Bearer ${t}` } : {};
}

/** WS/下载直链用查询串(空 token 返回空串, 调用方不拼 ?) */
export function tokenQuery(): string {
  const t = getToken();
  return t !== "" ? `token=${encodeURIComponent(t)}` : "";
}
