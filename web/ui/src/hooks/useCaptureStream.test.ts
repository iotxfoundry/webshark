// useCaptureStream.test.ts - 抓包 WS 推送消费 hook 单测(mock WS + fetch stub):
// live 握手(subscribe 先行 + ?handle=&token= 形态 + 句柄跨重连稳定) / subscribe 失败退避 /
// rows 微批追加(跨分片 no 编号 + ref 去重) / rotated / reset 全量对齐 / _ctl 暂停恢复上行 /
// fetchFrame 走 HTTP(ref 复合键路由) / check/complete/follow/tap 走 HTTP / 颜色服务端透传.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { renderHook, act } from "@testing-library/react";
import { useCaptureStream } from "./useCaptureStream";
import { ctlCaptureMsg, ctlFilterMsg } from "../api/captureStream";
import { ApiError } from "../api/client";
import { setToken } from "../api/token";
import { MockWebSocket, installMockWebSocket } from "../test/mockWebSocket";
import type { CaptureRowWire } from "./captureAggregator";

/** subscribe fetch 调用清单(URL+body+headers), 供握手断言 */
function subscribeCalls(): { url: string; body: Record<string, unknown>; auth?: string }[] {
  return (fetch as ReturnType<typeof vi.fn>).mock.calls
    .filter(([i]) => String(i).includes("/api/subscribe"))
    .map(([i, init]) => {
      const headers = (init as RequestInit).headers as Record<string, string>;
      return {
        url: String(i),
        body: JSON.parse((init as RequestInit).body as string) as Record<string, unknown>,
        auth: headers.Authorization,
      };
    });
}

/** 最近一次 WS 握手 URL 的 handle= 流句柄 */
function lastHandleParam(): string {
  const u = new URL(MockWebSocket.instances[MockWebSocket.instances.length - 1].url);
  return u.searchParams.get("handle") ?? "";
}

function wire(num: number, shard = "a.pcapng", extra: Partial<CaptureRowWire> = {}): CaptureRowWire {
  return { c: [String(num), "0.000000", "a", "b", "TCP", "60", `info ${num}`], num, ref: { shard, num }, ...extra };
}

