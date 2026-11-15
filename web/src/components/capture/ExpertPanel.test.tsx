// ExpertPanel.test.tsx - Expert Information 汇总: 打开即取数 / 头部按级别分组计数徽章
// (Error 红/Warning 黄/Chat 灰/其余中性) / 明细行渲染(级别|分组|协议|消息|帧号) / 行点击
// onFrameSelect 帧号回调 / 空明细友好文案 / 取数失败文案 / 关闭回调.
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { ExpertPanel } from "./ExpertPanel";
import type { SharkdExpertResponse } from "../../api/sharkdTypes";

const sample: SharkdExpertResponse = {
  taps: [{
    tap: "expert", type: "expert",
    details: [
      { f: 12, s: "Error", g: "Sequence", m: "dup ack", p: "TCP" },
      { f: 13, s: "Warning", g: "Sequence", m: "retransmission", p: "TCP" },
      { f: 14, s: "Warning", g: "Checksum", m: "bad checksum", p: "IP" },
      { f: 15, s: "Chat", g: "Sequence", m: "keep-alive", p: "TCP" },
      { f: 16, s: "Note", g: "Protocol", m: "out of order", p: "TCP" },
    ],
  }],
};

function renderPanel(fetchExpert = vi.fn().mockResolvedValue(sample), onFrameSelect = vi.fn(), onClose = vi.fn()) {
  render(
    <FluentProvider theme={webDarkTheme}>
      <ExpertPanel fetchExpert={fetchExpert} onFrameSelect={onFrameSelect} onClose={onClose} />
    </FluentProvider>,
  );
  return { onFrameSelect, onClose };
}

describe("ExpertPanel", () => {
  afterEach(() => cleanup());

  it("打开即取数: 头部级别计数徽章(Error×1/Warning×2/Chat×1/其余归中性)与明细行", async () => {
    const fetchExpert = vi.fn().mockResolvedValue(sample);
    renderPanel(fetchExpert);
    expect(fetchExpert).toHaveBeenCalled();
    expect(await screen.findByText("Error 1")).toBeInTheDocument();
    expect(screen.getByText("Warning 2")).toBeInTheDocument();
    expect(screen.getByText("Chat 1")).toBeInTheDocument();
    expect(screen.getByText("Note 1")).toBeInTheDocument();
    // 明细行: 消息 + 帧号 + 协议 + 分组
    expect(screen.getByText("retransmission")).toBeInTheDocument();
    expect(screen.getByText("13")).toBeInTheDocument();
    expect(screen.getByText("Checksum")).toBeInTheDocument();
    expect(screen.getByText("IP")).toBeInTheDocument();
  });

  it("行点击 → onFrameSelect(帧号)", async () => {
    const { onFrameSelect } = renderPanel();
    fireEvent.click(await screen.findByText("keep-alive"));
    expect(onFrameSelect).toHaveBeenCalledWith(15);
  });

  it("空明细 → 「无专家信息」", async () => {
    renderPanel(vi.fn().mockResolvedValue({ taps: [{ tap: "expert", type: "expert", details: [] }] }));
    expect(await screen.findByText("无专家信息")).toBeInTheDocument();
  });

  it("取数失败 → 错误文案", async () => {
    renderPanel(vi.fn().mockRejectedValue(new Error("boom")));
    expect(await screen.findByText("加载失败: boom")).toBeInTheDocument();
  });

  it("关闭按钮 → onClose", async () => {
    const { onClose } = renderPanel();
    fireEvent.click(await screen.findByRole("button", { name: "关闭专家信息" }));
    expect(onClose).toHaveBeenCalled();
  });
});
