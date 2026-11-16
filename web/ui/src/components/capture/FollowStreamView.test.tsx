// FollowStreamView.test.tsx - 流跟随对话视图: 打开即取数(端点+字节数) / 对话行方向交替标注
// (首段 C→S) / payload utf-8 解码含可打印转义回退 / 空流友好文案 / 取数失败文案 / 关闭回调.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { FollowStreamView, decodeFollowPayload } from "./FollowStreamView";
import type { SharkdFollowResult } from "../../api/sharkdTypes";

function renderView(fetchFollow: (type: string, filter: string) => Promise<SharkdFollowResult>, onClose = vi.fn()) {
  render(
    <FluentProvider theme={webDarkTheme}>
      <FollowStreamView type="TCP" filter="tcp.stream == 0" fetchFollow={fetchFollow} onClose={onClose} />
    </FluentProvider>,
  );
  return onClose;
}

describe("decodeFollowPayload", () => {
  it("utf-8 正常解码", () => {
    expect(decodeFollowPayload(btoa("GET / HTTP/1.1"))).toBe("GET / HTTP/1.1");
  });
  it("非法 utf-8 字节 → 可打印转义(\\xNN)", () => {
    // 0xFF 非 utf-8 合法序列
    expect(decodeFollowPayload(btoa(String.fromCharCode(0xff)))).toBe("\\xff");
  });
});

describe("FollowStreamView", () => {
  afterEach(() => cleanup());

  beforeEach(() => {
    // 防未处理 rejection 泄漏到其他用例
    vi.restoreAllMocks();
  });

  it("打开即取数: 渲染两端端点+字节数与对话行(方向按回合交替: 首 C→S 次 S→C)", async () => {
    const fetchFollow = vi.fn().mockResolvedValue({
      shost: "192.168.1.1", sport: "80", sbytes: 1024,
      chost: "192.168.1.2", cport: "53111", cbytes: 512,
      payloads: [{ n: 1, d: btoa("GET /") }, { n: 3, d: btoa("HTTP/1.1 200 OK") }],
    });
    renderView(fetchFollow);
    expect(fetchFollow).toHaveBeenCalledWith("TCP", "tcp.stream == 0");
    expect(await screen.findByText("192.168.1.1:80 (1024 B) ⇄ 192.168.1.2:53111 (512 B)")).toBeInTheDocument();
    expect(screen.getByText("帧 1")).toBeInTheDocument();
    expect(screen.getByText("C→S")).toBeInTheDocument();
    expect(screen.getByText("GET /")).toBeInTheDocument();
    expect(screen.getByText("帧 3")).toBeInTheDocument();
    expect(screen.getByText("S→C")).toBeInTheDocument();
    expect(screen.getByText("HTTP/1.1 200 OK")).toBeInTheDocument();
  });

  it("空流(TLS NONE): payloads 空 → 「该流无数据载荷」友好文案, 不报错", async () => {
    const fetchFollow = vi.fn().mockResolvedValue({
      shost: "NONE", sport: "0", sbytes: 0, chost: "", cport: "", cbytes: 0, payloads: [],
    });
    renderView(fetchFollow);
    expect(await screen.findByText("该流无数据载荷")).toBeInTheDocument();
    expect(screen.queryByText("C→S")).not.toBeInTheDocument();
  });

  it("取数失败 → 错误文案", async () => {
    const fetchFollow = vi.fn().mockRejectedValue(new Error("boom"));
    renderView(fetchFollow);
    expect(await screen.findByText("加载失败: boom")).toBeInTheDocument();
  });

  it("关闭按钮 → onClose", async () => {
    const fetchFollow = vi.fn().mockResolvedValue({
      shost: "a", sport: "1", sbytes: 0, chost: "b", cport: "2", cbytes: 0, payloads: [],
    });
    const onClose = renderView(fetchFollow);
    fireEvent.click(await screen.findByRole("button", { name: "关闭流跟随" }));
    expect(onClose).toHaveBeenCalled();
  });
});
