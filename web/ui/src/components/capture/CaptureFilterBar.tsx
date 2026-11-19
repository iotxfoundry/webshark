// CaptureFilterBar.tsx - display filter 输入条: 防抖 check 校验横幅(错误阻塞 + warning 非阻塞) +
// complete 补全下拉 + 错误透传(sharkd -5001 消息原样展示). filter 串 100% sharkd 语义, 前端不解析.
import { useEffect, useRef, useState } from "react";
import { Input, Badge, MessageBar, MessageBarBody, MessageBarTitle, makeStyles, mergeClasses, tokens } from "@fluentui/react-components";
import { Dismiss16Regular } from "@fluentui/react-icons";
import type { SharkdCompletion } from "../../api/sharkdTypes";
import { monoStyle } from "../../styles/mono";
import { errMsg as toErrMsg } from "../../api/client";

const useStyles = makeStyles({
  root: { display: "flex", flexDirection: "column", gap: tokens.spacingVerticalXS },
  bar: { display: "flex", alignItems: "center", gap: tokens.spacingHorizontalS },
  input: { flex: 1, fontFamily: monoStyle.fontFamily, fontSize: tokens.fontSizeBase200 },
  valid: { outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorPaletteGreenForeground1 },
  invalid: { outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorPaletteRedForeground1 },
  applyBtn: { flexShrink: 0 },
  // 筛选激活态: 输入框高亮描边(复用 valid/invalid 的 outline 样式模式)
  active: { outlineWidth: "1px", outlineStyle: "solid", outlineColor: tokens.colorBrandForeground1 },
  completions: {
    position: "absolute" as const, zIndex: 10, maxHeight: "240px", overflowY: "auto",
    background: tokens.colorNeutralBackground1, border: `1px solid ${tokens.colorNeutralStroke1}`,
    borderRadius: tokens.borderRadiusMedium, boxShadow: tokens.shadow8, minWidth: "280px",
  },
  compItem: { padding: `2px ${tokens.spacingHorizontalS}`, fontSize: tokens.fontSizeBase200, cursor: "pointer" as const, display: "flex", gap: tokens.spacingHorizontalS, alignItems: "baseline", background: "transparent" as const, border: "none" as const, textAlign: "left" as const, width: "100%" as const },
  compHover: { ":hover": { backgroundColor: tokens.colorNeutralBackground1Hover } },
  compName: { fontFamily: monoStyle.fontFamily, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" as const },
  compDesc: { color: tokens.colorNeutralForeground3, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" as const },
});

/** 输入串在光标处提取补全前缀(最后一个非空白 token; 运算符 token 如 "==" 不算前缀) */
export function completionPrefix(input: string): string {
  const m = input.match(/([^\s()]+\s*)$/);
  const tok = m ? m[1] : "";
  return /^(==|!=|>=|<=|>|<|&&|\|\||and|or|not|in|contains|matches)$/.test(tok.trim()) ? "" : tok;
}

/** 把选中补全应用到输入(替换末尾 token; 补全项自带尾部空格规则: 协议名补后接点由用户续输) */
export function applyCompletion(input: string, text: string): string {
  return input.replace(/([^\s()]*)$/, text + " ");
}

export function CaptureFilterBar({ value, onChange, onApply, check, complete, activeFilter }: {
  value: string;
  onChange: (v: string) => void;
  onApply: (v: string) => void;
  check: (f: string) => Promise<{ status: string; warning?: string }>;
  complete: (p: string) => Promise<SharkdCompletion[]>;
  /** 已生效筛选(非空 = 筛选模式激活: 输入框高亮 + 「筛选中」角标; 空串 = 普通流) */
  activeFilter?: string;
}) {
  const active = (activeFilter ?? "") !== "";
  const styles = useStyles();
  const [completions, setCompletions] = useState<SharkdCompletion[]>([]);
  // 下拉框可见性 = 聚焦且有候选: 还原的初值(如 localStorage filter)在页面加载
  // 后会自动弹补全, 无焦点门控时用户从未聚焦也关不掉(Esc/Enter 够不着输入框)
  const [compOpen, setCompOpen] = useState(false);
  const inputRef = useRef<HTMLDivElement>(null);
  // check/complete 各自独立的序号(并发防抖丢弃判定分开, 互不抢号)
  const checkSeqRef = useRef(0);
  const completeSeqRef = useRef(0);

  // 防抖 check: 输入停 400ms 校验; 空 filter 恒 OK 不发请求
  const [checkState, setCheckState] = useState<{ state: "idle" | "valid" | "invalid"; msg: string }>({ state: "idle", msg: "" });
  useEffect(() => {
    const v = value.trim();
    if (v === "") {
      // 空输入复位: 放进宏任务避开同步 setState-in-effect(cascading renders)
      const id = setTimeout(() => { setCheckState({ state: "idle", msg: "" }); setCompletions([]); }, 0);
      return () => clearTimeout(id);
    }
    const seq = ++checkSeqRef.current;
    const t = setTimeout(async () => {
      try {
        const r = await check(v);
        if (seq !== checkSeqRef.current) return;
        // 校验失败业务化: 200 + {code,message}, code 非 0 即无效(按 code 判定, 不走 HTTP 错误)
        if (r.code) {
          setCheckState({ state: "invalid", msg: r.message ?? `filter 无效(code=${r.code})` });
          return;
        }
        setCheckState({ state: "valid", msg: r.warning ?? "" });
      } catch (e) {
        if (seq !== checkSeqRef.current) return;
        setCheckState({ state: "invalid", msg: toErrMsg(e) });
      }
    }, 400);
    return () => clearTimeout(t);
  }, [value, check]);
  // 校验横幅直读状态对象(同一 setState 批次内渲染, 避免双 state 竞态)
  const state = checkState.state;
  const errMsg = checkState.msg;

  // 防抖 complete: 输入停 300ms 补全(前缀含点或字母; 空 token 不补)。
  // 不以 check 结果为门槛: 补全的意义就是辅助输入到合法(如 `ip.` 校验必报
  // 无效, 若拦截则候选永远弹不出, 与 Wireshark 行为相悖)
  useEffect(() => {
    const prefix = completionPrefix(value);
    if (prefix.trim() === "") {
      const id = setTimeout(() => setCompletions([]), 0);
      return () => clearTimeout(id);
    }
    const seq = ++completeSeqRef.current;
    const t = setTimeout(async () => {
      try {
        const items = await complete(prefix.trim());
        if (seq !== completeSeqRef.current) return;
        setCompletions(items.slice(0, 50));
      } catch { setCompletions([]); }
    }, 300);
    return () => clearTimeout(t);
  }, [value, complete]);

  return (
    <div className={styles.root}>
      <div className={styles.bar} ref={inputRef} style={{ position: "relative" }}>
        <Input
          className={mergeClasses(styles.input, active && styles.active,
            state === "valid" && styles.valid, state === "invalid" && styles.invalid)}
          placeholder="显示过滤器, 如: tcp.port == 80 && ip.addr == 192.168.1.1"
          value={value}
          aria-label="显示过滤器"
          onChange={(_, d) => onChange(d.value)}
          onFocus={() => setCompOpen(true)}
          onBlur={() => setCompOpen(false)}
          onKeyDown={(e) => {
            if (e.key === "Enter") { setCompletions([]); onApply(value); }
            if (e.key === "Escape") setCompletions([]);
          }}
        />
        {/* 筛选中角标: 恒挂容器 visibility 切换(占位稳定防清除钮跳动); title 提示已生效表达式 */}
        <span style={{ visibility: active ? "visible" : "hidden" }}
          title={active ? `已生效: ${activeFilter}` : undefined}>
          <Badge appearance="tint" color="brand">筛选中</Badge>
        </span>
        {value !== "" && (
          <button
            aria-label="清除过滤器"
            style={{ background: "none", border: "none", cursor: "pointer", color: tokens.colorNeutralForeground3, display: "inline-flex", padding: 8 }}
            onClick={() => { onChange(""); onApply(""); }}
          >
            <Dismiss16Regular />
          </button>
        )}
        {compOpen && completions.length > 0 && (
          <div className={styles.completions} role="listbox" style={{ top: "100%", left: 0 }}>
            {completions.map((c, i) => (
              <button key={`${c.f}-${i}`} className={mergeClasses(styles.compItem, styles.compHover)} role="option" aria-selected={false}
                onMouseDown={(e) => { e.preventDefault(); onChange(applyCompletion(value, c.f)); setCompletions([]); }}>
                <span className={styles.compName}>{c.f}</span>
                {c.d && <span className={styles.compDesc}>{c.d}</span>}
              </button>
            ))}
          </div>
        )}
      </div>
      {state === "invalid" && (
        <MessageBar intent="error">
          <MessageBarBody><MessageBarTitle>过滤器无效</MessageBarTitle>{errMsg}</MessageBarBody>
        </MessageBar>
      )}
      {state === "valid" && errMsg !== "" && (
        // check 通过但 sharkd 带 warning: 非阻塞横幅(校验态仍 valid, 不拦应用)
        <MessageBar intent="warning">
          <MessageBarBody><MessageBarTitle>过滤器告警</MessageBarTitle>{errMsg}</MessageBarBody>
        </MessageBar>
      )}
    </div>
  );
}
