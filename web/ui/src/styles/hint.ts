// hint.ts - 空态/占位提示共享 Griffel 样式(抽屉空态与详情面板占位的单一来源):
// hintDrawer = 抽屉全屏空态(居中 + XL 留白 + 次要灰) / hintPane = 面板占位居中基座(色与留白由调用方补) /
// hintMuted = 次要灰小字(面板行内提示).
import { tokens } from "@fluentui/react-components";
import type { GriffelStyle } from "@fluentui/react-components";

export const hintDrawer: GriffelStyle = {
  flex: 1,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
  padding: tokens.spacingVerticalXL,
  color: tokens.colorNeutralForeground3,
};

export const hintPane: GriffelStyle = {
  flex: 1,
  display: "flex",
  alignItems: "center",
  justifyContent: "center",
};

export const hintMuted: GriffelStyle = {
  color: tokens.colorNeutralForeground3,
  fontSize: tokens.fontSizeBase200,
};
