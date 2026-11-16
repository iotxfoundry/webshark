// token.test.ts - embedded(内存态)与 standalone(localStorage)双轨 + onTokenChange 版本通知.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { getToken, setToken, clearToken, getTokenVersion, onTokenChange } from "./token";
import { isEmbedded } from "../env";

vi.mock("../env", async (importOriginal) => {
  const orig = await importOriginal<typeof import("../env")>();
  return { ...orig, isEmbedded: vi.fn(() => false) };
});
const mockedIsEmbedded = vi.mocked(isEmbedded);

beforeEach(() => {
  localStorage.clear();
  setToken("");
  mockedIsEmbedded.mockReturnValue(false);
});
afterEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("token 存储(standalone)", () => {
  it("set/get/clear 走 localStorage(现状不变)", () => {
    setToken("abc");
    expect(localStorage.getItem("webshark-token")).toBe("abc");
    expect(getToken()).toBe("abc");
    clearToken();
    expect(localStorage.getItem("webshark-token")).toBeNull();
    expect(getToken()).toBe("");
  });
});

describe("token 存储(embedded)", () => {
  it("set/get 走内存态, 不触 localStorage", () => {
    mockedIsEmbedded.mockReturnValue(true);
    setToken("host-tk");
    expect(getToken()).toBe("host-tk");
    expect(localStorage.getItem("webshark-token")).toBeNull();
    clearToken();
    expect(getToken()).toBe("");
    expect(localStorage.getItem("webshark-token")).toBeNull();
  });

  it("embedded 内存令牌不残留到 standalone(宿主登出后无泄漏)", () => {
    mockedIsEmbedded.mockReturnValue(true);
    setToken("host-tk");
    mockedIsEmbedded.mockReturnValue(false);
    expect(getToken()).toBe("");
  });
});

describe("onTokenChange 版本通知", () => {
  it("有效变化递增版本并通知订阅; 重复写同值不通知; 退订生效", () => {
    const cb = vi.fn();
    const unsub = onTokenChange(cb);
    const v0 = getTokenVersion();
    setToken("a");
    expect(cb).toHaveBeenCalledTimes(1);
    expect(getTokenVersion()).toBe(v0 + 1);
    setToken("a"); // 同值重写: 不通知
    expect(cb).toHaveBeenCalledTimes(1);
    setToken("b");
    expect(getTokenVersion()).toBe(v0 + 2);
    unsub();
    clearToken();
    expect(cb).toHaveBeenCalledTimes(2);
  });
});
