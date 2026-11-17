// mockWebSocket.ts - 测试共享 mock WebSocket: 记录实例与上行, 测试手动派发下行事件
export class MockWebSocket {
  static instances: MockWebSocket[] = [];
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  readyState = MockWebSocket.CONNECTING;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onclose: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  binaryType = "";
  constructor(public url: string) { MockWebSocket.instances.push(this); }
  send(line: string): void { this.sent.push(line); }
  close(): void { this.readyState = MockWebSocket.CLOSED; this.onclose?.(); }
  open(): void { this.readyState = MockWebSocket.OPEN; this.onopen?.(); }
  /** 下行推送事件(单条 WS 消息可多行 JSON Lines) */
  push(...events: unknown[]): void {
    this.onmessage?.({ data: events.map((e) => JSON.stringify(e)).join("\n") });
  }
}

/** 安装: 替换 window.WebSocket 并清空实例记录; 返回还原函数(afterEach 调用恢复原 WebSocket) */
export function installMockWebSocket(): () => void {
  const orig = window.WebSocket;
  Object.defineProperty(window, "WebSocket", { value: MockWebSocket, configurable: true, writable: true });
  MockWebSocket.instances = [];
  return () => {
    Object.defineProperty(window, "WebSocket", { value: orig, configurable: true, writable: true });
  };
}
