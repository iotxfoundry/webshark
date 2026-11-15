// TokenGate.tsx - token 录入页: 任何 401 后展示(一个输入框 + 连接按钮, 样式与主界面一致).
// 提交后写 localStorage 并整页刷新(重建全部带凭据的连接); 空提交 = 清 token 以无凭据模式连接
// (服务端未配置鉴权时后端直通).
import { useState } from "react";
import { Button, Input, makeStyles, tokens } from "@fluentui/react-components";
import { setToken } from "../api/token";

const useStyles = makeStyles({
  root: {
    // 100dvh: 父级 FluentProvider 高度为 auto, 100% 会塌缩成内容高(同 App shell 的做法)
    height: "100dvh", display: "flex", alignItems: "center", justifyContent: "center",
    background: tokens.colorNeutralBackground2,
  },
  card: {
    display: "flex", flexDirection: "column", gap: tokens.spacingVerticalM,
    padding: `${tokens.spacingVerticalXXXL} ${tokens.spacingHorizontalXXXL}`,
    border: `1px solid ${tokens.colorNeutralStroke1}`, borderRadius: tokens.borderRadiusLarge,
    background: tokens.colorNeutralBackground1, minWidth: "340px", maxWidth: "90vw",
    boxShadow: tokens.shadow16,
  },
  title: { fontSize: tokens.fontSizeBase500, fontWeight: tokens.fontWeightSemibold, color: tokens.colorNeutralForeground1 },
  desc: { color: tokens.colorNeutralForeground3, fontSize: tokens.fontSizeBase300, lineHeight: "1.5" },
  row: { display: "flex", gap: tokens.spacingHorizontalS },
  input: { flex: 1, fontFamily: '"Fira Code", monospace' },
});

export default function TokenGate() {
  const styles = useStyles();
  const [value, setValue] = useState("");
  const submit = () => {
    setToken(value.trim());
    // 整页刷新: 重建全部连接(fetch/WS 均在模块加载后持 token), 避免conn 状态残留
    window.location.reload();
  };
  return (
    <div className={styles.root}>
      <form className={styles.card} onSubmit={(e) => { e.preventDefault(); submit(); }}>
        <span className={styles.title}>webshark 访问令牌</span>
        <span className={styles.desc}>
          服务端已启用访问控制, 请输入访问令牌后连接。留空并以无令牌模式连接, 仅在服务端未启用访问控制时可用。
        </span>
        <div className={styles.row}>
          <Input
            className={styles.input}
            type="password"
            placeholder="访问令牌"
            aria-label="访问令牌"
            value={value}
            onChange={(_, d) => setValue(d.value)}
          />
          <Button appearance="primary" type="submit">连接</Button>
        </div>
      </form>
    </div>
  );
}
