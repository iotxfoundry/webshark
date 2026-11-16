// embedMessaging.test.ts - 宿主注入协议: set-token/set-theme 派发 → store 变化; 未知类型/
// 缺字段忽略; notifyReady/notifyUnauthorized 对 window.parent post(type 消息).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { initEmbedMessaging, notifyReady, notifyUnauthorized } from "./embedMessaging";
import { useThemeStore } from "../stores/theme";
import { getToken } from "../api/token";

// 协议跑在 embedded 语境下(theme 持久化守卫等依赖该判定; jsdom 中 self===top 判定恒 false)
vi.mock("../env", async (importOriginal) => {
  const orig = await importOriginal<typeof import("../env")>();
  return { ...orig, isEmbedded: vi.fn(() => true) };
});
/** window.parent.postMessage 捕获(jsdom 中 parent === self, 整体替换隔离) */
let posted: unknown[] = [];
const realParent = window.parent;

beforeEach(() => {
  posted = [];
  localStorage.clear();
  useThemeStore.setState({ theme: "dark" });
  Object.defineProperty(window, "parent", {
    value: { postMessage: vi.fn((d: unknown) => { posted.push(d); }) },
    configurable: true,
  });
});
afterEach(() => {
  Object.defineProperty(window, "parent", { value: realParent, configurable: true });
  localStorage.clear();
});

function dispatch(data: unknown): void {
  window.dispatchEvent(new MessageEvent("message", { data }));
}

describe("宿主 → iframe", () => {
  it("set-token → 内存令牌生效(getToken 可读)", () => {
    initEmbedMessaging();
    dispatch({ type: "webshark:set-token", token: "tk-1" });
    expect(getToken()).toBe("tk-1");
    dispatch({ type: "webshark:set-token", token: "" });
    expect(getToken()).toBe("");
  });

  it("set-theme → 主题立即切换且不持久化", () => {
    initEmbedMessaging();
    dispatch({ type: "webshark:set-theme", theme: "light" });
    expect(useThemeStore.getState().theme).toBe("light");
    expect(localStorage.getItem("webshark-theme")).toBeNull();
    dispatch({ type: "webshark:set-theme", theme: "dark" });
    expect(useThemeStore.getState().theme).toBe("dark");
  });

  it("未知类型/缺字段/非对象载荷一律忽略(store 不变)", () => {
    initEmbedMessaging();
    dispatch({ type: "webshark:future", x: 1 });
    dispatch({ type: "webshark:set-token" }); // 缺 token
    dispatch({ type: "webshark:set-token", token: 123 }); // token 非串
    dispatch({ type: "webshark:set-theme", theme: "blue" }); // 非法主题值
    dispatch({ type: "webshark:set-theme" }); // 缺 theme
    dispatch("junk-string");
    dispatch(null);
    expect(getToken()).toBe("");
    expect(useThemeStore.getState().theme).toBe("dark");
  });
});

describe("iframe → 宿主", () => {
  it("notifyReady/notifyUnauthorized 对 window.parent postMessage, 数据仅含 type", () => {
    notifyReady();
    notifyUnauthorized();
    expect(posted).toEqual([
      { type: "webshark:ready" },
      { type: "webshark:unauthorized" },
    ]);
  });
});
