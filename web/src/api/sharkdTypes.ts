// sharkdTypes.ts - sharkd JSON-RPC 响应类型(协议形状以 sharkd_session.c 源码级行为为准).
// 注意: sharkd 输出所有 bool 为字符串 "true"(frames 的 i/m/ct 等), 前端按字符串判定.

/** frames 每帧行: c = 列字符串数组(顺序 = sharkd 当前文件列集), num = 帧号 */
export interface SharkdFrameRow {
  c: string[];
  num: number;
  /** "true" = ignored / marked / commented */
  i?: string;
  m?: string;
  ct?: string;
  comments?: string[];
  /** 色彩滤镜: 6 位 hex */
  bg?: string;
  fg?: string;
}

/** 协议树节点(递归, 子节点在 n 数组) */
export interface SharkdTreeNode {
  /** 显示标签(proto_item_fill_label 产物, 必有) */
  l: string;
  /** [item_start, item_length] 主数据字节区间(length > 0 才有) */
  h?: [number, number];
  /** [appendix_start, appendix_length] 附加区间 */
  i?: [number, number];
  /** 数据源索引(非主 tvb 时) */
  ds?: number;
  /** "proto" / "framenum" / "url" */
  t?: string;
  /** 仅 t=framenum */
  fnum?: number;
  /** 仅 t=url */
  url?: string;
  /** display filter 匹配串(点击"作为过滤器"用) */
  f?: string;
  /** 字段缩写名(abbrev, 如 mb.transaction) */
  fn?: string;
  /** expert severity 字符串 */
  s?: string;
  /** 子树 ett 索引 */
  e?: number;
  /** "true" = generated / hidden */
  g?: string;
  v?: string;
  /** 子节点数组 */
  n?: SharkdTreeNode[];
}

/** frame 详情: {tree, bytes} tree 协议树 + bytes 主数据 base64 */
export interface SharkdFrameDetail {
  tree?: SharkdTreeNode[];
  /** 主数据 base64 */
  bytes?: string;
}

/** complete 响应 field/pref 数组项(pref {f:"icmp", d:"ICMP"}; field 分支可能带 n/t) */
export interface SharkdCompletion {
  /** 补全文本 */
  f: string;
  /** 描述(字段/协议显示名, 唯一名才有; field 分支常缺省) */
  d?: string;
  /** field 分支描述备选(d 缺省时回退取 n) */
  n?: string;
  /** field 分支附带类型标记(消费时忽略) */
  t?: string;
}

/** follow 单段载荷: n = 帧号, d = 该段字节 base64 */
export interface SharkdFollowPayload {
  n: number;
  d: string;
}

/** follow 响应: 单流双向重组; 空流(TLS 无数据)返回 shost "NONE"/sport "0"/
 *  sbytes 0 且 payloads 缺省——客户端归一补齐 payloads 空数组(其余字段同样容忍缺省) */
export interface SharkdFollowResult {
  shost: string;
  sport: string;
  sbytes: number;
  chost: string;
  cport: string;
  cbytes: number;
  payloads: SharkdFollowPayload[];
}

/** tap expert 明细项: f 帧号, s 级别文本(Chat/Warning/Error...), g 分组
 *  (如 "Sequence"), m 消息, p 协议 */
export interface SharkdExpertDetail {
  f: number;
  s: string;
  g: string;
  m: string;
  p: string;
}

/** tap 单项(tap 响应数组元素; details 可能空) */
export interface SharkdExpertTap {
  tap: string;
  type: string;
  details: SharkdExpertDetail[];
}

/** tap expert 响应(shard = tap 实际执行时实时实例载入的分片名, 未载入为空串/缺省) */
export interface SharkdExpertResponse {
  shard?: string;
  taps?: SharkdExpertTap[];
}

/** check 响应(warning 形态) */
export interface SharkdCheckWarning {
  status: string;
  warning?: string;
}
