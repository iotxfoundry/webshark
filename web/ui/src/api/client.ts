// ApiError - webshark 错误约定: 服务端错误应答统一为 {code, message} JSON,
// code 为 sharkd envelope 的 code(透传时)或 HTTP 状态码, message 为可读文案.
// 前端只按此约定解析, 不做多格式回退.

export class ApiError extends Error {
  constructor(
    public code: number,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/** 任意抛出值 → 展示文案(Error 取 message, 其余 String 化; catch 分支统一转文案用) */
export function errMsg(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

/** 401 全局处理: 清 token; App 订阅该事件切到 token 录入页 */
let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: () => void): void {
  onUnauthorized = fn;
}
export function handleUnauthorized(): void {
  onUnauthorized?.();
}

/** 应答通用处理: 401 → 清 token + 全局登出事件; 非 ok 按 {code,message} 约定抛 ApiError */
export async function readJSON<T>(res: Response): Promise<T> {
  if (res.status === 401) {
    clearToken();
    handleUnauthorized();
    throw new ApiError(401, "未认证");
  }
  if (!res.ok) {
    let d: { code?: number; message?: string } | null = null;
    try {
      d = (await res.json()) as { code?: number; message?: string };
    } catch {
      d = null;
    }
    if (d !== null && typeof d.message === "string" && d.message !== "") {
      throw new ApiError(d.code ?? res.status, d.message);
    }
    throw new ApiError(res.status, `请求失败 (${res.status})`);
  }
  return (await res.json()) as T;
}

import { withBase } from "../base";
import { authHeaders, clearToken } from "./token";

/** POST JSON 通用(Bearer) */
export async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(withBase(path), {
    method: "POST",
    headers: { "Content-Type": "application/json", ...authHeaders() },
    body: JSON.stringify(body),
  });
  return await readJSON<T>(res);
}

/** GET JSON 通用(Bearer) */
export async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(withBase(path), { headers: authHeaders() });
  return await readJSON<T>(res);
}
