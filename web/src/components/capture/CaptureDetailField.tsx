import { useState } from "react";
import { Button, makeStyles, mergeClasses, tokens } from "@fluentui/react-components";
import { CheckmarkRegular, CopyRegular } from "@fluentui/react-icons";
import { monoStyle } from "../../styles/mono";

/** 长值阈值: 超出默认收起为前缀预览 */
export const LONG_VALUE_LIMIT = 200;

const useStyles = makeStyles({
  row: {
    display: "flex", alignItems: "flex-start", gap: tokens.spacingHorizontalS,
    padding: `2px ${tokens.spacingHorizontalS}`, borderBottom: `1px solid ${tokens.colorNeutralStroke2}`,
    fontSize: tokens.fontSizeBase200,
    // 悬停/键盘聚焦时浮现行尾操作钮(复制/收起)
    "& .captureFieldActions": { opacity: 0, transition: "opacity .15s" },
    "&:hover .captureFieldActions, &:focus-within .captureFieldActions": { opacity: 1 },
  },
  name: { ...monoStyle, flexShrink: 0, width: "40%", color: tokens.colorNeutralForeground2, overflowWrap: "anywhere" as const },
  val: { ...monoStyle, flex: 1, overflowWrap: "anywhere" as const, wordBreak: "break-all" as const },
  // 长值: 保留换行(http.body 等多行文本); 展开态限高内滚
  wrap: { whiteSpace: "pre-wrap" as const },
  open: { maxHeight: "40vh", overflowY: "auto" },
  // 收起态预览整块可点击展开(键盘可达)
  preview: {
    cursor: "pointer" as const,
    "&:hover": { color: tokens.colorBrandForeground1 },
    "&:focus-visible": { outline: `2px solid ${tokens.colorBrandStroke1}`, outlineOffset: "-2px" },
  },
  suffix: { color: tokens.colorNeutralForeground3 },
  actions: { display: "inline-flex", alignItems: "flex-start", gap: "2px", flexShrink: 0 },
});

/** 解码字段行: name 值名; truncated=true 值尾追加(上游已截断)灰色后缀 */
export default function CaptureDetailField({ name, value, truncated }: {
  name: string; value: string; truncated?: boolean;
}) {
  const s = useStyles();
  const long = value.length > LONG_VALUE_LIMIT;
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const copy = () => {
    const p = navigator.clipboard?.writeText(value);
    if (!p) return;
    p.then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    }).catch(() => { /* 剪贴板写入失败静默, 保留图标 */ });
  };
  const collapsed = long && !open;
  return (
    <div className={s.row}>
      <span className={s.name}>{name}</span>
      <span
        className={mergeClasses(s.val, long && s.wrap, long && open && s.open, collapsed && s.preview)}
        onClick={collapsed ? () => setOpen(true) : undefined}
        role={collapsed ? "button" : undefined}
        tabIndex={collapsed ? 0 : undefined}
        onKeyDown={collapsed ? (e) => { if (e.key === "Enter" || e.key === " ") setOpen(true); } : undefined}
      >
        {collapsed ? `${value.slice(0, LONG_VALUE_LIMIT)}…（${value.length} 字符，点击展开）` : value}
        {truncated && <span className={s.suffix}>（上游已截断）</span>}
      </span>
      <span className="captureFieldActions" style={{ display: "inline-flex", alignItems: "flex-start", gap: "2px", flexShrink: 0 }}>
        {long && (
          <Button size="small" appearance="subtle" aria-label={open ? "收起" : "展开"} title={open ? "收起" : "展开"}
            onClick={() => setOpen(!open)}>{open ? "收起" : "展开"}</Button>
        )}
        <Button size="small" appearance="subtle" aria-label={copied ? "已复制" : "复制"}
          title={copied ? "已复制" : "复制完整值"} icon={copied ? <CheckmarkRegular /> : <CopyRegular />}
          onClick={copy} />
      </span>
    </div>
  );
}
