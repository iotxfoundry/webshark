// client.test.ts - {code,message} 错误约定 + Bearer 头 + 401 → 录入页事件管线.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { ApiError, getJSON, postJSON, handleUnauthorized, setUnauthorizedHandler } from "./client";
import { setToken, getToken, clearToken } from "./token";

describe("token", () => {
  beforeEach(() => localStorage.clear());
  afterEach(() => localStorage.clear());

  it("setToken/getToken/clearToken 往返", () => {
    setToken("abc");
    expect(getToken()).toBe("abc");
    clearToken();
    expect(getToken()).toBe("");
  });
});

describe("readJSON 错误约定", () => {
  beforeEach(() => {
    localStorage.clear();
    setToken("t1");
  });
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("401: 清 token + 触发未认证回调 + 抛 ApiError(401)", async () => {
    const onUn = vi.fn();
    setUnauthorizedHandler(onUn);
    vi.stubGlobal("fetch", vi.fn(async () =>
      ({ ok: false, status: 401, json: async () => ({ code: 401, message: "未认证" }) }) as Response));
    let caught: unknown;
    try { await getJSON("/api/status"); } catch (e) { caught = e; }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).code).toBe(401);
    expect(getToken()).toBe(""); // 已清
    expect(onUn).toHaveBeenCalledTimes(1);
  });

  it("非 ok: {code,message} envelope → ApiError(code 优先于 HTTP 状态码)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      ({ ok: false, status: 400, json: async () => ({ code: -5001, message: "dfilter 语法错" }) }) as Response));
    let caught: unknown;
    try { await postJSON("/api/check", { filter: "x" }); } catch (e) { caught = e; }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).code).toBe(-5001);
    expect((caught as ApiError).message).toBe("dfilter 语法错");
  });

  it("非 ok 且非 JSON body: 回退 HTTP 状态码与通用文案", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      ({ ok: false, status: 502, json: async () => { throw new SyntaxError("html"); } }) as unknown as Response));
    let caught: unknown;
    try { await getJSON("/api/status"); } catch (e) { caught = e; }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).code).toBe(502);
  });

  it("成功应答直通 + POST 携 Bearer 与 JSON 头", async () => {
    let capturedInit: RequestInit | undefined;
    vi.stubGlobal("fetch", vi.fn(async (_u: RequestInfo | URL, init?: RequestInit) => {
      capturedInit = init;
      return { ok: true, status: 200, json: async () => ({ ok: true }) } as Response;
    }));
    await expect(postJSON("/api/subscribe", { handle: "h" })).resolves.toEqual({ ok: true });
    const headers = capturedInit?.headers as Record<string, string>;
    expect(headers.Authorization).toBe("Bearer t1");
    expect(headers["Content-Type"]).toBe("application/json");
    expect(capturedInit?.body).toBe(JSON.stringify({ handle: "h" }));
  });

  it("handleUnauthorized: 无回调时不抛错", () => {
    setUnauthorizedHandler(null as unknown as () => void);
    expect(() => handleUnauthorized()).not.toThrow();
  });
});
