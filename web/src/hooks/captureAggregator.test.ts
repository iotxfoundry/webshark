// captureAggregator.test.ts - 跨分片增量聚合器(纯函数): 跨分片连续编号(只增不重排) /
// ref 复合键去重 / reset 清空重启 / 上限 10 万截断最老一半(保留行 no 不变) / fg/bg 服务端透传.
import { describe, it, expect } from "vitest";
import { CAPTURE_ROW_CAP, createCaptureAggregator, type CaptureRowWire } from "./captureAggregator";

function wire(num: number, shard = "a.pcapng", extra: Partial<CaptureRowWire> = {}): CaptureRowWire {
  return { c: [String(num), "0.000000", "a", "b", "TCP", "60", "info"], num, ref: { shard, num }, ...extra };
}

describe("createCaptureAggregator", () => {
  it("跨分片连续编号: no 自增只增不重排, ref 透传为隐藏查询键, 原始字段原样保留", () => {
    const agg = createCaptureAggregator();
    agg.append([wire(5), wire(6)], "a.pcapng");
    agg.append([wire(1, "b.pcapng")], "b.pcapng");
    const v = agg.view();
    expect(v.map((r) => r.no)).toEqual([1, 2, 3]); // 跨分片连续递增
    expect(v.map((r) => r.ref)).toEqual([
      { shard: "a.pcapng", num: 5 },
      { shard: "a.pcapng", num: 6 },
      { shard: "b.pcapng", num: 1 },
    ]);
    expect(v[0].num).toBe(5);
    expect(v[0].c).toEqual(["5", "0.000000", "a", "b", "TCP", "60", "info"]);
  });

  it("重复 ref 去重: 同一行重推只保留首次分配的 no(无重)", () => {
    const agg = createCaptureAggregator();
    agg.append([wire(1), wire(2)], "a.pcapng");
    agg.append([wire(1), wire(2), wire(3)], "a.pcapng"); // 重放边界重叠
    expect(agg.view().map((r) => r.no)).toEqual([1, 2, 3]);
    expect(agg.size()).toBe(3);
  });

  it("reset: 清空全部, 编号从 1 重启(服务端全量重推对齐)", () => {
    const agg = createCaptureAggregator();
    agg.append([wire(1), wire(2)], "a.pcapng");
    agg.reset();
    expect(agg.size()).toBe(0);
    expect(agg.view()).toEqual([]);
    agg.append([wire(9, "z.pcapng")], "z.pcapng");
    expect(agg.view().map((r) => r.no)).toEqual([1]);
  });

  it(`上限截断: 达 ${CAPTURE_ROW_CAP} 一次性截断最老一半, 保留行 no 不变(跳号即原聚合序号)`, () => {
    const agg = createCaptureAggregator(10);
    agg.append(Array.from({ length: 9 }, (_, i) => wire(i + 1)), "a.pcapng");
    expect(agg.size()).toBe(9); // 未达上限不截
    agg.append([wire(10)], "a.pcapng");
    expect(agg.size()).toBe(5); // 达上限 → 截最老 5, 留最新 5
    expect(agg.view().map((r) => r.no)).toEqual([6, 7, 8, 9, 10]);
    agg.append([wire(11)], "a.pcapng");
    expect(agg.size()).toBe(6); // 未再达上限不截
    expect(agg.view().map((r) => r.no)).toEqual([6, 7, 8, 9, 10, 11]);
  });

  it("截断后同 ref 重推可重新入列(键随截断淘汰, 新 no 追加)", () => {
    const agg = createCaptureAggregator(10);
    agg.append(Array.from({ length: 12 }, (_, i) => wire(i + 1)), "a.pcapng");
    agg.append([wire(1)], "a.pcapng");
    expect(agg.size()).toBe(8);
    expect(agg.view().at(-1)?.ref).toEqual({ shard: "a.pcapng", num: 1 });
    expect(agg.view().at(-1)?.no).toBe(13); // 新分配, 不复活旧 no
  });

  it("行颜色服务端唯一真相: fg/bg 原样透传, 无色行字段缺省(前端零配色逻辑)", () => {
    const agg = createCaptureAggregator();
    agg.append([wire(1, "a", { fg: "ff0000", bg: "00ff00" }), wire(2)], "a.pcapng");
    const v = agg.view();
    expect(v[0].fg).toBe("ff0000");
    expect(v[0].bg).toBe("00ff00");
    expect("fg" in v[1]).toBe(false);
    expect("bg" in v[1]).toBe(false);
  });
});
