// env.ts - 运行环境探测. 模块化 helper 便于测试注入(vi.mock), 生产代码零测试开关.

/** iframe 嵌入判定: 跨域下读取 window.top 引用安全(只是不能深入访问其内容) */
export function isEmbedded(): boolean {
  return window.self !== window.top;
}

/** 初始筛选条件: URL ?filter=(显式注入优先) > 上次筛选(localStorage) > 空; 刷新/重开均保留 */
export function initialFilter(): string {
  const fromUrl = new URLSearchParams(window.location.search).get("filter");
  if (fromUrl !== null) return fromUrl;
  // 存储禁用/不可达时静默回空(内存态照常工作)
  try {
    return localStorage.getItem("webshark-filter") ?? "";
  } catch {
    return "";
  }
}

/** 上次筛选持久化(刷新/重开网页后还原; 空串=清除); 存储失败不拖垮筛选本身 */
export function persistFilter(f: string): void {
  try {
    localStorage.setItem("webshark-filter", f);
  } catch { /* noop */ }
}

/** 筛选条件同步进地址栏(replaceState 不产生历史项): 刷新/分享凭 URL 还原; 空串摘除参数 */
export function syncFilterUrl(f: string): void {
  const u = new URL(window.location.href);
  if (f === "") u.searchParams.delete("filter");
  else u.searchParams.set("filter", f);
  window.history.replaceState(null, "", u);
}
