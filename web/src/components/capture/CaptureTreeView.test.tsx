// CaptureTreeView.test.tsx - 树渲染基础: 行投影/懒展开/点选回调/filter 按钮回调.
import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { CaptureTreeView } from "./CaptureTreeView";
import type { SharkdTreeNode } from "../../api/sharkdTypes";

const tree: SharkdTreeNode[] = [
  {
    l: "Ethernet II, Src: aa:bb, Dst: cc:dd", h: [0, 14], f: "eth",
    n: [
      { l: "Source: aa:bb", f: "eth.src == aa:bb", h: [6, 6] },
      { l: "Destination: cc:dd", f: "eth.dst == cc:dd", h: [0, 6] },
    ],
  },
  { l: "Internet Protocol Version 4", h: [14, 20], f: "ip", n: [{ l: "Total Length: 60", f: "ip.len == 60", h: [16, 2] }] },
  { l: "Data (6 bytes)", h: [34, 6] },
];

function renderTree(overrides: Partial<Parameters<typeof CaptureTreeView>[0]> = {}) {
  const onNodeSelect = vi.fn();
  const onFilter = vi.fn();
  const utils = render(
    <FluentProvider theme={webDarkTheme}>
      <div style={{ height: 400 }}>
        <CaptureTreeView tree={tree} onNodeSelect={onNodeSelect} onFilter={onFilter} {...overrides} />
      </div>
    </FluentProvider>,
  );
  return { onNodeSelect, onFilter, ...utils };
}

describe("CaptureTreeView", () => {
  it("首屏展开顶层协议级: 子级行可见, 无子节点行无展开钮文案", () => {
    renderTree();
    // react-virtuoso 在 jsdom 无真实布局, 首帧渲染 itemContent 由 mock resize 驱动, 主动派发 resize 触发渲染
    fireEvent(window, new Event("resize"));
    expect(screen.getByText(/Ethernet II, Src: aa:bb/)).toBeInTheDocument();
    // 顶层已展开 → 子级行可见
    expect(screen.getByText(/Source: aa:bb/)).toBeInTheDocument();
    // 第三根无子级, 原样可见
    expect(screen.getByText(/Data \(6 bytes\)/)).toBeInTheDocument();
  });

  it("点行 → onNodeSelect(节点引用)", () => {
    const { onNodeSelect } = renderTree();
    fireEvent.click(screen.getByText("Source: aa:bb"));
    expect(onNodeSelect).toHaveBeenCalledWith(tree[0].n![0]);
  });

  it("收起顶层 → 子级行消失; 再展开恢复", () => {
    renderTree();
    // 顶层 Ethernet 的 caret: 第一个可展开行的 chevron 按钮
    const carets = screen.getAllByRole("generic").filter((el) => el.className.includes("caret") || el.tagName === "SPAN");
    // 直接点击标签行旁的 caret 区(第一个 chevron 容器)
    const ethRow = screen.getByText("Ethernet II, Src: aa:bb, Dst: cc:dd").closest("[role=button]")!;
    const caret = ethRow.firstElementChild as HTMLElement;
    fireEvent.click(caret);
    expect(screen.queryByText("Source: aa:bb")).not.toBeInTheDocument();
    fireEvent.click(caret);
    expect(screen.getByText("Source: aa:bb")).toBeInTheDocument();
    void carets;
  });

  it("点'作为过滤器'钮 → onFilter(f 串), 不触发行选", () => {
    const { onFilter, onNodeSelect } = renderTree();
    const btn = screen.getByRole("button", { name: /作为过滤器 ip\.len == 60/ });
    fireEvent.click(btn);
    expect(onFilter).toHaveBeenCalledWith("ip.len == 60");
    expect(onNodeSelect).not.toHaveBeenCalled();
  });

  it("换帧(全新节点对象): 相同字段路径键自动继承展开态, 不全塌", () => {
    const t1: SharkdTreeNode[] = [
      { l: "Ethernet II", f: "eth", n: [
        { l: "Internet Protocol", f: "ip", n: [{ l: "Total Length: 60", f: "ip.len == 60" }] },
      ] },
    ];
    // 形状相同的全新树(换帧: 节点对象全新, f 值同字段稳定)
    const t2: SharkdTreeNode[] = [
      { l: "Ethernet II (f2)", f: "eth", n: [
        { l: "Internet Protocol (f2)", f: "ip", n: [{ l: "Total Length: 61", f: "ip.len == 61" }] },
      ] },
    ];
    const { rerender } = renderTree({ tree: t1 });
    // 顶层默认展开; 手动展开二级 ip(jsdom 非虚拟路径, 行投影同步于 render/resize)
    fireEvent.click(screen.getByText("Internet Protocol").closest("[role=button]")!.firstElementChild as HTMLElement);
    fireEvent.click(screen.getByText("Internet Protocol").closest("[role=button]")!.firstElementChild as HTMLElement);
    fireEvent(window, new Event("resize"));
    expect(screen.getByText("Total Length: 60")).toBeInTheDocument();
    // 换帧: 新树对象引用全新 → 展开态按路径键继承(手工展开键与顶层默认展开都保留)
    rerender(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 400 }}>
          <CaptureTreeView tree={t2} onNodeSelect={() => undefined} onFilter={() => undefined} />
        </div>
      </FluentProvider>,
    );
    fireEvent(window, new Event("resize"));
    expect(screen.getByText("Internet Protocol (f2)")).toBeInTheDocument();
    expect(screen.getByText("Total Length: 61")).toBeInTheDocument(); // 二级展开态保留
  });

  it("空树: 不渲染内部头(页头由 CapturePage paneHead 唯一承担)", () => {
    render(
      <FluentProvider theme={webDarkTheme}>
        <CaptureTreeView tree={[]} onNodeSelect={() => undefined} onFilter={() => undefined} />
      </FluentProvider>,
    );
    expect(screen.queryByText("协议树详情")).not.toBeInTheDocument();
    expect(screen.queryByText("Data (6 bytes)")).not.toBeInTheDocument();
  });
});
