import { create } from "zustand";

type Theme = "light" | "dark";

interface ThemeStore {
  theme: Theme;
  toggle: () => void;
}

const saved = (typeof localStorage !== "undefined"
  ? localStorage.getItem("webshark-theme")
  : null) as Theme | null;

export const useThemeStore = create<ThemeStore>((set) => ({
  // 深色工业主题为默认(契约 §6)
  theme: saved === "light" ? "light" : "dark",
  toggle: () =>
    set((s) => {
      const next = s.theme === "light" ? "dark" : "light";
      // 存储禁用/写满时持久化失败不应拖垮主题切换, 忽略即可(内存态已生效).
      try { localStorage.setItem("webshark-theme", next); } catch { /* noop */ }
      return { theme: next };
    }),
}));
