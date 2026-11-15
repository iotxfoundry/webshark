// CaptureHexView.test.tsx - hex 视图渲染: 16 列网格/选区高亮/三态(mode 受控 prop: hex/bin/ascii).
import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { CaptureHexView } from "./CaptureHexView";
import type { SharkdTreeNode } from "../../api/sharkdTypes";

// "Hello" → 48 65 6c 6c 6f
const HELLO_B64 = "SGVsbG8=";

function renderHex(overrides: Partial<Parameters<typeof CaptureHexView>[0]> = {}) {
  return render(
    <FluentProvider theme={webDarkTheme}>
      <CaptureHexView bytesB64={HELLO_B64} mode="hex" {...overrides} />
    </FluentProvider>,
  );
}

describe("CaptureHexView", () => {
  it("hex 模式: 每字节两位 hex + offset 列", () => {
    renderHex({ mode: "hex" });
    expect(screen.getByText("48")).toBeInTheDocument();
    expect(screen.getByText("65")).toBeInTheDocument();
    expect(screen.getByText("6f")).toBeInTheDocument();
    expect(screen.getByText("00000")).toBeInTheDocument(); // offset 0
  });

  it("选区高亮: 选中节点 h=[1,3] → 恰 3 个字节格带选中背景(Griffel 类)", () => {
    const node: SharkdTreeNode = { l: "Len", h: [1, 3] };
    const { container } = renderHex({ selNode: node, mode: "hex" });
    // 选中类是 Griffel 哈希, 以背景色 var 区分: 选中格 span 含 colorPaletteBackground2 着色类.
    // 直接断言: 9 个 span(1 offset + 5 hex + ...)中, 3 个 hex 格有相同"选中"类且其余没有.
    const hexSpans = [...container.querySelectorAll("span")].filter((s) => /^[0-9a-f]{2}$/.test(s.textContent ?? ""));
    expect(hexSpans).toHaveLength(5);
    const selCount = hexSpans.filter((s) => s.className.split(" ").some((c) => c.length > 8 && c === hexSpans[1].className.split(" ").find((x) => x.length > 8 && !hexSpans[0].className.includes(x)))).length;
    // 更直接: 对比第 2/3/4 格与第 1 格 className 差异 → 选中格有额外类
    const base = hexSpans[0].className;
    const sel = hexSpans.filter((s) => s.className !== base);
    expect(sel).toHaveLength(3);
    void selCount;
  });

  it("三态受控: mode=bin 二进制网格(选区高亮跨态生效), mode=ascii 文本", () => {
    const node: SharkdTreeNode = { l: "Len", h: [1, 1] }; // 第 2 字节 'e' 高亮
    // 二进制: 'H'=0x48 → "01001000"; 'e'=0x65 → "01100101"; offset 列每行 8 字节
    const { unmount } = renderHex({ selNode: node, mode: "bin" });
    expect(screen.getByText("01001000")).toBeInTheDocument();
    expect(screen.getByText("01100101")).toBeInTheDocument();
    expect(screen.getByText("00000")).toBeInTheDocument();
    // 二进制选区高亮: 第 2 字节格比其余格多选中类
    const binCells = [...screen.getAllByText(/^0[01]{7}$/)];
    expect(binCells.length).toBeGreaterThanOrEqual(5);
    expect(binCells[1].className).not.toBe(binCells[0].className);
    unmount();
    // 文本: ASCII 可视化(ascii 态重命名)
    renderHex({ mode: "ascii" });
    expect(screen.getByText("H")).toBeInTheDocument();
    expect(screen.getByText("e")).toBeInTheDocument();
  });

  it("空数据: (空) 占位", () => {
    renderHex({ bytesB64: undefined });
    expect(screen.getByText("(空)")).toBeInTheDocument();
  });
});
