// captureStream.test.ts - 抓包 WS 推送线格式(服务端→前端下行事件解析 + 前端→服务端 _ctl 上行):
// 契约形态钉死(docs/api.md §3: hello/rows/rotated/reset/error + _ctl), 防前后端漂移.
import { describe, it, expect } from "vitest";
import { ctlCaptureMsg, ctlFilterMsg, parseCaptureLine } from "./captureStream";

describe("parseCaptureLine", () => {
  it("rows 事件: mode/shard/rows 数组, 行内 ref/fg/bg 透传", () => {
    const line = JSON.stringify({
      type: "rows", mode: "live", shard: "net_00001_20260101120000.pcapng",
      rows: [{ c: ["1", "0.000000"], num: 5, ref: { shard: "net_00001_20260101120000.pcapng", num: 5 }, fg: "ff0000", bg: "00ff00" }],
    });
    const e = parseCaptureLine(line);
    expect(e).toMatchObject({ type: "rows", mode: "live", shard: "net_00001_20260101120000.pcapng" });
    if (e?.type === "rows") {
      expect(e.rows).toHaveLength(1);
      expect(e.rows[0].ref).toEqual({ shard: "net_00001_20260101120000.pcapng", num: 5 });
      expect(e.rows[0].fg).toBe("ff0000");
      expect(e.rows[0].bg).toBe("00ff00");
    }
  });

  it("hello/rotated/reset/error 事件形态", () => {
    expect(parseCaptureLine(JSON.stringify({ type: "hello", mode: "live" }))).toEqual({ type: "hello", mode: "live" });
    expect(parseCaptureLine(JSON.stringify({ type: "rotated", shard: "net_00002.pcapng" }))).toEqual({ type: "rotated", shard: "net_00002.pcapng" });
    expect(parseCaptureLine(JSON.stringify({ type: "reset", reason: "overflow" }))).toEqual({ type: "reset", reason: "overflow" });
    expect(parseCaptureLine(JSON.stringify({ type: "error", reason: "筛选失败" }))).toEqual({ type: "error", reason: "筛选失败" });
  });

  it("顶层无 type 键(噪声行)→ null(传输层丢弃)", () => {
    expect(parseCaptureLine(JSON.stringify({ jsonrpc: "2.0", id: 1, result: { status: "OK" } }))).toBeNull();
    expect(parseCaptureLine(JSON.stringify({ result: { type: "rows" } }))).toBeNull(); // 嵌套 type 不误判
  });

  it("非 JSON 行与残缺行 → null", () => {
    expect(parseCaptureLine("Hello in child.")).toBeNull();
    expect(parseCaptureLine("{invalid json")).toBeNull();
    expect(parseCaptureLine("")).toBeNull();
    expect(parseCaptureLine(JSON.stringify({ noType: 1 }))).toBeNull();
  });
});

describe("ctlCaptureMsg", () => {
  it("上行 _ctl 暂停/恢复帧(契约形态: type/op/state)", () => {
    expect(JSON.parse(ctlCaptureMsg("pause"))).toEqual({ type: "_ctl", op: "capture", state: "pause" });
    expect(JSON.parse(ctlCaptureMsg("resume"))).toEqual({ type: "_ctl", op: "capture", state: "resume" });
  });
});

describe("ctlFilterMsg", () => {
  it("上行 _ctl 筛选切换帧(契约形态: type/op/filter; 空串=退出筛选)", () => {
    expect(JSON.parse(ctlFilterMsg("tcp.port == 502"))).toEqual({ type: "_ctl", op: "filter", filter: "tcp.port == 502" });
    expect(JSON.parse(ctlFilterMsg(""))).toEqual({ type: "_ctl", op: "filter", filter: "" });
  });
});
