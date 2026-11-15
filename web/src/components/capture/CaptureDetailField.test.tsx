// CaptureDetailField 轻量单测: 长值收起/展开 + 复制反馈 + 截断后缀
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import CaptureDetailField, { LONG_VALUE_LIMIT } from "./CaptureDetailField";

const renderField = (props: Parameters<typeof CaptureDetailField>[0]) =>
  render(<FluentProvider theme={webDarkTheme}><CaptureDetailField {...props} /></FluentProvider>);

describe("CaptureDetailField", () => {
  it("短值直接完整显示, 无展开钮", () => {
    renderField({ name: "mq.topic", value: "abc" });
    expect(screen.getByText("abc")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "展开" })).toBeNull();
  });

  it("长值默认收起为前缀预览(含字符数提示), 点击展开完整内容, 再点收起", () => {
    const long = "x".repeat(LONG_VALUE_LIMIT + 50);
    renderField({ name: "http.body", value: long });
    expect(screen.getByText(`${long.slice(0, LONG_VALUE_LIMIT)}…（${long.length} 字符，点击展开）`)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "展开" }));
    // 展开态: 前缀预览消失, 完整值出现(长值 getByText 精确匹配整串)
    expect(screen.queryByText(/点击展开/)).toBeNull();
    expect(screen.getByText(long)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "收起" }));
    expect(screen.getByText(/点击展开/)).toBeInTheDocument();
  });

  it("点击复制写剪贴板并短暂显示已复制(aria 反馈)", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, { clipboard: { writeText } });
    vi.useFakeTimers();
    renderField({ name: "k", value: "v" });
    // Fluent 图标钮无可见文本, aria-label 含图标名拼接, 用首个按钮(行内唯一)断言
    const copyBtn = screen.getAllByRole("button")[0];
    fireEvent.click(copyBtn);
    await vi.advanceTimersByTimeAsync(0);
    expect(writeText).toHaveBeenCalledWith("v");
    expect(copyBtn).toHaveAttribute("aria-label", "已复制");
    await vi.advanceTimersByTimeAsync(1500);
    expect(copyBtn).toHaveAttribute("aria-label", "复制");
    vi.useRealTimers();
  });

  it("truncated 时值尾追加(上游已截断)灰色后缀", () => {
    renderField({ name: "mq.payload", value: "abc", truncated: true });
    expect(screen.getByText("（上游已截断）")).toBeInTheDocument();
  });
});
