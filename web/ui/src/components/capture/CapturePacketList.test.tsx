// CapturePacketList.test.tsx - 包列表渲染: 列映射/行选中/色彩滤镜/空态(jsdom 非虚拟回退路径) +
// 滚动跟随透传(followOutput/atBottomStateChange, mock react-virtuoso + stubEnv 切虚拟路径; 跟随入口=自动跟随浮钮+滚到底自动恢复, 定案无工具栏开关) +
// 视口归底(scrollToIndex: 恢复跟随立即回底/分片重填滚新末行, mock handle 经 forwardRef 注入).
import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { CapturePacketList, CAPTURE_COLUMNS } from "./CapturePacketList";
import type { VirtuosoHandle } from "react-virtuoso";
import type { CaptureViewRow } from "../../hooks/captureAggregator";

// mock Virtuoso: 记录最近一次 props 供跟随透传断言(jsdom 无真实视口, 组件本体不渲染);
// forwardRef 注入 scrollToIndex handle; scrollerRef 注入假滚动容器(scrollTop 可设,
// addEventListener/removeEventListener 记录 handler, dispatch 手动派发 scroll 事件)
const { virtuosoProps, virtuosoHandles, virtuosoScrollers } = vi.hoisted(() => ({
  virtuosoProps: [] as Record<string, unknown>[],
  virtuosoHandles: [] as { scrollToIndex: ReturnType<typeof vi.fn> }[],
  virtuosoScrollers: [] as {
    scrollTop: number;
    scrollHeight: number;
    clientHeight: number;
    addEventListener: (t: string, h: () => void) => void;
    removeEventListener: (t: string, h: () => void) => void;
    dispatch: () => void;
  }[],
}));
vi.mock("react-virtuoso", async () => {
  const { forwardRef } = await import("react");
  return {
    Virtuoso: forwardRef((props: Record<string, unknown>, ref) => {
      virtuosoProps.push(props);
      const handle = { scrollToIndex: vi.fn() };
      virtuosoHandles.push(handle);
      if (typeof ref === "function") ref(handle);
      else if (ref) ref.current = handle;
      const handlers: Record<string, (() => void)[]> = {};
      const scroller = {
        scrollTop: 0,
        scrollHeight: 2000,
        clientHeight: 100,
        addEventListener: (t: string, h: () => void) => { (handlers[t] ??= []).push(h); },
        removeEventListener: (t: string, h: () => void) => { handlers[t] = (handlers[t] ?? []).filter((x) => x !== h); },
        dispatch: () => { for (const h of handlers.scroll ?? []) h(); },
      };
      virtuosoScrollers.push(scroller);
      if (typeof props.scrollerRef === "function") props.scrollerRef(scroller);
      return <div data-testid="virtuoso-mock" />;
    }),
  };
});

const rows: CaptureViewRow[] = [
  { c: ["1", "0.000000", "192.168.1.10", "192.168.1.1", "TCP", "60", "443 → 52000 [ACK] Seq=1"], num: 1, no: 1, ref: { shard: "a.pcapng", num: 1 } },
  { c: ["2", "0.001000", "192.168.1.1", "192.168.1.10", "TCP", "52", "52000 → 443 [ACK]"], num: 2, no: 2, ref: { shard: "a.pcapng", num: 2 }, bg: "ffffcc", fg: "000000" },
];

describe("CAPTURE_COLUMNS", () => {
  it("七列定义", () => {
    expect(CAPTURE_COLUMNS).toEqual(["No.", "时间", "源", "目的", "协议", "长度", "Info"]);
  });
});

