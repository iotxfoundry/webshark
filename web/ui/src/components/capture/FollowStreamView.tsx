// FollowStreamView.tsx - 流跟随对话视图(sharkd follow 单流重组): 抽屉展示一条 TCP/UDP/TLS/
// HTTP/HTTP2/QUIC 流的双向字节对话. 打开即取数; 头部两端端点+字节数; 消息流每段解 base64
// → utf-8(非法序列回退可打印 \xNN 转义), 行首标帧号+方向.
// 方向判定(近似): follow 响应的 payloads 是流内分段序列, 无方向字段——按回合交替近似:
// 偶数下标视为 C→S(客户端→服务端, 首段), 奇数下标视为 S→C; 未做协议语义重组(如 HTTP
// 请求/响应按报文边界配对), 纯双工对撞流(如逐字节 echo)下方向标注仅供参考.
import { Badge, Button, Drawer, DrawerBody, DrawerHeader, DrawerHeaderTitle,
  makeStyles, Spinner, Text, tokens,
} from "@fluentui/react-components";
import { Dismiss24Regular } from "@fluentui/react-icons";
import type { SharkdFollowResult } from "../../api/sharkdTypes";
import { monoStyle, useMonoStyles } from "../../styles/mono";
import { hintDrawer } from "../../styles/hint";
import { useDrawerFetch } from "../../hooks/useDrawerFetch";

/** payload 解码: base64 → utf-8; 非法序列(fatal)回退可打印转义(0x20-0x7e 直出, 其余 \xNN) */
export function decodeFollowPayload(b64: string): string {
  // 不复用 decodeBase64 的容错版: 非法 base64 需原样抛出(与历史行为一致, 由调用方 catch)
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  try {
    return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
  } catch {
    return Array.from(bytes, (b) =>
      b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : `\\x${b.toString(16).padStart(2, "0")}`,
    ).join("");
  }
}

const useStyles = makeStyles({
  body: {
    display: "flex",
    flexDirection: "column",
    minHeight: 0,
    padding: 0,
  },
  headRow: {
    display: "flex",
    alignItems: "center",
    gap: tokens.spacingHorizontalS,
    padding: `${tokens.spacingVerticalS} ${tokens.spacingHorizontalM}`,
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`,
    flexWrap: "wrap" as const,
    flexShrink: 0,
  },
  list: { flex: 1, overflowY: "auto", minHeight: 0 },
  line: {
    display: "flex",
    gap: tokens.spacingHorizontalS,
    alignItems: "flex-start",
    padding: `${tokens.spacingVerticalXS} ${tokens.spacingHorizontalM}`,
    borderBottom: `1px solid ${tokens.colorNeutralStroke2}`,
  },
  meta: {
    flexShrink: 0,
    display: "flex",
    flexDirection: "column",
    gap: "2px",
    alignItems: "flex-start",
    minWidth: "64px",
  },
  text: {
    margin: 0,
    whiteSpace: "pre-wrap",
    wordBreak: "break-all",
    flex: 1,
    fontFamily: monoStyle.fontFamily,
    fontSize: tokens.fontSizeBase200,
  },
  // 方向徽章主题 token 成对(背景/前景, 明暗主题均达标): C→S 蓝 / S→C 绿
  c2s: { backgroundColor: tokens.colorPaletteBlueBackground2, color: tokens.colorPaletteBlueForeground2 },
  s2c: { backgroundColor: tokens.colorPaletteGreenBackground2, color: tokens.colorPaletteGreenForeground2 },
  hint: hintDrawer,
});

export function FollowStreamView({ type, filter, fetchFollow, onClose }: {
  /** follow 类型(协议短名大写: TCP/UDP/TLS/HTTP/HTTP2/QUIC) */
  type: string;
  /** 流过滤串(树节点 f 串, 如 "tcp.stream == 0") */
  filter: string;
  /** 取数透传(宿主会话 hook 提供, 本组件不自建 sharkd 会话) */
  fetchFollow: (type: string, filter: string) => Promise<SharkdFollowResult>;
  onClose: () => void;
}) {
  const styles = useStyles();
  const monoS = useMonoStyles();
  const { data, err } = useDrawerFetch(() => fetchFollow(type, filter), [type, filter, fetchFollow]);

  return (
    <Drawer open onOpenChange={(_, s) => { if (!s.open) onClose(); }} position="end" size="large">
      <DrawerHeader>
        <DrawerHeaderTitle
          action={
            <Button appearance="subtle" size="small" icon={<Dismiss24Regular />}
              aria-label="关闭流跟随" title="关闭" onClick={onClose} />
          }
        >
          流跟随 — {type} <Text size={200} style={{ fontWeight: tokens.fontWeightRegular, marginLeft: 8 }}>{filter}</Text>
        </DrawerHeaderTitle>
      </DrawerHeader>
      <DrawerBody className={styles.body}>
        {err !== "" ? (
          <div className={styles.hint}>加载失败: {err}</div>
        ) : data === null ? (
          <div className={styles.hint}><Spinner label="正在重组流…" /></div>
        ) : (
          <>
            {/* 头部: 两端端点+字节数(空流 shost 为 NONE, 原样展示) */}
            <div className={styles.headRow}>
              <Badge appearance="filled" color="informative">{type}</Badge>
              <Text className={monoS.mono}>
                {data.shost}:{data.sport} ({data.sbytes} B) ⇄ {data.chost}:{data.cport} ({data.cbytes} B)
              </Text>
            </div>
            <div className={styles.list}>
              {data.payloads.length === 0 ? (
                <div className={styles.hint}>该流无数据载荷</div>
              ) : data.payloads.map((p, i) => {
                // 方向近似: 偶数下标 C→S(首段), 奇数下标 S→C(见文件头注释)
                const c2s = i % 2 === 0;
                return (
                  <div key={p.n} className={styles.line}>
                    <div className={styles.meta}>
                      <span className={monoS.mono}>帧 {p.n}</span>
                      <Badge size="small" appearance="filled" className={c2s ? styles.c2s : styles.s2c}>
                        {c2s ? "C→S" : "S→C"}
                      </Badge>
                    </div>
                    <pre className={styles.text}>{decodeFollowPayload(p.d)}</pre>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </DrawerBody>
    </Drawer>
  );
}
