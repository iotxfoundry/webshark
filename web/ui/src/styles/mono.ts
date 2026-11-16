import { makeStyles, tokens } from "@fluentui/react-components";
import type { GriffelStyle } from "@fluentui/react-components";

// 全站数据等宽样式唯一事实源(DESIGN.md「Data 12px 等宽」): 独立散落的 mono Griffel 类统一引用这里,
// 回落链与 global.css 的 .mono 一致(Fira Code → Cascadia Code → JetBrains Mono)
export const monoStyle: GriffelStyle = {
  fontFamily: '"Fira Code", "Cascadia Code", "JetBrains Mono", monospace',
  fontSize: tokens.fontSizeBase200,
};

export const useMonoStyles = makeStyles({ mono: monoStyle });