describe("useCaptureStream", () => {
  let restoreWS: () => void;
  /** subscribe 失败注入(下一轮调用起生效, 用后自动复位成功) */
  let subscribeFailOnce = false;
  /** subscribe 应答 cold=true 注入(服务端淘汰后重建会话) */
  let subscribeCold = false;
  /** check 400 错误文案注入(非空时 check 应答 400+该文案) */
  let checkError = "";
  beforeEach(() => {
    vi.useFakeTimers();
    subscribeCold = false;
    restoreWS = installMockWebSocket();
    setToken("tk-test");
    // subscribe stub: 恒成功(失败注入由用例控制); 其余 URL 一律 throw 暴露意外请求
    vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.includes("/api/subscribe")) {
        if (subscribeFailOnce) { subscribeFailOnce = false; return { ok: false, status: 429, json: async () => ({ code: 429, message: "流数达上限" }) } as Response; }
        return { ok: true, status: 200, json: async () => ({ ok: true, handle: "st-x", paused: false, filter: "", shard: "a.pcapng", cold: subscribeCold }) } as Response;
      }
      if (url.includes("/api/frame")) {
        const q = new URL(url, "http://localhost").searchParams; // fetch 相对 BASE_URL, 补解析基址
        return {
          ok: true, status: 200,
          json: async () => ({ tree: [{ l: `frame ${q.get("shard")}#${q.get("num")}` }], bytes: "AAE=" }),
        } as Response;
      }
      if (url.includes("/api/check")) {
        if (checkError !== "") {
          return { ok: false, status: 400, json: async () => ({ code: -1, message: checkError }) } as unknown as Response;
        }
        return { ok: true, status: 200, json: async () => ({ status: "OK" }) } as Response;
      }
      if (url.includes("/api/complete")) {
        return {
          ok: true, status: 200,
          json: async () => ({ field: [{ f: "tcp.port", d: "TCP port" }], pref: [{ f: "icmp", n: "ICMP" }] }),
        } as Response;
      }
      if (url.includes("/api/follow")) {
        return { ok: true, status: 200, json: async () => ({ shost: "a", sport: "1", sbytes: 2, payloads: [{ n: 1, d: "AA==" }] }) } as Response;
      }
      if (url.includes("/api/tap")) {
        return { ok: true, status: 200, json: async () => ({ taps: [{ tap: "expert", type: "expert", details: [] }] }) } as Response;
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

  async function mount() {
    const h = renderHook(() => useCaptureStream());
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    act(() => { ws.open(); });
    act(() => { ws.push({ type: "hello", mode: "live" }); });
    expect(h.result.current.conn).toBe("open");
    return { h, ws };
  }

  it("live 握手形态: subscribe 先行(POST body 携 handle, Bearer 头) → WS ?handle=<句柄>&token=<token>", async () => {
    await mount();
    const subs = subscribeCalls();
    expect(subs).toHaveLength(1);
    expect((subs[0].body.handle as string).length).toBeGreaterThan(0); // 非空句柄
    expect(subs[0].auth).toBe("Bearer tk-test"); // fetch 携 Bearer
    const u = new URL(MockWebSocket.instances[0].url);
    expect(u.pathname).toMatch(/\/api\/ws$/);
    expect(u.searchParams.get("token")).toBe("tk-test"); // WS 握手 ?token=
    expect(u.searchParams.get("handle")).toBe(subs[0].body.handle as string); // handle = subscribe 同一句柄
  });

  it("subscribe 失败: 不建 WS, conn=closed, 退避后重试 subscribe 再连", async () => {
    subscribeFailOnce = true;
    const h = renderHook(() => useCaptureStream());
    // 仅排干微任务(subscribe 失败路径), 不推进定时器(退避重试由用例显式触发)
    await act(async () => { await Promise.resolve(); });
    expect(subscribeCalls()).toHaveLength(1);
    expect(MockWebSocket.instances).toHaveLength(0); // 不建 WS
    expect(h.result.current.conn).toBe("closed");
    // 退避 1s → 重试 subscribe(成功) → WS 建立并 open
    act(() => { vi.advanceTimersByTime(1100); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(subscribeCalls()).toHaveLength(2);
    expect(MockWebSocket.instances).toHaveLength(1);
    const ws = MockWebSocket.instances[0];
    act(() => { ws.open(); });
    expect(h.result.current.conn).toBe("open");
  });

  it("断线重连: 同一流句柄先 subscribe 再连(幂等保活), 续推聚合器保留", async () => {    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1)] }); });
    const handleBefore = lastHandleParam();
    act(() => { ws.close(); });
    act(() => { vi.advanceTimersByTime(1100); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    expect(subscribeCalls()).toHaveLength(2); // 重连同样 subscribe 先行
    const ws2 = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    expect(new URL(ws2.url).searchParams.get("handle")).toBe(handleBefore); // 句柄跨重连稳定
    act(() => { ws2.open(); });
    act(() => { ws2.push({ type: "hello", mode: "live" }); });
    expect(h.result.current.conn).toBe("open");
    act(() => { ws2.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2)] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2]); // 旧行保留 + 重叠去重
  });

  it("会话被淘汰重建(subscribe cold=true): onEvent 收 reset(session-recreated), 聚合器清空防静默断层", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2)] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2]);
    // 重连时服务端已淘汰旧会话并新建(cold=true, 锚定最新分片不重放历史)
    subscribeCold = true;
    act(() => { ws.close(); });
    act(() => { vi.advanceTimersByTime(1100); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const ws2 = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    act(() => { ws2.open(); });
    // 聚合器已被 reset 清空: 旧行不残留
    expect(h.result.current.rows).toEqual([]);
    // 新会话续推: 从 1 重编号(旧行保留会静默跳段)
    act(() => { ws2.push({ type: "rows", mode: "live", shard: "z.pcapng", rows: [wire(1, "z.pcapng")] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1]);
  });

  it("error 帧: setError(reason) 进横幅管线; 已提交筛选时撤乐观标识(filter 态回退)", async () => {
    const { h, ws } = await mount();
    act(() => { h.result.current.applyFilter("tcp.port == 502"); });
    expect(h.result.current.filter).toBe("tcp.port == 502");
    // 筛选失败发 error 帧: 横幅出现 + 乐观「筛选中」回退
    act(() => { ws.push({ type: "error", reason: "筛选失败: 语法错" }); });
    expect(h.result.current.error).toBe("筛选失败: 语法错");
    expect(h.result.current.filter).toBe("");
    // 无筛选时的 error(建流失败)只上横幅, 不动 filter
    act(() => { ws.push({ type: "error", reason: "建流失败: 流数达上限" }); });
    expect(h.result.current.error).toBe("建流失败: 流数达上限");
    expect(h.result.current.filter).toBe("");
    expect(h.result.current.rows).toEqual([]);
  });

  it("rows 微批追加: ref 透传 + no 跨分片连续编号", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(5), wire(6)] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2]);
    expect(h.result.current.rows[0].ref).toEqual({ shard: "a.pcapng", num: 5 });
    // 新分片继续追加(编号连续, 不清不重排)
    act(() => { ws.push({ type: "rows", mode: "live", shard: "b.pcapng", rows: [wire(1, "b.pcapng")] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3]);
  });

  it("单条 WS 消息多行 JSON Lines 多事件逐条消费", async () => {
    const { h, ws } = await mount();
    act(() => {
      ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1)] });
      ws.push({ type: "rotated", shard: "b.pcapng" });
    });
    act(() => {
      ws.onmessage?.({ data: `${JSON.stringify({ type: "rows", mode: "live", shard: "b.pcapng", rows: [wire(1, "b.pcapng")] })}\n${JSON.stringify({ type: "rows", mode: "live", shard: "b.pcapng", rows: [wire(2, "b.pcapng")] })}` });
    });
    await act(async () => { await Promise.resolve(); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3]);
  });

  it("重复 ref 去重(重放边界重叠不产生重复行/重复号)", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2)] }); });
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(2), wire(3)] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3]);
  });

  it("rotated 信息性: 不改变已收行", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1)] }); });
    act(() => { ws.push({ type: "rotated", shard: "b.pcapng" }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1]);
  });

  it("reset: 清空聚合器, 后续服务端全量重推从 no=1 重建对齐", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2)] }); });
    act(() => { ws.push({ type: "reset", reason: "overflow" }); });
    expect(h.result.current.rows).toEqual([]);
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2), wire(3)] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3]); // 全量对齐, 无残留旧行
  });

  it("_ctl 上行: 暂停/恢复发 {type:_ctl,op:capture,state}", async () => {
    const { h, ws } = await mount();
    act(() => { h.result.current.setCapturePaused(true); });
    expect(ws.sent[ws.sent.length - 1]).toBe(ctlCaptureMsg("pause"));
    act(() => { h.result.current.setCapturePaused(false); });
    expect(ws.sent[ws.sent.length - 1]).toBe(ctlCaptureMsg("resume"));
  });

  it("_ctl 未连接时忽略(不崩不误发), 重连后可发", async () => {
    const h = renderHook(() => useCaptureStream());
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const ws = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    expect(() => act(() => { h.result.current.setCapturePaused(true); })).not.toThrow();
    act(() => { h.result.current.setCapturePaused(true); });
    expect(ws.sent).toEqual([]);
    act(() => { ws.open(); });
    act(() => { h.result.current.setCapturePaused(true); });
    expect(ws.sent[ws.sent.length - 1]).toBe(ctlCaptureMsg("pause"));
  });

  it("行颜色服务端唯一真相: fg/bg 原样透传, 无色行不产生配色", async () => {
    const { h, ws } = await mount();
    act(() => {
      ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1, "a", { fg: "ff0000", bg: "00ff00" }), wire(2)] });
    });
    expect(h.result.current.rows[0].fg).toBe("ff0000");
    expect(h.result.current.rows[0].bg).toBe("00ff00");
    expect("fg" in h.result.current.rows[1]).toBe(false);
  });

  it("未知 type 事件忽略不崩(向前兼容)", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "future-thing", data: 1 }); });
    expect(h.result.current.rows).toEqual([]);
    expect(h.result.current.error).toBe("");
  });

  it("fetchFrame 走 HTTP(ref 复合键路由: handle+shard+num), 与 WS 推送并存", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(5)] }); });
    const p = h.result.current.fetchFrame({ shard: "a.pcapng", num: 5 });
    await act(async () => { await Promise.resolve(); });
    const frameCall = (fetch as ReturnType<typeof vi.fn>).mock.calls
      .map(([i]) => String(i)).find((u) => u.includes("/api/frame"));
    expect(frameCall).toBeDefined();
    const q = new URL(frameCall!, "http://localhost").searchParams; // fetch 相对 BASE_URL, 补解析基址
    expect(q.get("shard")).toBe("a.pcapng");
    expect(q.get("num")).toBe("5");
    expect(q.get("handle")).toBe(lastHandleParam()); // 定位到当前流
    expect(q.get("proto")).toBe("true"); // sharkd frame 瘦响应: 布尔字面量标志请求协议树/字节
    expect(q.get("bytes")).toBe("true");
    await expect(p).resolves.toMatchObject({ tree: [{ l: "frame a.pcapng#5" }] });
    // 推送行不受 HTTP 查询影响; WS 无 JSON-RPC 上行
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1]);
    expect(ws.sent.filter((l) => l.includes('"method"'))).toEqual([]);
  });

  it("filter _ctl 上行: applyFilter 发 {type:_ctl,op:filter}, 空串退出; 未连接仅存态不误发", async () => {
    const { h, ws } = await mount();
    act(() => { h.result.current.applyFilter("tcp.port == 502"); });
    expect(ws.sent[ws.sent.length - 1]).toBe(ctlFilterMsg("tcp.port == 502"));
    expect(h.result.current.filter).toBe("tcp.port == 502");
    act(() => { h.result.current.applyFilter(""); });
    expect(ws.sent[ws.sent.length - 1]).toBe(ctlFilterMsg("")); // 空串 = 退出筛选
    expect(h.result.current.filter).toBe("");
  });

  it("筛选行同一聚合器: mode:filter 微批(快照段 ref=合并文件标识)续号追加, 同 ref 重推去重保号", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2)] }); });
    // 进入筛选: 快照段行 ref.shard = 合并文件标识, 前端无感走同一聚合路径(续号)
    act(() => {
      ws.push({
        type: "rows", mode: "filter", shard: "merge_t1_001.pcapng",
        rows: [wire(5, "merge_t1_001.pcapng"), wire(7, "merge_t1_001.pcapng")],
      });
    });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3, 4]);
    expect(h.result.current.rows[2].ref).toEqual({ shard: "merge_t1_001.pcapng", num: 5 });
    // 快照段匀速重推同 ref(限速重放边界): 去重吸收, 序号保留首次分配
    act(() => {
      ws.push({ type: "rows", mode: "filter", shard: "merge_t1_001.pcapng", rows: [wire(5, "merge_t1_001.pcapng")] });
    });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3, 4]);
  });

  it("退出筛选: 普通流重推活动分片行(同 ref)被去重吸收, 不清表不重号不重复", async () => {
    const { h, ws } = await mount();
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2), wire(3)] }); });
    act(() => {
      ws.push({ type: "rows", mode: "filter", shard: "merge_t1_001.pcapng", rows: [wire(5, "merge_t1_001.pcapng")] });
    });
    // 退出筛选: 服务端重推活动分片行 → ref 去重天然吸收
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(1), wire(2), wire(3)] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3, 4]);
    expect(h.result.current.rows).toHaveLength(4);
    // 之后新行正常续号
    act(() => { ws.push({ type: "rows", mode: "live", shard: "a.pcapng", rows: [wire(4)] }); });
    expect(h.result.current.rows.map((r) => r.no)).toEqual([1, 2, 3, 4, 5]);
  });

  it("重连后 filter 重断言: conn open 即补发当前 filter _ctl(与 pause 同模式)", async () => {
    const { h, ws } = await mount();
    act(() => { h.result.current.applyFilter("tcp.port == 502"); });
    expect(ws.sent[ws.sent.length - 1]).toBe(ctlFilterMsg("tcp.port == 502"));
    // 断线重连(换新 WS)
    act(() => { ws.close(); });
    act(() => { vi.advanceTimersByTime(1100); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const ws2 = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    expect(ws2).not.toBe(ws);
    act(() => { ws2.open(); });
    // open 即重断言筛选态(服务端筛选态随流重建)
    expect(ws2.sent).toContain(ctlFilterMsg("tcp.port == 502"));
    expect(h.result.current.conn).toBe("open");
    // 清除后再重连: 清除态不发(空 filter 无需断言)
    act(() => { h.result.current.applyFilter(""); });
    act(() => { ws2.close(); });
    act(() => { vi.advanceTimersByTime(2100); });
    await act(async () => { await vi.runOnlyPendingTimersAsync(); });
    const ws3 = MockWebSocket.instances[MockWebSocket.instances.length - 1];
    act(() => { ws3.open(); });
    expect(ws3.sent.filter((l) => l.includes("filter"))).toEqual([]);
  });

  /** fetch 调用按 URL 片段过滤(相对 BASE_URL) */
  function fetchCalls(fragment: string): string[] {
    return (fetch as ReturnType<typeof vi.fn>).mock.calls.map(([i]) => String(i)).filter((u) => u.includes(fragment));
  }

  it("checkFilter 走 HTTP(POST body {filter}; 400 envelope 错误文案透传)", async () => {
    const { h, ws } = await mount();
    // 成功: {status:"OK"} 直通
    await expect(h.result.current.checkFilter("tcp.port == 80")).resolves.toEqual({ status: "OK" });
    const calls = fetchCalls("/api/check");
    expect(calls).toHaveLength(1);
    const [, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls.find(([u]) => String(u).includes("/api/check"))!;
    expect((init as RequestInit).method).toBe("POST");
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({ filter: "tcp.port == 80" });
    // 空 filter 不发请求
    await expect(h.result.current.checkFilter("")).resolves.toEqual({ status: "OK" });
    expect(fetchCalls("/api/check")).toHaveLength(1);
    // 400(envelope 错误): {code,message} JSON 约定 — ApiError.message=sharkd 原文案, code=sharkd code
    checkError = "display filter 语法错: unexpected ==";
    let caught: unknown;
    try {
      await h.result.current.checkFilter("tcp.bad ==");
    } catch (e) {
      caught = e;
    }
    expect(caught).toBeInstanceOf(ApiError);
    expect((caught as ApiError).message).toContain("display filter 语法错");
    expect((caught as ApiError).code).toBe(-1); // data.code 优先于 HTTP 状态码
    checkError = "";
    expect(ws.sent.filter((l) => l.includes('"method"'))).toEqual([]); // WS 无 JSON-RPC
  });

  it("complete 走 HTTP(POST body {prefix}; field 前 pref 后合并, 描述 d 回退 n)", async () => {
    const { h, ws } = await mount();
    await expect(h.result.current.complete("tc")).resolves.toEqual([
      { f: "tcp.port", d: "TCP port" },
      { f: "icmp", d: "ICMP", n: "ICMP" }, // 透传保留 n, 展示层只读 d
    ]);
    const calls = (fetch as ReturnType<typeof vi.fn>).mock.calls.filter(([u]) => String(u).includes("/api/complete"));
    expect(calls).toHaveLength(1);
    expect(JSON.parse((calls[0][1] as RequestInit).body as string)).toEqual({ prefix: "tc" });
    // 空前缀不发请求
    await expect(h.result.current.complete("")).resolves.toEqual([]);
    expect((fetch as ReturnType<typeof vi.fn>).mock.calls.filter(([u]) => String(u).includes("/api/complete"))).toHaveLength(1);
    expect(ws.sent.filter((l) => l.includes('"method"'))).toEqual([]);
  });

  it("fetchFollow 走 HTTP(POST body {handle, follow, filter}; 响应缺省归一)", async () => {
    const { h, ws } = await mount();
    const p = h.result.current.fetchFollow("TCP", "tcp.stream == 0");
    await act(async () => { await Promise.resolve(); });
    const [, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls.find(([u]) => String(u).includes("/api/follow"))!;
    expect((init as RequestInit).method).toBe("POST");
    const body = JSON.parse((init as RequestInit).body as string) as Record<string, unknown>;
    expect(body.follow).toBe("TCP");
    expect(body.filter).toBe("tcp.stream == 0");
    expect((body.handle as string).length).toBeGreaterThan(0); // 流句柄定位流
    await expect(p).resolves.toMatchObject({ shost: "a", sport: "1", sbytes: 2, payloads: [{ n: 1, d: "AA==" }] });
    expect(ws.sent.filter((l) => l.includes('"method"'))).toEqual([]);
  });

  it("fetchExpert 走 HTTP(POST body {handle, tap0:\"expert\"}; 响应透传)", async () => {
    const { h, ws } = await mount();
    const ep = h.result.current.fetchExpert();
    await act(async () => { await Promise.resolve(); });
    const [, init] = (fetch as ReturnType<typeof vi.fn>).mock.calls.find(([u]) => String(u).includes("/api/tap"))!;
    expect((init as RequestInit).method).toBe("POST");
    expect(JSON.parse((init as RequestInit).body as string)).toEqual({ handle: lastHandleParam(), tap0: "expert" });
    await expect(ep).resolves.toMatchObject({ taps: [{ tap: "expert" }] });
    expect(ws.sent.filter((l) => l.includes('"method"'))).toEqual([]);
  });
});
