// App.test.tsx - 单页外壳: 顶栏品牌/状态徽章/主题切换/TokenGate 切换(401 管线).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import App from "./App";
import { setToken } from "./api/token";

vi.mock("./api/capture", async (importOriginal) => {
  const orig = await importOriginal<typeof import("./api/capture")>();
  return { ...orig, fetchCaptureStatus: vi.fn() };
});
const mockedStatus = vi.mocked((await import("./api/capture")).fetchCaptureStatus);

describe("App", () => {
  beforeEach(() => {
    localStorage.clear();
    setToken("");
    mockedStatus.mockResolvedValue({ connected: true, files: [{ name: "a.pcapng", size: 1, mtime: "" }] });
  });
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("顶栏品牌 + sharkd 状态 + 分片数徽章", async () => {
    render(<App />);
    expect(screen.getAllByText("webshark").length).toBeGreaterThanOrEqual(1);
    expect(await screen.findByText("sharkd 在线")).toBeInTheDocument();
    expect(screen.getByText("1 分片")).toBeInTheDocument();
    expect(screen.getAllByText("报文分析").length).toBeGreaterThanOrEqual(1);
  });

  it("status 失败 → sharkd 离线徽章(不崩)", async () => {
    mockedStatus.mockRejectedValue(new Error("boom"));
    render(<App />);
    expect(await screen.findByText("sharkd 离线")).toBeInTheDocument();
  });

  it("主题切换: aria 按钮切换并写 localStorage", async () => {
    render(<App />);
    const btn = screen.getByRole("button", { name: "切换主题" });
    expect(btn.querySelector("svg")).not.toBeNull();
    fireEvent.click(btn);
    expect(localStorage.getItem("webshark-theme")).toBe("light");
    fireEvent.click(btn);
    expect(localStorage.getItem("webshark-theme")).toBe("dark");
  });

  it("401 → TokenGate 录入页; 提交 token 写 localStorage 并重载", async () => {
    const reload = vi.fn();
    Object.defineProperty(window, "location", { value: { ...window.location, reload }, configurable: true });
    const { container } = render(<App />);
    // 直接触发 401 管线(readJSON 内部走 handleUnauthorized)
    const { handleUnauthorized } = await import("./api/client");
    await act(async () => { handleUnauthorized(); });
    expect(screen.getByText("webshark 访问令牌")).toBeInTheDocument();
    const input = screen.getByLabelText("访问令牌");
    fireEvent.change(input, { target: { value: "secret" } });
    fireEvent.click(screen.getByRole("button", { name: "连接" }));
    expect(localStorage.getItem("webshark-token")).toBe("secret");
    expect(reload).toHaveBeenCalled();
    void container;
  });

  it("顶栏令牌按钮 → 录入页(主动换 token)", async () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "访问令牌" }));
    expect(screen.getByText("webshark 访问令牌")).toBeInTheDocument();
  });

  it("隐藏标签页暂停 status 轮询; 恢复可见立即拉取一次", async () => {
    vi.useFakeTimers();
    // 隐藏: 初始 poll + interval 均跳过
    Object.defineProperty(document, "hidden", { configurable: true, get: () => true });
    try {
      render(<App />);
      await act(async () => { vi.advanceTimersByTime(5001); });
      expect(mockedStatus).not.toHaveBeenCalled();
      // 恢复可见 + visibilitychange → 立即 poll 一次
      Object.defineProperty(document, "hidden", { configurable: true, get: () => false });
      await act(async () => { document.dispatchEvent(new Event("visibilitychange")); });
      expect(mockedStatus).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
      delete (document as unknown as { hidden?: boolean }).hidden;
    }
  });
});
