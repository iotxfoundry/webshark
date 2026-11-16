// App.tsx - webshark 单页外壳: FluentProvider(深色默认/浅色) + 401 管线(TokenGate 覆盖) +
// CapturePage(自含单条工具栏, 状态徽章与操作按钮一并承载). 无路由(契约 §6)、无可见 chrome:
// 支持直接打开(显示品牌)与 iframe 嵌入(隐藏品牌, 宿主系统自带头)两种形态.
// embedded: 宿主经 postMessage 注入令牌/主题(lib/embedMessaging), 401 → 通知宿主刷新令牌,
// 不弹 TokenGate(保持断连横幅/等待态); standalone: 401 → token 录入页, 现状不变.
import { useEffect, useState } from "react";
import { FluentProvider } from "@fluentui/react-components";
import { useThemeStore } from "./stores/theme";
import { websharkDarkTheme, websharkLightTheme } from "./styles/theme";
import { setUnauthorizedHandler } from "./api/client";
import { isEmbedded } from "./env";
import { initEmbedMessaging, notifyReady, notifyUnauthorized } from "./lib/embedMessaging";
import CapturePage from "./pages/CapturePage";
import TokenGate from "./components/TokenGate";
import "./styles/global.css";

export default function App() {
  const { theme } = useThemeStore();
  const [locked, setLocked] = useState(false);
  const embedded = isEmbedded();

  // 主题同步到根节点 data-theme, 供全局 CSS(滚动条等)跟随明暗主题
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  // embedded: 注册宿主协议监听 + 宣告就绪(宿主此后才发 set-*)
  useEffect(() => {
    if (!embedded) return;
    const dispose = initEmbedMessaging();
    notifyReady();
    return dispose;
  }, [embedded]);

  // 401 管线: standalone → token 录入页; embedded → 通知宿主刷新令牌重发 set-token
  useEffect(() => {
    setUnauthorizedHandler(() => {
      if (embedded) notifyUnauthorized();
      else setLocked(true);
    });
    return () => setUnauthorizedHandler(() => undefined);
  }, [embedded]);

  return (
    <FluentProvider theme={theme === "dark" ? websharkDarkTheme : websharkLightTheme}>
      {locked ? <TokenGate /> : <CapturePage embedded={embedded} onOpenToken={embedded ? undefined : () => setLocked(true)} />}
    </FluentProvider>
  );
}
