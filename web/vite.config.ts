import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// webshark 前端构建: base './' (任意反代子路径下资源相对自洽) + outDir dist(Go //go:embed all:dist 内嵌).
// dev server 代理 /api 到本地 webshark 服务端(WEBSHARK_DEV_TARGET 可覆盖), ws 升级放行(/api/ws).
export default defineConfig({
  plugins: [react()],
  base: "./",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    target: "es2022",
    rollupOptions: {
      output: {
        // 按依赖稳定性分组拆 vendor, 利于长期缓存(业务迭代不触发 vendor 重下).
        // 必须按"包名"精确归类, 不能用 id.includes("/react/") 子串匹配(嵌套依赖误判).
        manualChunks(id) {
          if (!id.includes("node_modules")) return undefined;
          const rest = id.slice(id.lastIndexOf("node_modules/") + "node_modules/".length);
          const pkg = rest.startsWith("@")
            ? rest.split("/").slice(0, 2).join("/")
            : rest.split("/")[0];

          // @griffel 是 Fluent UI 的样式引擎, 仅被 @fluentui 使用, 归入同组.
          if (pkg.startsWith("@fluentui/") || pkg.startsWith("@griffel/")) return "fluentui";
          if (pkg === "react" || pkg === "react-dom" || pkg === "scheduler" ||
            pkg === "react-is" || pkg === "use-sync-external-store" ||
            pkg === "loose-envify" || pkg === "js-tokens") {
            return "react";
          }
          return "vendor";
        },
      },
    },
  },
  server: {
    port: 5175,
    proxy: {
      "/api": {
        target: process.env.WEBSHARK_DEV_TARGET ?? "http://127.0.0.1:8080",
        changeOrigin: true,
        ws: true, // /api/ws 升级放行
      },
    },
  },
  resolve: {
    alias: { "@": "/src" },
  },
});
