// ErrorBoundary.tsx - 渲染期异常兜底(React 仅类组件可做错误边界): 子树抛错时整树卸载白屏,
// 此处捕获并渲染全屏提示(标题/错误信息/重载按钮), 与 App shell/TokenGate 同款 100dvh 占满视口.
import React from "react";
import { Button, makeStyles, Text, tokens } from "@fluentui/react-components";
import { ArrowClockwise16Regular } from "@fluentui/react-icons";
import { monoStyle } from "../styles/mono";

const useStyles = makeStyles({
  root: {
    height: "100dvh", display: "flex", flexDirection: "column",
    alignItems: "center", justifyContent: "center", gap: tokens.spacingVerticalM,
    padding: tokens.spacingVerticalXXL,
  },
  msg: { ...monoStyle, color: tokens.colorNeutralForeground3, overflowWrap: "anywhere", maxWidth: "80ch" },
});

/** 渲染期异常兜底边界: 捕获子树错误 → 全屏 fallback(避免白屏), 重载走整页刷新 */
export class ErrorBoundary extends React.Component<{ children: React.ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null };

  static getDerivedStateFromError(error: unknown) {
    return { error: error instanceof Error ? error : new Error(String(error)) };
  }

  componentDidCatch(error: unknown) {
    console.error("界面渲染出错", error);
  }

  render() {
    const { error } = this.state;
    if (error === null) return this.props.children;
    return <ErrorFallback error={error} />;
  }
}

function ErrorFallback({ error }: { error: Error }) {
  const styles = useStyles();
  return (
    <div className={styles.root}>
      <Text weight="semibold" size={500}>界面渲染出错</Text>
      <Text size={200} className={styles.msg}>{error.message}</Text>
      <Button appearance="secondary" icon={<ArrowClockwise16Regular />} onClick={() => window.location.reload()}>
        重新加载
      </Button>
    </div>
  );
}
