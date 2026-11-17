// theme.ts - webshark 品牌(Fluent BrandVariants): 沿用工业深色蓝(数据蓝绿 + 状态绿),
// 深色为默认主题, 浅色主题保留(DESIGN.md 色板).
import { createDarkTheme, createLightTheme } from "@fluentui/react-components";
import type { BrandVariants, Theme } from "@fluentui/react-components";

export const websharkBrand: BrandVariants = {
  10: "#060C14",
  20: "#0C1A2B",
  30: "#0E2840",
  40: "#0F3456",
  50: "#10416C",
  60: "#114E83",
  70: "#125C9A",
  80: "#136AB2",
  90: "#1A78CA",
  100: "#2286E2",
  110: "#3A94E8",
  120: "#52A2EE",
  130: "#6AB0F3",
  140: "#82BEF8",
  150: "#9ACCFC",
  160: "#B2DAFE",
};

export const websharkDarkTheme: Theme = createDarkTheme(websharkBrand);
export const websharkLightTheme: Theme = createLightTheme(websharkBrand);

// 全站唯一字体（main.tsx 自托管 @fontsource/fira-code）：Fluent 组件（Tab、
// 按钮、菜单等界面件）默认用主题字体栈（Segoe UI 系），会与数据区 Fira Code
// 割裂——覆盖主题 fontFamily 统一为 Fira Code；中文不在其覆盖范围，续接系统
// 无衬线 fallback。
for (const t of [websharkDarkTheme, websharkLightTheme]) {
  t.fontFamilyBase = '"Fira Code", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif';
}
