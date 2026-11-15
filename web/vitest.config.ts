import { defineConfig } from "vitest/config";

export default defineConfig({
  // MODE=test: 虚拟滚动组件(captureTree.isTestEnv)回退普通列表(jsdom 无真实视口测量)
  mode: "test",
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test/setup.ts"],
  },
});
