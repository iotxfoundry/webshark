import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import { ErrorBoundary } from "./components/ErrorBoundary";

// 全站唯一字体 Fira Code(body/表格统一等宽), 自托管, 离线可用;
// 中文不在 Fira Code 覆盖范围, 走系统 fallback; 连字(-> != 等)全站保留.
import "@fontsource/fira-code/400.css";
import "@fontsource/fira-code/600.css";

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <ErrorBoundary>
      <App />
    </ErrorBoundary>
  </React.StrictMode>,
);
