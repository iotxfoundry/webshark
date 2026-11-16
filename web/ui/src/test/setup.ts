import "@testing-library/jest-dom";

// jsdom 无 ResizeObserver: Fluent UI 部分组件(MessageBar 等)依赖, 缺失时整页崩进错误边界.
// 提供空实现兜底(布局观测在测试中无意义).
class ResizeObserverStub implements ResizeObserver {
  observe(): void {}
  unobserve(): void {}
  disconnect(): void {}
}
if (typeof globalThis.ResizeObserver === "undefined") {
  globalThis.ResizeObserver = ResizeObserverStub as unknown as typeof ResizeObserver;
}
