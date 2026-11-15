// captureHex.ts - 原始字节 hex 视图纯逻辑: base64 解码 + 16 列网格投影 + 树选区切片.
// 框架无关(纯函数), 便于单测; 渲染由 CaptureHexView 组件消费.

/** 网格行: 16 字节一行, offset 行首偏移 */
export interface HexRow {
  offset: number;
  /** 本行字节(≤16) */
  bytes: Uint8Array;
}

/** base64 → 字节(容错: 非法 base64 返回空) */
export function decodeBase64(b64: string | undefined): Uint8Array {
  if (!b64) return new Uint8Array(0);
  try {
    const bin = atob(b64);
    const out = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
    return out;
  } catch {
    return new Uint8Array(0);
  }
}

/** 字节 → 16 列行数组 */
export function toHexRows(bytes: Uint8Array): HexRow[] {
  const rows: HexRow[] = [];
  for (let off = 0; off < bytes.length; off += 16) {
    rows.push({ offset: off, bytes: bytes.subarray(off, Math.min(off + 16, bytes.length)) });
  }
  return rows;
}

/** 字节 → 两位 hex(小写) */
export function byteHex(b: number): string {
  return b.toString(16).padStart(2, "0");
}

/** 字节 → ASCII 可视字符(不可见落 '.') */
export function byteAscii(b: number): string {
  return b >= 0x20 && b < 0x7f ? String.fromCharCode(b) : ".";
}

/** 字节 → 8 位二进制串 */
export function byteBits(b: number): string {
  return b.toString(2).padStart(8, "0");
}

/** 字节 → 二进制视图行数组(每行 8 字节, 每字节 8 bit) */
export function toBinRows(bytes: Uint8Array): HexRow[] {
  const rows: HexRow[] = [];
  for (let off = 0; off < bytes.length; off += 8) {
    rows.push({ offset: off, bytes: bytes.subarray(off, Math.min(off + 8, bytes.length)) });
  }
  return rows;
}

/** 偏移 → 5 位 hex 串(offset 列, hex/bin 两视图共用) */
export function offsetHex(off: number): string {
  return off.toString(16).padStart(5, "0");
}

/** 树节点选区(数据源内偏移): 每行 [start,end) 与行格 index 区间投影 */
export interface SelectionSpan {
  start: number;
  end: number; // exclusive
}

/** 单个 h/i 区间(相对所属数据源)的完整 span */
export function nodeSpans(h: [number, number] | undefined, i: [number, number] | undefined): SelectionSpan[] {
  const out: SelectionSpan[] = [];
  if (h && h[1] > 0) out.push({ start: h[0], end: h[0] + h[1] });
  if (i && i[1] > 0) out.push({ start: i[0], end: i[0] + i[1] });
  return out;
}