describe("CapturePacketList", () => {
  it("列宽拖拽: 把手方向键调整列宽并持久化 localStorage, 双击复位", () => {
    localStorage.removeItem("webshark.colwidth.v1");
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={() => undefined} />
        </div>
      </FluentProvider>,
    );
    expect(screen.getByText("No.").style.width).toBe("52px");
    // 键盘调整时间列 +16px(jsdom 无 PointerEvent 实现, pointer 拖拽路径以真浏览器为准)
    const handle = screen.getByRole("button", { name: "调整时间列宽(拖拽, 方向键微调, 双击复位)" });
    fireEvent.keyDown(handle, { key: "ArrowRight" });
    expect(screen.getByText("时间").style.width).toBe("104px");
    // 双击复位(持久化时机)
    fireEvent.doubleClick(handle);
    expect(screen.getByText("时间").style.width).toBe("96px");
    expect(JSON.parse(localStorage.getItem("webshark.colwidth.v1")!)[1]).toBe(96);
    localStorage.removeItem("webshark.colwidth.v1");
  });


  it("行渲染: 列数组按位置映射七列", () => {
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={() => undefined} />
        </div>
      </FluentProvider>,
    );
    expect(screen.getAllByText("192.168.1.10").length).toBeGreaterThanOrEqual(1);
    expect(screen.getAllByText("TCP")).toHaveLength(2);
    expect(screen.getByText("443 → 52000 [ACK] Seq=1")).toBeInTheDocument();
    expect(screen.getAllByText("60")).toHaveLength(1);
  });

  it("点行 → onSelect(该帧)", () => {
    const onSelect = vi.fn();
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={onSelect} />
        </div>
      </FluentProvider>,
    );
    fireEvent.click(screen.getByText("443 → 52000 [ACK] Seq=1"));
    expect(onSelect).toHaveBeenCalledWith(rows[0]);
  });

  it("色彩滤镜: bg/fg 注入行 style(浏览器转为 rgb)", () => {
    const { container } = render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={() => undefined} />
        </div>
      </FluentProvider>,
    );
    const styled = container.querySelector("[role=button][style*='rgb(255, 255, 204)']");
    expect(styled).not.toBeNull();
  });

  it("色彩滤镜降级: bg 有 fg 缺 → 只染背景不覆盖文字色(无非法 '#inherit'); 无 bg → 无行内染色", () => {
    const r: CaptureViewRow[] = [
      { c: ["1", "0", "a", "b", "TCP", "60", "no-bg"], num: 1, no: 1, ref: { shard: "a.pcapng", num: 1 } },
      { c: ["2", "0", "a", "b", "TCP", "60", "bg-only"], num: 2, no: 2, ref: { shard: "a.pcapng", num: 2 }, bg: "ffffcc" },
    ];
    const { container } = render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={r} onSelect={() => undefined} />
        </div>
      </FluentProvider>,
    );
    const noBg = [...container.querySelectorAll("[role=button]")].find((el) => el.textContent?.includes("no-bg"));
    expect(noBg!.getAttribute("style")).toBeNull(); // 无 bg: 默认色
    const bgOnly = [...container.querySelectorAll("[role=button]")].find((el) => el.textContent?.includes("bg-only"));
    expect(bgOnly!.getAttribute("style")).toContain("rgb(255, 255, 204)");
    expect(bgOnly!.getAttribute("style")).not.toContain("inherit"); // fg 缺省不产生非法值
  });

  it("自动跟随浮钮: following=false 时右下角浮动小圆钮出现, 点击 → onFollowResume", () => {
    const onResume = vi.fn();
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={() => undefined} following={false} onFollowResume={onResume} />
        </div>
      </FluentProvider>,
    );
    const btn = screen.getByRole("button", { name: "自动跟随" });
    expect(btn).toBeInTheDocument();
    // absolute 覆层: 不参与列表布局(Griffel floatFollow position:absolute)
    expect(getComputedStyle(btn).position).toBe("absolute");
    fireEvent.click(btn);
    expect(onResume).toHaveBeenCalledTimes(1);
  });

  it("跟随浮钮: following=true(或缺省)不渲染", () => {
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={() => undefined} following={true} onFollowResume={() => undefined} />
        </div>
      </FluentProvider>,
    );
    expect(screen.queryByRole("button", { name: "跟随最新" })).not.toBeInTheDocument();
  });

  it("selectedNum 高亮对应行", () => {
    const { container } = render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} selectedNum={2} onSelect={() => undefined} />
        </div>
      </FluentProvider>,
    );
    const selRow = [...container.querySelectorAll("[role=button]")].find((el) => el.textContent?.includes("52000 → 443"));
    expect(selRow).toBeDefined();
    // 选中行比非选中行多一个 Griffel 类(rowSel 哈希)
    const plainRow = [...container.querySelectorAll("[role=button]")].find((el) => el.textContent?.includes("443 → 52000"));
    expect(selRow!.className.split(" ").length).toBeGreaterThan(plainRow!.className.split(" ").length);
  });

  it("空列表: 只渲染表头", () => {
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={[]} onSelect={() => undefined} />
        </div>
      </FluentProvider>,
    );
    for (const h of CAPTURE_COLUMNS) expect(screen.getByText(h)).toBeInTheDocument();
    expect(screen.queryByText("TCP")).not.toBeInTheDocument();
  });
});

