// CaptureHexView.tsx - 原始字节三态视图(Tab: 十六进制/二进制/文本):
// bytes 由 base64 解码(captureHex.decodeBase64); 树选区(captureHex.nodeSpans 区间)高亮,
// 三态共用同一 span 投影(nodeSpans 逻辑不变). 二进制=每字节 8 bit 网格(每行 8 字节).
import { useMemo, } from "react";
import { makeStyles, mergeClasses, tokens } from "@fluentui/react-components";
import { decodeBase64, toHexRows, toBinRows, byteHex, byteAscii, byteBits, offsetHex, nodeSpans, type SelectionSpan } from "../../utils/captureHex";
import type { SharkdTreeNode } from "../../api/sharkdTypes";
import { useMonoStyles } from "../../styles/mono";

export type HexMode = "hex" | "bin" | "ascii";

const useStyles = makeStyles({
  root: { display: "flex", flexDirection: "column", minHeight: "0", overflowY: "auto", overflowX: "auto", flex: 1 },
  grid: { padding: `${tokens.spacingVerticalXXS} ${tokens.spacingHorizontalS}` },
  row: { display: "flex", gap: tokens.spacingHorizontalS, whiteSpace: "nowrap" as const },
  offset: { color: tokens.colorNeutralForeground3 },
  hex: { display: "inline-flex" },
  cell: { width: "1.7em", textAlign: "center" as const },
  cellBin: { width: "6.2em", textAlign: "center" as const },
  cellSel: { backgroundColor: tokens.colorBrandBackground2, borderRadius: "2px" },
  asciiSel: { backgroundColor: tokens.colorBrandBackground2, fontWeight: tokens.fontWeightBold },
});

/** 单字节渲染单元(避免整段字符串 diff, 选中态单元格级高亮; 三态共用 span 投影) */
function ByteCells({ row, spans, mode, styles, monoS }: {
  row: ReturnType<typeof toHexRows>[number];
  spans: SelectionSpan[];
  mode: HexMode;
  styles: ReturnType<typeof useStyles>;
  monoS: ReturnType<typeof useMonoStyles>;
}) {
  const cells = [];
  for (let i = 0; i < row.bytes.length; i++) {
    const pos = row.offset + i;
    const sel = spans.some((sp) => pos >= sp.start && pos < sp.end);
    if (mode === "hex") {
      cells.push(
        <span key={pos} className={mergeClasses(styles.cell, monoS.mono, sel && styles.cellSel)}>{byteHex(row.bytes[i])}</span>,
      );
    } else if (mode === "bin") {
      cells.push(
        <span key={pos} className={mergeClasses(styles.cellBin, monoS.mono, sel && styles.cellSel)}>{byteBits(row.bytes[i])}</span>,
      );
    } else {
      cells.push(
        <span key={pos} className={mergeClasses(monoS.mono, sel && styles.asciiSel)}>{byteAscii(row.bytes[i])}</span>,
      );
    }
  }
  return <>{cells}</>;
}

export function CaptureHexView({ bytesB64, selNode, mode }: {
  /** 主数据 base64(frame.bytes; sharkd 4.6.10 frame 响应无 ds 多数据源) */
  bytesB64?: string;
  /** 选中树节点(h/i 字节区间) */
  selNode?: SharkdTreeNode;
  /** 显示模式(受控, Tab 在面板头, 由页面持有) */
  mode: HexMode;
}) {
  const styles = useStyles();
  const monoS = useMonoStyles();

  const bytes = useMemo(() => decodeBase64(bytesB64), [bytesB64]);
  const spans = useMemo(() => (selNode ? nodeSpans(selNode.h, selNode.i) : []), [selNode]);
  const rows = useMemo(() => (mode === "bin" ? toBinRows(bytes) : toHexRows(bytes)), [bytes, mode]);

  return (
    <div className={styles.root}>
      <div className={styles.grid}>
        {rows.map((row) => (
          <div key={row.offset} className={styles.row}>
            <span className={mergeClasses(styles.offset, monoS.mono)}>{offsetHex(row.offset)}</span>
            <span className={mergeClasses(styles.hex, monoS.mono)}>
              <ByteCells row={row} spans={spans} mode={mode} styles={styles} monoS={monoS} />
            </span>
          </div>
        ))}
        {bytes.length === 0 && (
          <div className={monoS.mono} style={{ color: tokens.colorNeutralForeground3 }}>(空)</div>
        )}
      </div>
    </div>
  );
}
