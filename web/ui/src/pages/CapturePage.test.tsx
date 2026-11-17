// CapturePage.test.tsx - 抓包页集成(live 对接): subscribe 先行建流 + 零 JSON-RPC 轮询(WS 无
// JSON-RPC; status 探活为独立 HTTP 轮询) / rows 推送渲染(No.=聚合序号) / 帧详情 HTTP 化(ref 复
// 合键路由, 404 历史分片横幅) / 换帧详情常挂 / 自动跟随浮钮入口 / 暂停恢复抓包(_ctl 上行) / 断
// 线重连(subscribe 再行)/ 导出 / 工具栏(品牌 standalone 显示|embedded 隐藏, 状态徽章来自
// status 探活, 主题/令牌图标钮).
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { act, fireEvent, screen } from "@testing-library/react";
import { FluentProvider, webDarkTheme } from "@fluentui/react-components";
import { render } from "@testing-library/react";
import CapturePage from "./CapturePage";
import { downloadCaptureAll } from "../api/capture";
import { setToken } from "../api/token";
import { MockWebSocket, installMockWebSocket } from "../test/mockWebSocket";
import type { CaptureRowWire } from "../hooks/captureAggregator";

// downloadCaptureAll mock(jsdom 锚点点击导航未实现, 直接断言调用)
vi.mock("../api/capture", async (importOriginal) => {
  const orig = await importOriginal<typeof import("../api/capture")>();
  return { ...orig, downloadCaptureAll: vi.fn() };
});
const mockedDownload = vi.mocked(downloadCaptureAll);

/** rpcRequests: 上行 JSON-RPC 请求数(live 模式只认 _ctl, 轮询退役断言用) */
function rpcRequests(ws: MockWebSocket): Record<string, unknown>[] {
  return ws.sent.map((l) => JSON.parse(l) as Record<string, unknown>).filter((r) => r.method !== undefined);
}

function wire(num: number, shard = "a.pcapng"): CaptureRowWire {
  return { c: [String(num), "0.000000", "a", "b", "TCP", "60", `info ${num}`], num, ref: { shard, num } };
}

