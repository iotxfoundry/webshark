// base.ts - 应用挂载前缀(反代子路径形态: location.pathname 恒为挂载目录, 以 / 结尾).
// 反代子路径(如 https://host/webshark/)下 fetch/WS 均按前缀拼接, 与 vite base './' 配套.

/** 挂载前缀: 根部署为 "/", 子路径挂载为 "/xxx/" */
export const BASE: string = (() => {
  const p = window.location.pathname;
  return p.endsWith("/") ? p : p + "/";
})();

/** 为根绝对路径补挂载前缀("/x" → "<BASE>x") */
export function withBase(path: string): string {
  return BASE + path.replace(/^\//, "");
}
