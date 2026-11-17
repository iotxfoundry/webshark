// env.test.ts - 筛选持久化与 URL 还原(env.ts 纯函数部分)
import { beforeEach, describe, expect, it } from "vitest";
import { initialFilter, persistFilter, syncFilterUrl } from "./env";

describe("筛选保留", () => {
  beforeEach(() => {
    localStorage.clear();
    window.history.replaceState(null, "", "/");
  });

  it("初始优先级: URL ?filter= > localStorage 上次筛选 > 空", () => {
    expect(initialFilter()).toBe(""); // 两者皆空
    localStorage.setItem("webshark-filter", "tcp.port == 80");
    expect(initialFilter()).toBe("tcp.port == 80"); // 仅 localStorage
    window.history.replaceState(null, "", "/?filter=udp.port%20==%2053");
    expect(initialFilter()).toBe("udp.port == 53"); // URL 优先
  });

  it("persistFilter 落盘可读; 存储不可达不抛", () => {
    persistFilter("ip.addr == 1.2.3.4");
    expect(localStorage.getItem("webshark-filter")).toBe("ip.addr == 1.2.3.4");
    expect(initialFilter()).toBe("ip.addr == 1.2.3.4");
    persistFilter(""); // 空串=清除
    expect(localStorage.getItem("webshark-filter")).toBe("");
    expect(initialFilter()).toBe("");
  });

  it("syncFilterUrl: 应用写 ?filter=, 空串摘参, 不产生历史项", () => {
    syncFilterUrl("tcp.flags == 0x02");
    expect(window.location.search).toBe("?filter=tcp.flags+%3D%3D+0x02");
    expect(initialFilter()).toBe("tcp.flags == 0x02"); // 写入后可还原
    syncFilterUrl("");
    expect(window.location.search).toBe("");
  });
});
