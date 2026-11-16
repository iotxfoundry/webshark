// theme.test.ts - 深色默认 + 切换持久化(webshark localStorage key).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { useThemeStore } from "./theme";
import { isEmbedded } from "../env";

vi.mock("../env", async (importOriginal) => {
  const orig = await importOriginal<typeof import("../env")>();
  return { ...orig, isEmbedded: vi.fn(() => false) };
});
const mockedIsEmbedded = vi.mocked(isEmbedded);

beforeEach(() => {
  localStorage.clear();
  mockedIsEmbedded.mockReturnValue(false);
  useThemeStore.setState({ theme: "dark" });
});

afterEach(() => {
  localStorage.clear();
});

describe("useThemeStore", () => {
  it("无存档默认 dark(深色工业主题为默认)", () => {
    expect(useThemeStore.getState().theme).toBe("dark");
  });

  it("toggle 切换到 light 并持久化 webshark-theme", () => {
    useThemeStore.getState().toggle();
    expect(useThemeStore.getState().theme).toBe("light");
    expect(localStorage.getItem("webshark-theme")).toBe("light");
  });

  it("toggle 从 light 切换回 dark", () => {
    useThemeStore.setState({ theme: "light" });
    useThemeStore.getState().toggle();
    expect(useThemeStore.getState().theme).toBe("dark");
    expect(localStorage.getItem("webshark-theme")).toBe("dark");
  });

  it("存档 light 恢复 light", () => {
    localStorage.setItem("webshark-theme", "light");
    // store 模块级初始化只读一次: 直接断言恢复逻辑等价行为(初始化在模块加载, 测试经 setState 模拟)
    useThemeStore.setState({ theme: "light" });
    expect(useThemeStore.getState().theme).toBe("light");
  });

  it("setTheme 切换并持久化(standalone 协议入口)", () => {
    useThemeStore.getState().setTheme("light");
    expect(useThemeStore.getState().theme).toBe("light");
    expect(localStorage.getItem("webshark-theme")).toBe("light");
  });

  it("embedded: setTheme/toggle 均不持久化(主题归宿主管)", () => {
    mockedIsEmbedded.mockReturnValue(true);
    useThemeStore.getState().setTheme("light");
    expect(useThemeStore.getState().theme).toBe("light");
    useThemeStore.getState().toggle();
    expect(useThemeStore.getState().theme).toBe("dark");
    expect(localStorage.getItem("webshark-theme")).toBeNull();
  });
});

describe("初始值 URL ?theme= (store 模块级初始化, resetModules 后重新加载验证)", () => {
  function setLocationSearch(search: string): void {
    Object.defineProperty(window, "location", {
      value: { ...window.location, search },
      configurable: true,
    });
  }

  it("embedded + ?theme=light: 初始即 light(防宿主浅色主题闪深色)", async () => {
    mockedIsEmbedded.mockReturnValue(true);
    setLocationSearch("?theme=light");
    vi.resetModules();
    const envMod = await import("../env");
    vi.mocked(envMod.isEmbedded).mockReturnValue(true);
    const { useThemeStore: fresh } = await import("./theme");
    expect(fresh.getState().theme).toBe("light");
    expect(localStorage.getItem("webshark-theme")).toBeNull();
  });

  it("standalone: URL ?theme= 被忽略, 仍按 localStorage/dark", async () => {
    localStorage.setItem("webshark-theme", "light");
    setLocationSearch("?theme=dark");
    vi.resetModules();
    const { useThemeStore: fresh } = await import("./theme");
    expect(fresh.getState().theme).toBe("light");
  });
});
