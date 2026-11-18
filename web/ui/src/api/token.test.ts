// token.test.ts - embedded(内存态)与 standalone(localStorage)双轨 + onTokenChange 版本通知.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { getToken, setToken, clearToken, getTokenVersion, onTokenChange } from "./token";

// hoisted 共享 mock 实例: vi.resetModules 后工厂重跑仍返回同一 fn(门控用例动态重导依赖它)
const { isEmbeddedMock } = vi.hoisted(() => ({ isEmbeddedMock: vi.fn(() => false) }));
vi.mock("../env", async (importOriginal) => {
  const orig = await importOriginal<typeof import("../env")>();
  return { ...orig, isEmbedded: isEmbeddedMock };
});
const mockedIsEmbedded = vi.mocked(isEmbeddedMock);

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

describe("awaitingFirstToken 数据面启动门控", () => {
  it("embedded: 非空 set-token 前等待; 空串(清除)继续等待; 置位后不回退; standalone 恒 false", async () => {
    // 门控状态是模块态且只置位一次: 取全新实例测完整生命周期(静态实例被上方用例共享)
    vi.resetModules();
    mockedIsEmbedded.mockReturnValue(true);
    const fresh = await import("./token");
    expect(fresh.awaitingFirstToken()).toBe(true);
    fresh.setToken(""); // 宿主清除(空串): 继续等待
    expect(fresh.awaitingFirstToken()).toBe(true);
    fresh.setToken("host-tk");
    expect(fresh.awaitingFirstToken()).toBe(false);
    fresh.clearToken(); // 401 清除: 不重入门控(恢复流程照旧)
    expect(fresh.awaitingFirstToken()).toBe(false);
    mockedIsEmbedded.mockReturnValue(false);
    expect(fresh.awaitingFirstToken()).toBe(false); // standalone 立即启动
  });
});
