// CaptureFilterBar.test.tsx - filter 条纯函数与校验联动(mock check/complete promise).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, act, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { CaptureFilterBar, completionPrefix, applyCompletion } from "./CaptureFilterBar";
import { useState } from "react";

describe("completionPrefix", () => {
  it("取末尾非空白 token", () => {
    expect(completionPrefix("tcp.")).toBe("tcp.");
    expect(completionPrefix("ip.addr == 192")).toBe("192");
    expect(completionPrefix("tcp.port == ")).toBe(""); // 运算符 token(== )不补字段, 视为无前缀
    expect(completionPrefix("")).toBe("");
    expect(completionPrefix("not (tcp.po")).toBe("tcp.po");
  });
});

describe("applyCompletion", () => {
  it("替换末尾 token 并补尾空格", () => {
    expect(applyCompletion("tcp.po", "tcp.port")).toBe("tcp.port ");
    expect(applyCompletion("ip.addr == ", "tcp")).toBe("ip.addr == tcp ");
  });
});

function Harness({ check, complete, onApply }: {
  check: (f: string) => Promise<{ status?: string; warning?: string; code?: number; message?: string }>;
  complete: (p: string) => Promise<{ f: string; d?: string }[]>;
  onApply?: (v: string) => void;
}) {
  const [v, setV] = useState("");
  return (
    <FluentProvider theme={webDarkTheme}>
      <CaptureFilterBar
        value={v}
        onChange={setV}
        onApply={onApply ?? (() => undefined)}
        check={check}
        complete={complete}
      />
    </FluentProvider>
  );
}

describe("CaptureFilterBar", () => {
  beforeEach(() => vi.useFakeTimers({ shouldAdvanceTime: true }));
  afterEach(() => { vi.useRealTimers(); cleanup(); vi.restoreAllMocks(); });

  it("合法 filter: check 通过后无错误横幅", async () => {
    const check = vi.fn().mockResolvedValue({ status: "OK" });
    const complete = vi.fn().mockResolvedValue([]);
    render(<Harness check={check} complete={complete} />);
    await userEvent.type(screen.getByRole("textbox"), "tcp.port == 80");
    await act(async () => { await vi.advanceTimersByTimeAsync(600); });
    expect(check).toHaveBeenCalledWith("tcp.port == 80");
    expect(screen.queryByText("过滤器无效")).not.toBeInTheDocument();
  });

  it("非法 filter: code 非 0 → 错误横幅透传 sharkd 消息", async () => {
    const check = vi.fn().mockResolvedValue({ code: -5001, message: 'Filter invalid - "mqtt" was unexpected in this context.' });
    const complete = vi.fn().mockResolvedValue([]);
    render(<Harness check={check} complete={complete} />);
    await userEvent.type(screen.getByRole("textbox"), "tcp ===");
    await act(async () => { await vi.advanceTimersByTimeAsync(600); });
    expect(screen.getByText("过滤器无效")).toBeInTheDocument();
    expect(screen.getByText(/unexpected in this context/)).toBeInTheDocument();
  });

  it("合法但带 warning: 无错误横幅(校验通过), 渲染非阻塞 warning 横幅透传 sharkd 告警", async () => {
    const check = vi.fn().mockResolvedValue({ status: "OK", warning: '"&&" might be unexpected' });
    const complete = vi.fn().mockResolvedValue([]);
    render(<Harness check={check} complete={complete} />);
    await userEvent.type(screen.getByRole("textbox"), "tcp.port == 80 &&");
    await act(async () => { await vi.advanceTimersByTimeAsync(600); });
    expect(check).toHaveBeenCalledWith("tcp.port == 80 &&");
    expect(screen.queryByText("过滤器无效")).not.toBeInTheDocument();
    expect(screen.getByText("过滤器告警")).toBeInTheDocument();
    expect(screen.getByText(/might be unexpected/)).toBeInTheDocument();
  });

  it("补全下拉: complete 结果展示, 点选应用替换末尾 token", async () => {
    const check = vi.fn().mockResolvedValue({ status: "OK" });
    const complete = vi.fn().mockResolvedValue([{ f: "tcp.port", d: "TCP port" }, { f: "tcp.stream", d: "TCP stream" }]);
    const applied: string[] = [];
    render(<Harness check={check} complete={complete} onApply={(v) => applied.push(v)} />);
    const input = screen.getByRole("textbox");
    await userEvent.type(input, "tcp.po");
    await act(async () => { await vi.advanceTimersByTimeAsync(400); });
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    expect(screen.getByText("tcp.port")).toBeInTheDocument();
    await userEvent.click(screen.getByText("tcp.port"));
    await waitForInputValue(input, "tcp.port ");
    // Enter 应用
    await userEvent.type(input, "== 80{Enter}");
    expect(applied[applied.length - 1]).toContain("== 80");
  });

  it("下拉框失焦即收起, 重聚焦恢复: 还原初值未聚焦时不弹, 点空白可关掉", async () => {
    const check = vi.fn().mockResolvedValue({ status: "OK" });
    const complete = vi.fn().mockResolvedValue([{ f: "mqtt", d: "MQ Telemetry Transport Protocol" }]);
    render(<Harness check={check} complete={complete} />);
    const input = screen.getByRole("textbox");
    await userEvent.type(input, "mqtt");
    await act(async () => { await vi.advanceTimersByTimeAsync(400); });
    expect(screen.getByRole("listbox")).toBeInTheDocument();
    await userEvent.tab(); // 失焦(点空白同效): 下拉收起
    expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
    await act(async () => { input.focus(); }); // 重聚焦: 候选仍在, 下拉恢复
    expect(screen.getByRole("listbox")).toBeInTheDocument();
  });

  it("校验无效仍出补全: `ip.` 类非法前缀正是补全目标, 不以 check 结果为门槛(对齐 Wireshark)", async () => {
    const check = vi.fn().mockResolvedValue({ code: -5001, message: 'Filter invalid - "." was unexpected in this context.' });
    const complete = vi.fn().mockResolvedValue([{ f: "ip.addr", d: "Source or Destination Address" }]);
    render(<Harness check={check} complete={complete} />);
    const input = screen.getByRole("textbox");
    await userEvent.type(input, "ip.");
    await act(async () => { await vi.advanceTimersByTimeAsync(600); });
    expect(screen.getByText("过滤器无效")).toBeInTheDocument(); // 错误横幅保留(透传 sharkd)
    expect(complete).toHaveBeenCalledWith("ip.");               // 补全照常请求
    expect(screen.getByRole("listbox")).toBeInTheDocument();    // 候选照常展示
    expect(screen.getByText("ip.addr")).toBeInTheDocument();
  });

  it("空 filter: 不发 check 也不发 complete", async () => {
    const check = vi.fn();
    const complete = vi.fn();
    render(<Harness check={check} complete={complete} />);
    await userEvent.type(screen.getByRole("textbox"), " ");
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(check).not.toHaveBeenCalled();
    expect(complete).not.toHaveBeenCalled();
  });
});

/** Fluent Input 受控值断言(jsdom 受控组件更新经 native setter) */
async function waitForInputValue(input: HTMLElement, expected: string): Promise<void> {
  const native = input.querySelector("input") ?? input;
  await import("@testing-library/dom").then(({ waitFor }) =>
    waitFor(() => expect((native as HTMLInputElement).value).toBe(expected)),
  );
}
