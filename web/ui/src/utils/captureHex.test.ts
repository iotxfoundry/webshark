import { describe, it, expect } from "vitest";
import { decodeBase64, toHexRows, byteHex, byteAscii, nodeSpans } from "./captureHex";

describe("decodeBase64", () => {
  it("解码 base64 字节", () => {
    // "SGVsbG8=" = "Hello"
    expect([...decodeBase64("SGVsbG8=")]).toEqual([0x48, 0x65, 0x6c, 0x6c, 0x6f]);
  });
  it("空/undefined/非法输入返回空数组", () => {
    expect(decodeBase64(undefined).length).toBe(0);
    expect(decodeBase64("").length).toBe(0);
    expect(decodeBase64("!!!非法!!!").length).toBe(0);
  });
});

describe("toHexRows", () => {
  it("16 字节一行, 尾行不足 16", () => {
    const bytes = new Uint8Array(35);
    const rows = toHexRows(bytes);
    expect(rows).toHaveLength(3);
    expect(rows[0].offset).toBe(0);
    expect(rows[1].offset).toBe(16);
    expect(rows[2].offset).toBe(32);
    expect(rows[2].bytes).toHaveLength(3);
  });
  it("空字节 → 无行", () => {
    expect(toHexRows(new Uint8Array(0))).toHaveLength(0);
  });
});

describe("byteHex/byteAscii", () => {
  it("两位小写 hex 与 ASCII 可视化", () => {
    expect(byteHex(0x0a)).toBe("0a");
    expect(byteHex(0xff)).toBe("ff");
    expect(byteAscii(0x41)).toBe("A");
    expect(byteAscii(0x00)).toBe(".");
    expect(byteAscii(0x7f)).toBe(".");
  });
});

describe("nodeSpans", () => {
  it("h/i 区间转 span, len=0 省略", () => {
    expect(nodeSpans([4, 2], [10, 3])).toEqual([{ start: 4, end: 6 }, { start: 10, end: 13 }]);
    expect(nodeSpans(undefined, undefined)).toEqual([]);
    expect(nodeSpans([0, 0], undefined)).toEqual([]);
  });
});