const rows2: CaptureViewRow[] = [
  { c: ["7", "1.000000", "a", "b", "TCP", "60", "info 7"], num: 7, no: 1, ref: { shard: "b.pcapng", num: 7 } },
  { c: ["8", "1.001000", "a", "b", "TCP", "52", "info 8"], num: 8, no: 2, ref: { shard: "b.pcapng", num: 8 } },
  { c: ["9", "1.002000", "a", "b", "TCP", "60", "info 9"], num: 9, no: 3, ref: { shard: "b.pcapng", num: 9 } },
];

describe("CapturePacketList 滚动跟随透传", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    virtuosoProps.length = 0;
    virtuosoHandles.length = 0;
  });

  /** 切开发模式走虚拟路径(Virtuoso mock), 渲染后取最近一次 Virtuoso props */
  function renderVirtual(following: boolean, onFollowCancel?: () => void, onFollowResume?: () => void) {
    vi.stubEnv("MODE", "development");
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={() => undefined} following={following}
            onFollowCancel={onFollowCancel} onFollowResume={onFollowResume} />
        </div>
      </FluentProvider>,
    );
    return virtuosoProps[virtuosoProps.length - 1];
  }

  it("following=true → Virtuoso followOutput=true(恒滚底, 非 auto 近底判定)", () => {
    const props = renderVirtual(true);
    expect(props.followOutput).toBe(true);
  });

  it("following=false → Virtuoso followOutput=false(不滚尾)", () => {
    const props = renderVirtual(false);
    expect(props.followOutput).toBe(false);
  });

  it("atBottomStateChange(false) 不取消跟随(取消只由原生 scroll 方向判定); (true) → onFollowResume", () => {
    const onCancel = vi.fn();
    const onResume = vi.fn();
    const props = renderVirtual(true, onCancel, onResume) as {
      atBottomStateChange?: (atBottom: boolean) => void;
    };
    props.atBottomStateChange?.(false); // 任意时机 false: 不再触发取消
    expect(onCancel).not.toHaveBeenCalled();
    props.atBottomStateChange?.(true); // 滚回底部: 自动恢复跟随
    expect(onResume).toHaveBeenCalledTimes(1);
  });
});

