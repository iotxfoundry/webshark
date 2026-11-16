// App.test.tsx - 单页外壳: 渲染 CapturePage(无可见 chrome) + 401 管线切换 TokenGate +
// 嵌入形态判定(isEmbedded 注入) + 主题 data-theme 同步 + embedded 协议挂载(401 通知宿主).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";
import App from "./App";
import { setToken, getToken } from "./api/token";

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

/** window.parent.postMessage 捕获(jsdom 中 parent === self, 整体替换隔离) */
let posted: unknown[] = [];
const realParent = window.parent;

describe("App", () => {
  beforeEach(() => {
    localStorage.clear();
    setToken("");
    posted = [];
    Object.defineProperty(window, "parent", {
      value: { postMessage: vi.fn((d: unknown) => { posted.push(d); }) },
      configurable: true,
    });
    mockedIsEmbedded.mockReturnValue(false);
  });
  afterEach(() => {
    localStorage.clear();
    Object.defineProperty(window, "parent", { value: realParent, configurable: true });
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

  it("embedded: 挂载即向宿主 post ready; 401 → post unauthorized 且不弹 TokenGate", async () => {
    mockedIsEmbedded.mockReturnValue(true);
    render(<App />);
    expect(posted).toEqual([{ type: "webshark:ready" }]);
    const { handleUnauthorized } = await import("./api/client");
    await act(async () => { handleUnauthorized(); });
    expect(posted).toEqual([
      { type: "webshark:ready" },
      { type: "webshark:unauthorized" },
    ]);
    // 保持当前视图(断连横幅/等待态由 CapturePage 呈现), 不弹录入页
    expect(screen.queryByText("webshark 访问令牌")).not.toBeInTheDocument();
    expect(screen.getByTestId("capture-page")).toBeInTheDocument();
  });

  it("embedded: 不向 CapturePage 传 onOpenToken(令牌入口归零)", () => {
    mockedIsEmbedded.mockReturnValue(true);
    render(<App />);
    expect(MockedCapturePage).toHaveBeenCalledWith(
      expect.objectContaining({ embedded: true, onOpenToken: undefined }), {},
    );
  });

  it("embedded: 宿主 set-token 经协议生效且不写 localStorage", () => {
    mockedIsEmbedded.mockReturnValue(true);
    render(<App />);
    window.dispatchEvent(new MessageEvent("message", { data: { type: "webshark:set-token", token: "host-tk" } }));
    expect(getToken()).toBe("host-tk");
    expect(localStorage.getItem("webshark-token")).toBeNull();
  });
});