describe("CapturePage", () => {
  let restoreWS: () => void;
  /** frame HTTP 端点应答(status!=200 时 body 为错误文本) */
  let frameReply: { status: number; body: unknown };
  /** frame 挂起门(换帧常挂用例: 置 Promise 延缓应答, 释放后继续) */
  let frameHold: Promise<void> | null = null;
  let releaseFrame: () => void;
  /** tap expert HTTP 端点应答(专家面板用例) */
  let tapReply: unknown;
  /** GET /api/status 应答(工具栏 sharkd 徽章/分片数用) */
  let statusReply: { connected: boolean; files: { name: string; size: number; mtime: string }[]; reason?: string };
  beforeEach(() => {
    vi.useFakeTimers();
    restoreWS = installMockWebSocket();
    localStorage.clear(); // 筛选持久化跨用例泄漏清理(重开保留特性会读上次筛选)
    window.history.replaceState(null, "", "/"); // 同上: ?filter= 与地址栏残留
    mockedDownload.mockReset();
    frameReply = { status: 200, body: { tree: [{ l: "Ethernet II" }], bytes: "AAE=" } };
    frameHold = null;
    tapReply = { taps: [] };
    statusReply = { connected: true, files: [{ name: "a.pcapng", size: 1, mtime: "" }] };
    setToken("tk-page");
    // fetch 路由: subscribe(建流)/status(探活)/frame(详情)/check/complete 放行; 其余 URL throw 暴露未预期请求
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/subscribe")) {
        return { ok: true, status: 200, json: async () => ({ ok: true, handle: "h", paused: false, filter: "", shard: "a.pcapng" }) } as Response;
      }
      if (url.includes("/api/status")) {
        return { ok: true, status: 200, json: async () => statusReply } as Response;
      }
      if (url.includes("/api/check")) {
        return { ok: true, status: 200, json: async () => ({ status: "OK" }) } as Response;
      }
      if (url.includes("/api/complete")) {
        return { ok: true, status: 200, json: async () => ({ field: [], pref: [] }) } as Response;
      }
      if (url.includes("/api/tap")) {
        return { ok: true, status: 200, json: async () => tapReply } as Response;
      }
      if (url.includes("/api/frame")) {
        if (frameHold !== null) await frameHold;
        if (frameReply.status === 200) return { ok: true, status: 200, json: async () => frameReply.body } as Response;
        return { ok: false, status: frameReply.status, json: async () => frameReply.body } as Response;
      }
      throw new Error(`unexpected fetch: ${url}`);
    }));
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    restoreWS();
    // act 包裹: 清理期 setToken 触发 hook 内 token 订阅 setState(RTL cleanup 在其后运行)
    act(() => { setToken(""); });
  });

  function subscribeCalls(): number {
    return (fetch as ReturnType<typeof vi.fn>).mock.calls.filter(([i]) => String(i).includes("/api/subscribe")).length;
  }
  function frameCalls(): string[] {
    return (fetch as ReturnType<typeof vi.fn>).mock.calls.map(([i]) => String(i)).filter((u) => u.includes("/api/frame"));
  }

  /** 挂载并建流(subscribe → WS 握手 + hello + 首批 rows 推送) */
  async function mountOpen(rows: CaptureRowWire[] = [], shard = "a.pcapng", props: { embedded?: boolean; onOpenToken?: () => void } = {}) {
    render(
      <FluentProvider theme={webDarkTheme}>
        <CapturePage embedded={props.embedded} onOpenToken={props.onOpenToken} />
      </FluentProvider>,
    );
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    act(() => { ws.open(); });
    act(() => { ws.push({ type: "hello", mode: "live" }); });
    if (rows.length > 0) {
      act(() => { ws.push({ type: "rows", mode: "live", shard, rows }); });
    }
    await act(async () => { await Promise.resolve(); });
    return ws;
  }

  it("subscribe 先行建流 + WS 零 JSON-RPC 轮询: 等待期占位数据到达即退", async () => {
    render(
      <FluentProvider theme={webDarkTheme}>
        <CapturePage />
      </FluentProvider>,
    );
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    // subscribe 恰一次先行, WS URL 为 ?handle=&token= 形态
    expect(subscribeCalls()).toBe(1);
    const u = new URL(ws.url);
    expect(u.pathname).toMatch(/\/api\/ws$/);
    expect(u.searchParams.get("token")).toBe("tk-page"); // WS 握手 ?token=
    expect((u.searchParams.get("handle") ?? "").length).toBeGreaterThan(0);
    act(() => { ws.open(); });
    // 握手前: 等待数据占位
    expect(screen.getAllByText(/正在等待实时数据流/).length).toBeGreaterThanOrEqual(1);
    act(() => { ws.push({ type: "hello", mode: "live" }); });
    expect(screen.getAllByText(/正在等待实时数据流/).length).toBeGreaterThanOrEqual(1);
    // 时间推进(轮询若存在必发请求/调用) + 推送第一批: 占位退, 行渲染
    act(() => { vi.advanceTimersByTime(10_000); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(rpcRequests(ws)).toEqual([]); // 零 JSON-RPC 轮询
    expect(subscribeCalls()).toBe(1); // 建流外 WS 无额外握手
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1)] }); });
    expect(screen.queryByText(/正在等待实时数据流/)).not.toBeInTheDocument();
    expect(screen.getByText("info 1")).toBeInTheDocument();
  });

  it("工具栏 standalone: 品牌显示 + sharkd 徽章/分片数来自 status 探活 + 主题/令牌图标钮", async () => {
    statusReply = { connected: true, files: [{ name: "a.pcapng", size: 1, mtime: "" }, { name: "b.pcapng", size: 1, mtime: "" }] };
    const openToken = vi.fn();
    await mountOpen([wire(1)], "a.pcapng", { onOpenToken: openToken });
    expect(screen.getByText("webshark")).toBeVisible();
    expect(screen.getByText("sharkd 在线")).toBeInTheDocument();
    expect(screen.getByText("2 分片")).toBeInTheDocument();
    expect(screen.getByText("已连接")).toBeInTheDocument();
    expect(screen.queryByText("报文分析")).not.toBeInTheDocument();
    // 主题图标钮: 存在且可切换
    const themeBtn = screen.getByRole("button", { name: "切换主题" });
    expect(themeBtn.querySelector("svg")).not.toBeNull();
    fireEvent.click(themeBtn);
    expect(localStorage.getItem("webshark-theme")).toBe("light");
    // 令牌图标钮: 存在且触发回调
    fireEvent.click(screen.getByRole("button", { name: "访问令牌" }));
    expect(openToken).toHaveBeenCalledTimes(1);
  });

  it("工具栏 embedded: 品牌隐藏(占位保留) + 主题/令牌钮隐藏 + status 失败 → sharkd 离线徽章", async () => {
    statusReply = { connected: false, files: [], reason: "sharkd 未就绪" };
    await mountOpen([], "a.pcapng", { embedded: true });
    const brand = screen.getByText("webshark");
    expect(brand).not.toBeVisible(); // visibility 隐藏, 工具栏高度不跳
    expect(screen.getByText("sharkd 离线")).toBeInTheDocument();
    expect(screen.getByText("0 分片")).toBeInTheDocument();
    expect(screen.getByText("已连接")).toBeInTheDocument(); // WS 流与 status 探活互相独立
    // standalone 仅在非嵌入形态显示品牌
    expect(screen.queryByText("报文分析")).not.toBeInTheDocument();
    // 主题/令牌图标钮: embedded 隐藏(归宿主管, 经 postMessage 注入)
    expect(screen.queryByRole("button", { name: "切换主题" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "访问令牌" })).not.toBeInTheDocument();
  });

  it("embedded 401 不弹 TokenGate: 保持断连横幅(主题/令牌由宿主管)", async () => {
    // 401 应答: subscribe 失败 → 断连横幅(而非录入页)
    (fetch as ReturnType<typeof vi.fn>).mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/subscribe")) {
        return { ok: false, status: 401, json: async () => ({ code: 401, message: "未认证" }) } as Response;
      }
      if (url.includes("/api/status")) {
        return { ok: false, status: 401, json: async () => ({ code: 401, message: "未认证" }) } as Response;
      }
      throw new Error(`unexpected fetch: ${url}`);
    });
    render(
      <FluentProvider theme={webDarkTheme}>
        <CapturePage embedded />
      </FluentProvider>,
    );
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    // 无 TokenGate 录入页(TokenGate 由 App 层 standalone 门控); 断连横幅呈现
    // (宿主重发 set-token 后按新令牌重连, 见 useCaptureStream.test 令牌重连用例)
    expect(screen.queryByText("webshark 访问令牌")).not.toBeInTheDocument();
    expect(screen.getByText(/实时流已断开/)).toBeInTheDocument();
  });

  it("rows 推送渲染: No. 列 = 聚合序号(跨分片连续), 点行走 HTTP frame(ref 复合键)", async () => {
    const ws = await mountOpen([wire(5), wire(6)], "a.pcapng");
    expect(screen.getByText("info 5")).toBeInTheDocument();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "b.pcapng", rows: [wire(1, "b.pcapng")] }); });
    // 跨分片行直接追加: No. 列显示聚合序号 1/2/3(非分片内帧号)
    expect(screen.getByText("info 1")).toBeInTheDocument();
    expect(screen.getByText("info 6")).toBeInTheDocument();

    // 点行(聚合序号 3, ref=b.pcapng#1) → HTTP frame 详情; WS 无 JSON-RPC
    fireEvent.click(screen.getByText("info 1"));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const calls = frameCalls();
    expect(calls).toHaveLength(1);
    const q = new URL(calls[0], "http://localhost").searchParams; // fetch 相对 BASE_URL, 补解析基址
    expect(q.get("shard")).toBe("b.pcapng");
    expect(q.get("num")).toBe("1");
    expect((q.get("handle") ?? "").length).toBeGreaterThan(0);
    expect(rpcRequests(ws)).toEqual([]);
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("Ethernet II")).toBeInTheDocument();
    // 面板头帧徽标 = 聚合序号
    expect(screen.getAllByText("帧 3").length).toBeGreaterThanOrEqual(1);
  });

  it("frame 详情 404(历史分片)横幅在下一次成功 fetchFrame 后清除", async () => {
    const ws = await mountOpen([wire(1), wire(2)]);
    // 点行 1 → 历史/异常 404 → 错误横幅透出服务端消息
    frameReply = { status: 404, body: { code: 404, message: "分片不在缓存视图" } };
    fireEvent.click(screen.getByText("info 1"));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText(/帧 1 详情失败: 分片不在缓存视图/)).toBeInTheDocument();

    // 点行 2 → 成功 → 横幅清除
    frameReply = { status: 200, body: { tree: [{ l: "Ethernet II" }], bytes: "AAE=" } };
    fireEvent.click(screen.getByText("info 2"));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(screen.queryByText(/帧 1 详情失败/)).not.toBeInTheDocument();
    expect(screen.getByText("Ethernet II")).toBeInTheDocument();
    expect(rpcRequests(ws)).toEqual([]);
  });

  it("换帧详情常挂: 点行不卸载详情面板(旧内容降透明+头部加载提示标记可见), 新详情到达原地替换", async () => {
    const ws = await mountOpen([wire(1), wire(2)]);
    // 帧 1 详情加载完成
    fireEvent.click(screen.getByText("info 1"));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("Ethernet II")).toBeInTheDocument();

    // 点行 2: 应答挂起 — 旧帧树保留渲染(不卸载), 加载标记可见
    frameReply = { status: 200, body: { tree: [{ l: "IPv4 Header" }], bytes: "AgM=" } };
    frameHold = new Promise<void>((r) => { releaseFrame = r; });
    fireEvent.click(screen.getByText("info 2"));
    await act(async () => { await Promise.resolve(); });
    expect(frameCalls()).toHaveLength(2);
    expect(screen.getByText("Ethernet II")).toBeInTheDocument(); // 旧树在途可见
    expect(screen.getAllByText("加载帧详情…").length).toBeGreaterThanOrEqual(1);
    const pane = screen.getByText("Ethernet II").closest("[data-loading]") as HTMLElement;
    expect(pane).not.toBeNull();

    // 释放应答: 原地替换(同一容器持续存在, 未卸载重挂), 加载标记清除
    releaseFrame();
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(pane.isConnected).toBe(true); // 容器持续存在
    expect(pane.contains(screen.getByText("IPv4 Header"))).toBe(true);
    expect(screen.queryByText("Ethernet II")).not.toBeInTheDocument(); // 旧帧内容已被替换
    expect(screen.queryByText("加载帧详情…")).not.toBeInTheDocument();
    expect(pane.querySelector("[data-loading]")).toBeNull();
    expect(rpcRequests(ws)).toEqual([]);
  });

  it("自动跟随入口收敛(浮钮唯一): 默认跟随无浮钮, 推送照常追加渲染, 零上行", async () => {
    const ws = await mountOpen([wire(1)]);
    // 默认 following=true: 无浮动钮(工具栏亦无重复开关)
    expect(screen.queryByRole("button", { name: "自动跟随" })).not.toBeInTheDocument();
    const sentBefore = ws.sent.length; // conn open 断言的初始 _ctl resume 不计入
    // 数据照常追加渲染
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(2), wire(3)] }); });
    expect(screen.getByText("info 3")).toBeInTheDocument();
    // 零上行: 无任何新请求(_ctl/JSON-RPC)
    expect(ws.sent.length).toBe(sentBefore);
    expect(screen.queryByRole("switch", { name: "自动滚动" })).not.toBeInTheDocument();
  });

  it("暂停/恢复抓包: WS 上行 _ctl 流控制, 与 autoscroll 语义独立", async () => {
    const ws = await mountOpen([wire(1)]);
    fireEvent.click(screen.getByRole("button", { name: "暂停抓包" }));
    expect(JSON.parse(ws.sent[ws.sent.length - 1])).toEqual({ type: "_ctl", op: "capture", state: "pause" });
    // 暂停态徽标出现
    expect(screen.getByText("已暂停")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "恢复抓包" }));
    expect(JSON.parse(ws.sent[ws.sent.length - 1])).toEqual({ type: "_ctl", op: "capture", state: "resume" });
  });

  it("空态 Spinner 三态文案: 暂停=抓包已暂停 / 筛选中=等待匹配报文 / 默认=正在等待实时数据流", async () => {
    const ws = await mountOpen(); // 无 rows
    const hits = (t: string) => screen.getAllByText(t).length;
    expect(hits("正在等待实时数据流…")).toBeGreaterThanOrEqual(1);
    // 暂停态
    fireEvent.click(screen.getByRole("button", { name: "暂停抓包" }));
    expect(hits("抓包已暂停")).toBeGreaterThanOrEqual(1);
    fireEvent.click(screen.getByRole("button", { name: "恢复抓包" }));
    expect(hits("正在等待实时数据流…")).toBeGreaterThanOrEqual(1);
    // 筛选态(空匹配窗口)
    const input = screen.getByLabelText("显示过滤器");
    fireEvent.change(input, { target: { value: "tcp.port == 502" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(hits("等待匹配报文…")).toBeGreaterThanOrEqual(1);
    expect(ws.sent.filter((l) => l.includes('"method"'))).toEqual([]);
  });

  it("筛选提交/清除: Enter 发 _ctl{op:filter} + 「筛选中」标识, 清除按钮发空串退出并撤标识", async () => {
    const ws = await mountOpen([wire(1)]);
    const input = screen.getByLabelText("显示过滤器");
    expect(screen.getByText("筛选中")).not.toBeVisible(); // 恒挂容器 visibility 切换(防清除钮跳动)
    fireEvent.change(input, { target: { value: "tcp.port == 502" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(JSON.parse(ws.sent[ws.sent.length - 1])).toEqual({ type: "_ctl", op: "filter", filter: "tcp.port == 502" });
    const badge = screen.getByText("筛选中");
    expect(badge).toBeVisible(); // 激活态角标
    expect(badge.closest("[title]")).not.toBeNull(); // title 提示已生效表达式
    // 清除(X 按钮): onChange("")+onApply("") → _ctl 空串退出, 标识撤销
    fireEvent.click(screen.getByRole("button", { name: "清除过滤器" }));
    expect(JSON.parse(ws.sent[ws.sent.length - 1])).toEqual({ type: "_ctl", op: "filter", filter: "" });
    expect(screen.getByText("筛选中")).not.toBeVisible();
    expect(rpcRequests(ws)).toEqual([]); // check/complete 走 HTTP, WS 无 JSON-RPC
  });

  it("error 帧: 「控制指令未生效」横幅出现, 筛选中乐观标识撤销", async () => {
    const ws = await mountOpen([wire(1)]);
    const input = screen.getByLabelText("显示过滤器");
    fireEvent.change(input, { target: { value: "tcp.port == 502" } });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(screen.getByText("筛选中")).toBeVisible();
    act(() => { ws.push({ type: "error", reason: "筛选失败: 语法错" }); });
    expect(screen.getByText("控制指令未生效")).toBeInTheDocument();
    expect(screen.getByText("筛选失败: 语法错")).toBeInTheDocument();
    expect(screen.getByText("筛选中")).not.toBeVisible(); // 乐观回退
    expect(rpcRequests(ws)).toEqual([]);
  });

  it("校验/补全防抖走 HTTP(400ms check / 300ms complete), WS 零 JSON-RPC", async () => {
    const ws = await mountOpen([wire(1)]);
    const input = screen.getByLabelText("显示过滤器");
    fireEvent.change(input, { target: { value: "tcp.por" } }); // 尾 token 非运算符: 补全前缀有效
    const calls = (frag: string) => (fetch as ReturnType<typeof vi.fn>).mock.calls.filter(([i]) => String(i).includes(frag)).length;
    // 未到任一防抖期: 均不发
    act(() => { vi.advanceTimersByTime(250); });
    expect(calls("/api/check")).toBe(0);
    expect(calls("/api/complete")).toBe(0);
    // 300ms: complete 补全先发; 400ms: check 校验发
    act(() => { vi.advanceTimersByTime(60); });
    expect(calls("/api/complete")).toBe(1);
    act(() => { vi.advanceTimersByTime(100); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(calls("/api/check")).toBe(1);
    expect(rpcRequests(ws)).toEqual([]); // 结构上不可能再发 JSON-RPC
  });

  it("断线重连: 断开横幅 + 重连徽标, 重连 subscribe 再行后按当前态重断言暂停", async () => {
    const ws = await mountOpen([wire(1)]);
    fireEvent.click(screen.getByRole("button", { name: "暂停抓包" }));
    expect(JSON.parse(ws.sent[ws.sent.length - 1])).toEqual({ type: "_ctl", op: "capture", state: "pause" });
    // 断线 → 横幅 + 重连中徽标
    act(() => { ws.close(); });
    expect(screen.getByText("实时流已断开")).toBeInTheDocument();
    expect(screen.getByText("重连中…")).toBeInTheDocument();
    // 退避重连: subscribe 先行(确保会话在宽限期内) → 全新 WS → 横幅退; conn open 时重断言暂停态
    act(() => { vi.advanceTimersByTime(1100); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(subscribeCalls()).toBe(2);
    const ws2 = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    expect(ws2).not.toBe(ws);
    act(() => { ws2.open(); });
    await act(async () => { await Promise.resolve(); });
    expect(screen.queryByText("实时流已断开")).not.toBeInTheDocument();
    expect(JSON.parse(ws2.sent[ws2.sent.length - 1])).toEqual({ type: "_ctl", op: "capture", state: "pause" });
  });

  it("reset: 清空重建窗口占位, 全量重推后恢复渲染", async () => {
    const ws = await mountOpen([wire(1)]);
    act(() => { ws.push({ type: "reset", reason: "overflow" }); });
    expect(screen.getAllByText(/正在等待实时数据流/).length).toBeGreaterThanOrEqual(1);
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2)] }); });
    expect(screen.queryByText(/正在等待实时数据流/)).not.toBeInTheDocument();
    expect(screen.getByText("info 2")).toBeInTheDocument();
  });

  it("详情串帧防护: 先点慢帧 A 后点快帧 B, A 的迟到应答不得覆盖 B 的详情", async () => {
    // 两次 frame 请求按调用顺序入列, 应答体手动 resolve(模拟 A=冷分片慢 / B=缓存命中快)
    const resolvers: Array<(d: unknown) => void> = [];
    (fetch as ReturnType<typeof vi.fn>).mockImplementation(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/frame")) {
        const body = await new Promise<unknown>((r) => { resolvers.push(r); });
        return { ok: true, status: 200, json: async () => body } as Response;
      }
      // 其余端点: subscribe 放行建流, check/complete/tap 空应答
      if (url.includes("/api/subscribe")) {
        return { ok: true, status: 200, json: async () => ({ ok: true, handle: "h", paused: false, filter: "", shard: "a.pcapng" }) } as Response;
      }
      return { ok: true, status: 200, json: async () => ({}) } as Response;
    });
    const ws = await mountOpen([wire(1), wire(2)]);

    // 点 A(帧 1) 请求挂起 → 点 B(帧 2) 请求挂起
    fireEvent.click(screen.getByText("info 1"));
    await act(async () => { await Promise.resolve(); });
    fireEvent.click(screen.getByText("info 2"));
    await act(async () => { await Promise.resolve(); });
    expect(resolvers).toHaveLength(2);

    // 先 resolve B(快) 再 resolve A(慢): A 迟到不得覆盖 B
    resolvers[1]({ tree: [{ l: "IPv4 Header" }], bytes: "AgM=" });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("IPv4 Header")).toBeInTheDocument(); // B 先渲染

    resolvers[0]({ tree: [{ l: "Ethernet II" }], bytes: "AAE=" });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    await act(async () => { await Promise.resolve(); });
    // 最终详情必须是 B(后点帧): A 的迟到应答被丢弃
    expect(screen.getByText("IPv4 Header")).toBeInTheDocument();
    expect(screen.queryByText("Ethernet II")).not.toBeInTheDocument();
    expect(rpcRequests(ws)).toEqual([]);
  });

  it("专家面板分片以 tap 应答为准: tap 声明 shard=A, 点击时 shard 状态已轮换为 B, 查询用 A", async () => {
    // 帧 9 不在 rows 内 → 点击走合成行路径, 查询 shard 必须取 tap 应答值而非点击时的 shard 状态
    const ws = await mountOpen([wire(1)], "a.pcapng");
    // 服务端已轮换(rotated) → shard 状态变为 B; tap 应答声明实际执行分片 = A
    act(() => { ws.push({ type: "rotated", shard: "b.pcapng" }); });
    tapReply = { shard: "a.pcapng", taps: [{ tap: "expert", type: "expert", details: [{ f: 9, s: "Error", g: "Sequence", m: "ack", p: "TCP" }] }] };
    fireEvent.click(screen.getByRole("button", { name: "专家信息" }));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("ack")).toBeInTheDocument(); // 面板数据就绪

    // 点击专家条目: frame 查询必须用 tap 应答的 shard=A, 不得用本地 shard 状态(B)
    fireEvent.click(screen.getByText("ack"));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const calls = frameCalls();
    expect(calls.length).toBeGreaterThanOrEqual(1);
    const q = new URL(calls[calls.length - 1], "http://localhost").searchParams;
    expect(q.get("shard")).toBe("a.pcapng");
    expect(q.get("num")).toBe("9");
  });

  it("专家面板降级: tap 应答无 shard(实时实例未载入)点击条目不发 frame 查询, 透出提示", async () => {
    await mountOpen([wire(1)]); // shard 状态有值, 但 tap 应答未载入分片
    tapReply = { taps: [{ tap: "expert", type: "expert", details: [{ f: 1, s: "Error", g: "Sequence", m: "ack", p: "TCP" }] }] };
    fireEvent.click(screen.getByRole("button", { name: "专家信息" }));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    await act(async () => { await Promise.resolve(); });
    expect(screen.getByText("ack")).toBeInTheDocument();
    fireEvent.click(screen.getByText("ack"));
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(frameCalls()).toHaveLength(0); // shard 为空不查询
    expect(screen.getByText(/无法定位帧/)).toBeInTheDocument();
  });

  it("导出 = spool 全部合并单一 pcapng(服务端拼接), 走 /api/download", async () => {
    const ws = await mountOpen();
    fireEvent.click(screen.getByRole("button", { name: /导出全部历史/ }));
    expect(mockedDownload).toHaveBeenCalled();
    expect(rpcRequests(ws)).toEqual([]);
  });
});