describe("CapturePacketList 用户上滚取消(原生 scroll 方向判定)", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    virtuosoProps.length = 0;
    virtuosoHandles.length = 0;
    virtuosoScrollers.length = 0;
  });

  /** 切开发模式走虚拟路径, 取最新假滚动容器 */
  function renderScroller(following: boolean, onFollowCancel: () => void) {
    vi.stubEnv("MODE", "development");
    render(
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={rows} onSelect={() => undefined} following={following}
            onFollowCancel={onFollowCancel} />
        </div>
      </FluentProvider>,
    );
    return virtuosoScrollers[virtuosoScrollers.length - 1];
  }

  it("① scrollTop 上减(5000→3000, 差>4) → onFollowCancel 调用", () => {
    const onCancel = vi.fn();
    const el = renderScroller(true, onCancel);
    el.scrollTop = 5000;
    el.dispatch(); // 建立基准
    expect(onCancel).not.toHaveBeenCalled();
    el.scrollTop = 3000;
    el.dispatch();
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it("② scrollTop 升高(程序归底方向) → 不取消", () => {
    const onCancel = vi.fn();
    const el = renderScroller(true, onCancel);
    el.scrollTop = 0;
    el.dispatch();
    el.scrollTop = 5000;
    el.dispatch();
    el.scrollTop = 8000;
    el.dispatch();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("③ 微抖动(差≤4) → 不取消(方向判定死区)", () => {
    const onCancel = vi.fn();
    const el = renderScroller(true, onCancel);
    el.scrollTop = 5000;
    el.dispatch();
    el.scrollTop = 4998;
    el.dispatch();
    expect(onCancel).not.toHaveBeenCalled();
  });

  it("⑥ 内容不可滚(空列表/塌缩钳位噪声) → 不取消, 只记位置", () => {
    const onCancel = vi.fn();
    const el = renderScroller(true, onCancel);
    el.scrollHeight = 100;
    el.clientHeight = 300; // scrollHeight <= clientHeight: 视口不可滚
    el.scrollTop = 500;
    el.dispatch();
    el.scrollTop = 0;
    el.dispatch();
    expect(onCancel).not.toHaveBeenCalled();
    // 恢复可滚后, 后续真实上滚仍生效(噪声只记位不污染方向判定)
    el.scrollHeight = 2000;
    el.scrollTop = 500;
    el.dispatch();
    el.scrollTop = 0;
    el.dispatch();
    expect(onCancel).toHaveBeenCalledTimes(1);
  });
});

describe("CapturePacketList 视口归底(P1/P2)", () => {
  afterEach(() => {
    vi.unstubAllEnvs();
    virtuosoProps.length = 0;
    virtuosoHandles.length = 0;
    virtuosoScrollers.length = 0;
  });

  const refObj: { current: VirtuosoHandle | null } = { current: null };

  function ui(following: boolean, r: CaptureViewRow[], onFollowCancel?: () => void) {
    return (
      <FluentProvider theme={webDarkTheme}>
        <div style={{ height: 300 }}>
          <CapturePacketList rows={r} onSelect={() => undefined} following={following}
            onFollowCancel={onFollowCancel} virtuosoRef={refObj} />
        </div>
      </FluentProvider>
    );
  }

  it("P1: 恢复跟随(following false→true) → 立即 scrollToIndex 末行(不等下一次 append)", () => {
    vi.stubEnv("MODE", "development");
    const { rerender } = render(ui(false, rows));
    expect(refObj.current).not.toBeNull();
    rerender(ui(true, rows));
    expect(refObj.current!.scrollToIndex).toHaveBeenCalledWith({ index: rows.length - 1, align: "end" });
  });

  it("P2: 分片重填(fresh 清空后 rows 替换, following) → scrollToIndex 新分片末行", () => {
    vi.stubEnv("MODE", "development");
    const { rerender } = render(ui(true, []));
    expect(refObj.current!.scrollToIndex).not.toHaveBeenCalled(); // rows 空: 不滚
    rerender(ui(true, rows2));
    expect(refObj.current!.scrollToIndex).toHaveBeenCalledWith({ index: rows2.length - 1, align: "end" });
  });

  it("following=false 行变化不滚底(冻结视图)", () => {
    vi.stubEnv("MODE", "development");
    const { rerender } = render(ui(false, rows));
    rerender(ui(false, rows2));
    expect(refObj.current!.scrollToIndex).not.toHaveBeenCalled();
  });

  it("冷启守卫: rows 空 → atBottomStateChange(false) 不取消跟随, rows 落地后照常归底", () => {
    vi.stubEnv("MODE", "development");
    const onCancel = vi.fn();
    const { rerender } = render(ui(true, [], onCancel));
    // 冷启/重填窗口: Virtuoso 对空列表报 atBottom=false —— 非用户上滚, 不得掐 following
    (virtuosoProps[virtuosoProps.length - 1] as { atBottomStateChange?: (b: boolean) => void })
      .atBottomStateChange?.(false);
    expect(onCancel).not.toHaveBeenCalled();
    // rows 落地: following 仍存活, 归底 scroll 正常发生
    rerender(ui(true, rows2, onCancel));
    expect(refObj.current!.scrollToIndex).toHaveBeenCalledWith({ index: rows2.length - 1, align: "end" });
  });

  // false→cancel 路径不存在: 底部状态无法区分用户上滚与初始填充/程序滚动,
  // 取消跟随即由上方"原生 scroll 方向判定"用例覆盖
});
