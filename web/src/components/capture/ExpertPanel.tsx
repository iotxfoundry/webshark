// ExpertPanel.tsx - Expert Information 汇总(sharkd tap expert): 抽屉展示专家诊断消息.
// 头部按 severity 文本分组计数徽章(Error 红/Warning 黄/Chat 灰, 其余中性); 明细行
// [级别, 分组, 协议, 消息, 帧号], 行点击 → onFrameSelect(n) 由宿主选中该帧并拉详情
// (最小实现: 不强求包列表滚动定位——目标帧可能不在已拉取游标内, 见 CapturePage.onExpertFrame).
import { useEffect, useMemo, useState } from "react";
import {
  Badge, Button, Drawer, DrawerBody, DrawerHeader, DrawerHeaderTitle,
  makeStyles, Spinner, tokens,
} from "@fluentui/react-components";
import { Dismiss24Regular } from "@fluentui/react-icons";
import { Virtuoso } from "react-virtuoso";
import type { SharkdExpertDetail, SharkdExpertResponse } from "../../api/sharkdTypes";
import { isTestEnv } from "../../utils/captureTree";
import { useMonoStyles } from "../../styles/mono";

const useStyles = makeStyles({
  body: { display: "flex", flexDirection: "column", minHeight: 0, padding: 0 },
  headRow: {
    display: "flex", alignItems: "center", gap: tokens.spacingHorizontalS,
    padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}`,
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`, flexWrap: "wrap" as const, flexShrink: 0,
  },
  // 级别徽章主题 token 成对(背景/前景, 明暗主题对比达标): Error 红/Warning 黄/Chat 灰
  sevError: { backgroundColor: tokens.colorPaletteRedBackground2, color: tokens.colorPaletteRedForeground2 },
  sevWarn: { backgroundColor: tokens.colorPaletteMarigoldBackground2, color: tokens.colorPaletteMarigoldForeground2 },
  sevChat: { backgroundColor: tokens.colorPalettePlatinumBackground2, color: tokens.colorPalettePlatinumForeground2 },
  list: { flex: 1, overflowY: "auto", minHeight: 0 },
  row: {
    display: "grid",
    gridTemplateColumns: "80px 110px 80px 1fr 60px",
    gap: tokens.spacingHorizontalS,
    alignItems: "baseline",
    padding: `${tokens.spacingVerticalXS} ${tokens.spacingHorizontalM}`,
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`,
    fontSize: tokens.fontSizeBase200,
    cursor: "pointer" as const,
    ":hover": { backgroundColor: tokens.colorNeutralBackground1Hover },
  },
  rowHead: { cursor: "default" as const, fontWeight: tokens.fontWeightSemibold, color: tokens.colorNeutralForeground3 },
  hint: {
    flex: 1, display: "flex", alignItems: "center", justifyContent: "center",
    padding: tokens.spacingVerticalXL, color: tokens.colorNeutralForeground3,
  },
});

/** severity → 徽章样式类(Error 红/Warning 黄/Chat 灰/其余中性不染色) */
function sevClass(s: string): string | undefined {
  const k = s.toLowerCase();
  if (k.includes("error")) return "sevError";
  if (k.includes("warn")) return "sevWarn";
  if (k.includes("chat")) return "sevChat";
  return undefined;
}

export function ExpertPanel({ fetchExpert, onFrameSelect, onClose }: {
  /** 取数透传(宿主会话 hook 提供) */
  fetchExpert: () => Promise<SharkdExpertResponse>;
  /** 明细行点击: 选中该帧(宿主拉详情) */
  onFrameSelect: (n: number) => void;
  onClose: () => void;
}) {
  const styles = useStyles();
  const monoS = useMonoStyles();
  const [data, setData] = useState<SharkdExpertResponse | null>(null);
  const [err, setErr] = useState("");

  // 打开即取数; alive 防卸载后 setState(重置放异步头, 避免同步 setState-in-effect 级联渲染)
  useEffect(() => {
    let alive = true;
    const load = async () => {
      setData(null);
      setErr("");
      try {
        const d = await fetchExpert();
        if (alive) setData(d);
      } catch (e) {
        if (alive) setErr(e instanceof Error ? e.message : String(e));
      }
    };
    void load();
    return () => { alive = false; };
  }, [fetchExpert]);

  // 全部明细(多 tap 展平, 现仅 expert 一项) + 按级别文本分组计数(Error/Warning/Chat/其余)
  const details = useMemo(() => (data?.taps ?? []).flatMap((t) => t.details), [data]);
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const d of details) m.set(d.s, (m.get(d.s) ?? 0) + 1);
    return [...m.entries()];
  }, [details]);

  const renderRow = (d: SharkdExpertDetail, i: number) => (
    <div key={`${d.f}-${i}`} className={styles.row} role="button" tabIndex={0}
      onClick={() => onFrameSelect(d.f)}
      onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") onFrameSelect(d.f); }}>
      <span><Badge size="small" appearance="filled" className={sevClass(d.s)}>{d.s}</Badge></span>
      <span>{d.g}</span>
      <span>{d.p}</span>
      <span style={{ overflowWrap: "anywhere" }}>{d.m}</span>
      <span className={monoS.mono}>{d.f}</span>
    </div>
  );

  return (
    <Drawer open onOpenChange={(_, s) => { if (!s.open) onClose(); }} position="end" size="large">
      <DrawerHeader>
        <DrawerHeaderTitle
          action={
            <Button appearance="subtle" size="small" icon={<Dismiss24Regular />}
              aria-label="关闭专家信息" title="关闭" onClick={onClose} />
          }
        >
          专家信息
        </DrawerHeaderTitle>
      </DrawerHeader>
      <DrawerBody className={styles.body}>
        {err !== "" ? (
          <div className={styles.hint}>加载失败: {err}</div>
        ) : data === null ? (
          <div className={styles.hint}><Spinner label="正在统计专家信息…" /></div>
        ) : (
          <>
            {/* 头部级别计数徽章(其余级别中性默认样式) */}
            <div className={styles.headRow}>
              {counts.map(([s, c]) => (
                <Badge key={s} appearance="filled" className={sevClass(s)}>{s} {c}</Badge>
              ))}
            </div>
            <div className={styles.list}>
              {details.length === 0 ? (
                <div className={styles.hint}>无专家信息</div>
              ) : (
                <>
                  <div className={`${styles.row} ${styles.rowHead}`} aria-hidden>
                    <span>级别</span><span>分组</span><span>协议</span><span>消息</span><span>帧号</span>
                  </div>
                  {/* 虚拟滚动: 7 万行全量进 DOM 卡死实测 6.5s/50 万节点; jsdom 回退普通列表 */}
                  {isTestEnv() ? (
                    <div style={{ maxHeight: "100%", overflowY: "auto" }}>
                      {details.map((d: SharkdExpertDetail, i) => renderRow(d, i))}
                    </div>
                  ) : (
                    <Virtuoso
                      style={{ height: "100%" }}
                      totalCount={details.length}
                      itemContent={(i) => renderRow(details[i], i)}
                    />
                  )}
                </>
              )}
            </div>
          </>
        )}
      </DrawerBody>
    </Drawer>
  );
}
