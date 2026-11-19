// flowGraph.test.ts - 流图布局构建纯函数单测: 节点按首见顺序去重 / src==dst 与空端点整行
// 跳过 / limit 取末尾窗口 / 绝对日期时间截取时分秒(短串原样) / 空 rows 返回空.
import { describe, it, expect } from "vitest";
import { buildFlow, FLOW_LIMIT } from "./flowGraph";
import { COL } from "./captureColumns";
import type { CaptureViewRow } from "../hooks/captureAggregator";

let seq = 0;
/** 行构造: 按 8 列契约填 c[](下标取自 COL), 附聚合器形态的 no/ref */
function row(src: string, dst: string, proto = "TCP", info = "", abs = "2026-11-19 17:30:24.664916"): CaptureViewRow {
  seq += 1;
  const c: string[] = ["", "0.000000", abs, src, dst, proto, "60", info];
  expect(c[COL.abs]).toBe(abs); // 列契约自检: abs 下标即第 3 列
  return { c, num: seq, no: seq, ref: { shard: "a.pcapng", num: seq } };
}

describe("buildFlow", () => {
  it("多行节点按首见顺序去重, 流 n 映射节点下标", () => {
    const { nodes, flows } = buildFlow([row("a", "b"), row("b", "c"), row("a", "b", "UDP", "ack")]);
    expect(nodes).toEqual(["a", "b", "c"]);
    expect(flows.map((f) => f.n)).toEqual([[0, 1], [1, 2], [0, 1]]);
    expect(flows[0].c).toBe("TCP"); // info 空 → 仅协议
    expect(flows[2].c).toBe("UDP · ack"); // info 非空 → 协议 · info
    expect(flows[2].no).toBe(3);
    expect(flows[2].ref).toEqual({ shard: "a.pcapng", num: 3 });
  });

  it("src==dst 与空端点行整行跳过(不产节点不入流)", () => {
    const { nodes, flows } = buildFlow([row("a", "a"), row("", "b"), row("a", ""), row("a", "b")]);
    expect(nodes).toEqual(["a", "b"]);
    expect(flows).toHaveLength(1);
    expect(flows[0].n).toEqual([0, 1]);
  });

  it("limit 取末尾窗口: 旧行节点/流一并淘汰, 默认上限 FLOW_LIMIT 全量", () => {
    seq = 0; // 本用例断言聚合行号, 复位全局计数器
    const rows = [row("x", "y"), row("a", "b"), row("b", "c")];
    const { nodes, flows } = buildFlow(rows, 2);
    expect(nodes).toEqual(["a", "b", "c"]);
    expect(flows.map((f) => f.no)).toEqual([2, 3]);
    expect(buildFlow(rows, FLOW_LIMIT)).toEqual(buildFlow(rows));
  });

  it("时间串: 绝对日期时间取 slice(11) 时分秒, 短串原样透传", () => {
    const { flows } = buildFlow([row("a", "b"), row("a", "b", "TCP", "", "12.345")]);
    expect(flows[0].t).toBe("17:30:24.664916");
    expect(flows[1].t).toBe("12.345");
  });

  it("空 rows 返回空节点空流", () => {
    expect(buildFlow([])).toEqual({ nodes: [], flows: [] });
  });
});
