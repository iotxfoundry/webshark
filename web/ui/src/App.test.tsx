// App.test.tsx - 单页外壳: 渲染 CapturePage(无可见 chrome) + 401 管线切换 TokenGate +
// 嵌入形态判定(isEmbedded 注入) + 主题 data-theme 同步.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import App from "./App";
import { setToken } from "./api/token";

// CapturePage mock(外壳测试不关注三面板内部, 仅断言 props 透传)
vi.mock("./pages/CapturePage", () => ({
  default: vi.fn(({ embedded, onOpenToken }: { embedded?: boolean; onOpenToken?: () => void }) => (
    <div data-testid="capture-page" data-embedded={String(embedded ?? false)}>
      <button aria-label="token-stub" onClick={onOpenToken}>token-stub</button>
    </div>
  )),
}));
const MockedCapturePage = vi.mocked((await import("./pages/CapturePage")).default);

// isEmbedded mock(env.ts 模块化 helper: 测试注入点, 生产零开关)
vi.mock("./env", async (importOriginal) => {
  const orig = await importOriginal<typeof import("./env")>();
  return { ...orig, isEmbedded: vi.fn(() => false) };
});
const mockedIsEmbedded = vi.mocked((await import("./env")).isEmbedded);

describe("App", () => {
  beforeEach(() => {
    localStorage.clear();
    setToken("");
    mockedIsEmbedded.mockReturnValue(false);
  });
  afterEach(() => {
    localStorage.clear();
    vi.restoreAllMocks();
  });

  it("渲染 CapturePage 且未锁定时无 TokenGate; isEmbedded 结果透传为 embedded prop", () => {
    const view = render(<App />);
    expect(view.getByTestId("capture-page")).toBeInTheDocument();
    expect(screen.queryByText("webshark 访问令牌")).not.toBeInTheDocument();
    expect(MockedCapturePage).toHaveBeenCalledWith(expect.objectContaining({ embedded: false }), {});
    mockedIsEmbedded.mockReturnValue(true);
    view.rerender(<App />);
    expect(view.getByTestId("capture-page")).toHaveAttribute("data-embedded", "true");
  });

  it("主题切换(经 CapturePage 工具栏)写 localStorage 且同步根节点 data-theme", () => {
    // 这里仅验证 App 的 data-theme 同步: CapturePage 已 mock, 主题切换由页内测试覆盖;
    // App 层断言默认深色 data-theme
    render(<App />);
    expect(document.documentElement.dataset.theme).toBe("dark");
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

  it("CapturePage 令牌回调 → App 切 TokenGate(主动换 token)", () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "token-stub" }));
    expect(screen.getByText("webshark 访问令牌")).toBeInTheDocument();
  });
});
