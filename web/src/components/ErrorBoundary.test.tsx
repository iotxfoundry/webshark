// ErrorBoundary.test.tsx - 渲染期异常兜底: fallback 标题/错误信息/重载按钮.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { ErrorBoundary } from "./ErrorBoundary";

const Boom = () => { throw new Error("boom"); };

function renderBoundary() {
  render(
    <FluentProvider theme={webDarkTheme}>
      <ErrorBoundary>
        <Boom />
      </ErrorBoundary>
    </FluentProvider>,
  );
}

describe("ErrorBoundary", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("子组件渲染抛错 → fallback(标题+错误信息+重新加载按钮)", () => {
    // 抑制 React 对错误边界的 console.error 噪音
    vi.spyOn(console, "error").mockImplementation(() => {});
    const reload = vi.fn();
    Object.defineProperty(window, "location", { value: { ...window.location, reload }, configurable: true });
    renderBoundary();
    expect(screen.getByText("界面渲染出错")).toBeInTheDocument();
    expect(screen.getByText(/boom/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
    expect(reload).toHaveBeenCalled();
  });
});
