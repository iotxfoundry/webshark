// token.ts - 访问令牌存储(webshark): token 存 localStorage, 空 token = 服务端未配置鉴权
// (请求直通). 所有 fetch 带 Authorization: Bearer; WS 与下载直链带 ?token=.
// 任何 401 → clearToken() 并由 App 切换到 token 录入页.

const TOKEN_KEY = "webshark-token";

/** 当前 token(未配置时为空串) */
export function getToken(): string {
  try {
    return localStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

export function setToken(token: string): void {
  try {
    if (token === "") localStorage.removeItem(TOKEN_KEY);
    else localStorage.setItem(TOKEN_KEY, token);
  } catch { /* 存储禁用/写满: 内存态已生效, 忽略 */ }
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
