// env.ts - 运行环境探测. 模块化 helper 便于测试注入(vi.mock), 生产代码零测试开关.

/** iframe 嵌入判定: 跨域下读取 window.top 引用安全(只是不能深入访问其内容) */
export function isEmbedded(): boolean {
  return window.self !== window.top;
}
