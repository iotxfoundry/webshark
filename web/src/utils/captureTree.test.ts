import { describe, it, expect } from "vitest";
import { flattenTree, topLevelKeys } from "./captureTree";
import type { SharkdTreeNode } from "../api/sharkdTypes";

const tree: SharkdTreeNode[] = [
  { l: "Ethernet II", h: [0, 14], f: "eth", n: [{ l: "Source: a", f: "eth.src", h: [6, 6] }, { l: "Type: IP", f: "eth.type", h: [12, 2] }] },
  { l: "Internet Protocol", h: [14, 20], f: "ip", n: [] },
  { l: "Data (8 bytes)" },
];

describe("flattenTree", () => {
  it("全收起: 只投影根层; 行键 = 路径段拼串(根即单段)", () => {
    const rows = flattenTree(tree, new Set());
    expect(rows).toHaveLength(3);
    expect(rows[0].depth).toBe(0);
    expect(rows[0].hasChildren).toBe(true);
    expect(rows[2].hasChildren).toBe(false);
    expect(rows[0].key).toBe("eth");
    expect(rows[1].key).toBe("ip");
    expect(rows[2].key).toBe("2"); // 无 f/fn 的节点回退兄弟下标
  });
  it("展开键(路径串)投影子级行: 缩进 +1, 子键 = 父键 + '/' + 子段", () => {
    const rows = flattenTree(tree, new Set(["eth"]));
    expect(rows.map((r) => r.node.l)).toEqual(["Ethernet II", "Source: a", "Type: IP", "Internet Protocol", "Data (8 bytes)"]);
    expect(rows[1].depth).toBe(1);
    expect(rows[1].key).toBe("eth/eth.src");
    expect(rows[2].key).toBe("eth/eth.type");
  });
  it("缺 f 回退 fn 再回退兄弟下标(路径段恒非空)", () => {
    const t: SharkdTreeNode[] = [
      { l: "a", n: [{ l: "x" }, { l: "y", fn: "mb.transaction" }] },
      { l: "b", fn: "fake.proto" },
    ];
    const rows = flattenTree(t, new Set(["0"]));
    expect(rows[0].key).toBe("0");
    expect(rows[1].key).toBe("0/0");
    expect(rows[2].key).toBe("0/mb.transaction");
    expect(rows[3].key).toBe("fake.proto"); // fn 兜底(仅 f 缺省时)
  });
});

describe("topLevelKeys", () => {
  it("有子节点的根的路径键(首屏默认展开协议层); n 空数组不算可展开", () => {
    expect(topLevelKeys(tree)).toEqual(["eth"]);
  });
});
