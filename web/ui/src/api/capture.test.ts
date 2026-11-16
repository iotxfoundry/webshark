// capture.test.ts - HTTP API 归一化: complete field/pref 合并按 f 去重(field 与 pref
// 对同一前缀可能同时返回同名项, 如 prefix=icmp 时两分支均含 icmp)
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { completeCaptureFilter } from "./capture";
import { setToken } from "./token";

describe("completeCaptureFilter", () => {
  beforeEach(() => {
    localStorage.clear();
    setToken("t");
  });
  afterEach(() => {
    localStorage.clear();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("field 与 pref 拼接后按 f 去重(保留首次出现)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      ({
        ok: true, status: 200,
        json: async () => ({
          field: [{ f: "icmp", n: "Internet Control Message Protocol" }, { f: "icmp.type", t: "FT_UINT8" }],
          pref: [{ f: "icmp", d: "ICMP" }, { f: "icmp.checksum", d: "Checksum" }],
        }),
      }) as Response));
    const r = await completeCaptureFilter("icmp");
    expect(r.map((x) => x.f)).toEqual(["icmp", "icmp.type", "icmp.checksum"]);
    expect(r).toHaveLength(3);
  });

  it("空应答/缺省分支归一为空数组", async () => {
    vi.stubGlobal("fetch", vi.fn(async () =>
      ({ ok: true, status: 200, json: async () => ({}) }) as Response));
    expect(await completeCaptureFilter("x")).toEqual([]);
  });
});
