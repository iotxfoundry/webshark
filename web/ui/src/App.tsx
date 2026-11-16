// App.tsx - webshark 单页外壳: FluentProvider(深色默认/浅色) + 401 管线(TokenGate 覆盖) +
// CapturePage(自含单条工具栏, 状态徽章与操作按钮一并承载). 无路由(契约 §6)、无可见 chrome:
// 支持直接打开(显示品牌)与 iframe 嵌入(隐藏品牌, 宿主系统自带头)两种形态.
import { useEffect, useState } from "react";
import { FluentProvider } from "@fluentui/react-components";
import { useThemeStore } from "./stores/theme";
import { websharkDarkTheme, websharkLightTheme } from "./styles/theme";
import { setUnauthorizedHandler } from "./api/client";
import { isEmbedded } from "./env";
import CapturePage from "./pages/CapturePage";
import TokenGate from "./components/TokenGate";
import "./styles/global.css";

export default function App() {
  const { theme } = useThemeStore();
  const [locked, setLocked] = useState(false);

  // 主题同步到根节点 data-theme, 供全局 CSS(滚动条等)跟随明暗主题
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  // 401 → token 录入页(清 token 已在 readJSON 内完成)
  useEffect(() => {
    setUnauthorizedHandler(() => setLocked(true));
    return () => setUnauthorizedHandler(() => undefined);
  }, []);

  return (
    <FluentProvider theme={theme === "dark" ? websharkDarkTheme : websharkLightTheme}>
      {locked ? <TokenGate /> : <CapturePage embedded={isEmbedded()} onOpenToken={() => setLocked(true)} />}
    </FluentProvider>
  );
}
