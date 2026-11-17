// severity.ts - 专家诊断级别文本分类(sharkd s 串子串判定, 优先级 error > warn > chat):
// ExpertPanel 徽章分组与 CaptureTreeView 树行着色共用同一判定
export type SeverityKind = "error" | "warn" | "chat";

export function severityKind(s: string): SeverityKind | undefined {
  const k = s.toLowerCase();
  if (k.includes("error")) return "error";
  if (k.includes("warn")) return "warn";
  if (k.includes("chat")) return "chat";
  return undefined;
}
