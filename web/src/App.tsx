// App.tsx - webshark 单页外壳: 顶部工具栏(标题/主题切换/sharkd 状态/token 管理) + 报文分析页.
// 无路由(单页单视图, 契约 §6)、无侧边栏; FluentProvider 持深色(默认)/浅色两主题.
// 401 → setUnauthorizedHandler 触发 → TokenGate 录入页覆盖.
import { useEffect, useState } from "react";
import { FluentProvider, Button, Badge, Text, Tooltip, makeStyles, tokens } from "@fluentui/react-components";
import { WeatherMoon16Regular, WeatherSunny16Regular, Key16Regular } from "@fluentui/react-icons";
import { useThemeStore } from "./stores/theme";
import { websharkDarkTheme, websharkLightTheme } from "./styles/theme";
import { setUnauthorizedHandler } from "./api/client";
import { getToken } from "./api/token";
import { fetchCaptureStatus, type CaptureStatus } from "./api/capture";
import CapturePage from "./pages/CapturePage";
import TokenGate from "./components/TokenGate";
import "./styles/global.css";

const useStyles = makeStyles({
  // 100dvh: 直接撑满视口, 不依赖 #root/FluentProvider 的百分比高度链
  shell: { display: "flex", flexDirection: "column", height: "100dvh", minHeight: 0 },
  topbar: {
    display: "flex", alignItems: "center", gap: tokens.spacingHorizontalS,
    padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalL}`,
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`,
    background: tokens.colorNeutralBackground2, flexShrink: 0,
  },
  brand: { display: "flex", alignItems: "baseline", gap: tokens.spacingHorizontalS },
  main: { flex: 1, minHeight: 0, padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalL}`, display: "flex", flexDirection: "column" },
});

export default function App() {
  const styles = useStyles();
  const { theme, toggle } = useThemeStore();
  const [locked, setLocked] = useState(false);
  const [status, setStatus] = useState<CaptureStatus | null>(null);

  // 主题同步到根节点 data-theme, 供全局 CSS(滚动条等)跟随明暗主题
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  // 401 → token 录入页(清 token 已在 readJSON 内完成)
  useEffect(() => {
    setUnauthorizedHandler(() => setLocked(true));
    return () => setUnauthorizedHandler(() => undefined);
  }, []);

  // sharkd 可达性 + spool 分片数(顶栏状态展示); 401 由 readJSON 管线兜底, 其余失败静默
  useEffect(() => {
    if (locked) return;
    let alive = true;
    const poll = () => {
      fetchCaptureStatus()
        .then((s) => { if (alive) setStatus(s); })
        .catch(() => { if (alive) setStatus({ connected: false, files: [], reason: "网络不可达" }); });
    };
    poll();
    const t = window.setInterval(poll, 5000);
    return () => { alive = false; window.clearInterval(t); };
  }, [locked]);

  const fileCount = status?.files.length ?? 0;
  return (
    <FluentProvider theme={theme === "dark" ? websharkDarkTheme : websharkLightTheme}>
      {locked ? (
        <TokenGate />
      ) : (
        <div className={styles.shell}>
          <div className={styles.topbar}>
            <div className={styles.brand}>
              <Text weight="semibold" size={500}>webshark</Text>
              <Text size={200} style={{ color: tokens.colorNeutralForeground3 }}>报文分析</Text>
            </div>
            <Badge appearance="tint" color={status?.connected ? "success" : "danger"}
              title={status?.connected ? "sharkd 已连接" : `sharkd 不可达${status?.reason ? `: ${status.reason}` : ""}`}>
              {status?.connected ? "sharkd 在线" : "sharkd 离线"}
            </Badge>
            <Badge appearance="tint" color="informative" title="spool 分片数">{fileCount} 分片</Badge>
            <div style={{ flex: 1 }} />
            <Tooltip content={getToken() === "" ? "设置访问令牌" : "更换访问令牌"} relationship="label">
              <Button appearance="subtle" size="small" icon={<Key16Regular />}
                aria-label="访问令牌" onClick={() => setLocked(true)} />
            </Tooltip>
            <Tooltip content={theme === "dark" ? "切换浅色主题" : "切换深色主题"} relationship="label">
              <Button appearance="subtle" size="small"
                icon={theme === "dark" ? <WeatherSunny16Regular /> : <WeatherMoon16Regular />}
                aria-label="切换主题" onClick={toggle} />
            </Tooltip>
          </div>
          <div className={styles.main}>
            <CapturePage />
          </div>
        </div>
      )}
    </FluentProvider>
  );
}
