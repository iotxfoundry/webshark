// theme.ts - 主题 store(zustand): standalone 持久化 localStorage + 切换钮;
// embedded(iframe 嵌入)由宿主注入(初始 ?theme= URL 参数 + postMessage set-theme),
// 主题归宿主管, 不持久化. 初始优先级: URL ?theme=(embedded)> localStorage(standalone)> dark.
import { create } from "zustand";
import { isEmbedded } from "../env";

type Theme = "light" | "dark";

interface ThemeStore {
  theme: Theme;
  /** set-theme 协议入口(embedded, 不持久化); 与 toggle 同语义(立即切换) */
  setTheme: (t: Theme) => void;
  toggle: () => void;
}

// URL ?theme= 仅在 embedded 下生效(防宿主浅色主题下闪深色: 首帧渲染前即生效)
const urlTheme =
  isEmbedded() && new URLSearchParams(window.location.search).get("theme")
const saved = (typeof localStorage !== "undefined"
  ? localStorage.getItem("webshark-theme")
  : null) as Theme | null;

function persist(next: Theme): void {
  // embedded 下持久化不可达(按钮已隐藏); isEmbedded 守卫防回归(主题归宿主管不落盘)
  if (isEmbedded()) return;
  // 存储禁用/写满时持久化失败不应拖垮主题切换, 忽略即可(内存态已生效).
  try { localStorage.setItem("webshark-theme", next); } catch { /* noop */ }
}

export const useThemeStore = create<ThemeStore>((set, get) => ({
  // 深色工业主题为默认(契约 §6)
  theme: urlTheme === "light" || urlTheme === "dark" ? urlTheme : saved === "light" ? "light" : "dark",
  setTheme: (t) =>
    set(() => {
      persist(t);
      return { theme: t };
    }),
  toggle: () => get().setTheme(get().theme === "light" ? "dark" : "light"),
}));
