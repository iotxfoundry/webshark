package sharkd

import "encoding/json"

// 本文件类型与 JSON tag 均按本机 sharkd 4.6.9 实测响应编写（字段实测基线），
// 未实测的方法（iograph、download）不提供 typed 封装，请用 Call 自行构造。

// LoadResult load/setconf/bye 等操作型方法的响应：status 为 "OK" 即成功
// （失败走 envelope error，不经 status 字段）。
type LoadResult struct {
	Status string `json:"status"`
}

// CheckResult check 的响应：filter 校验通过 status 为 "OK"；有警告时另带
// warning 文本（filter 仍有效）。
type CheckResult struct {
	Status  string `json:"status"`
	Warning string `json:"warning,omitempty"`
}

// Info info 的响应：sharkd 能力清单（版本、列定义、可用 tap 名单等）。
type Info struct {
	// Version sharkd 版本号（如 "4.6.9"）。
	Version string `json:"version"`
	// Columns 默认列定义（帧列表 c 数组与之一一对应）。
	Columns []InfoColumn `json:"columns"`
	// Stats/Nstat/Convs/SeqA/Taps/EO/SRT/RTD/Follow 可用统计/会话/tap 名单，
	// tap 字段即对应 tap 方法的 tap 参数值。
	Stats  []InfoTap `json:"stats"`
	Nstat  []InfoTap `json:"nstat"`
	Convs  []InfoTap `json:"convs"`
	SeqA   []InfoTap `json:"seqa"`
	Taps   []InfoTap `json:"taps"`
	EO     []InfoTap `json:"eo"`
	SRT    []InfoTap `json:"srt"`
	RTD    []InfoTap `json:"rtd"`
	Follow []InfoTap `json:"follow"`
	// CaptureTypes/EncapTypes 支持的抓包/封装类型。
	CaptureTypes []NameDesc `json:"capture_types"`
	EncapTypes   []NameDesc `json:"encap_types"`
	// Ftypes 字段类型名列表。
	Ftypes []string `json:"ftypes"`
}

// InfoColumn 默认列定义（name 列标题，format 列格式串）。
type InfoColumn struct {
	Name   string `json:"name"`
	Format string `json:"format"`
}

// InfoTap 可用 tap 条目（name 显示名，tap 协议键）。
type InfoTap struct {
	Name string `json:"name"`
	Tap  string `json:"tap"`
}

// NameDesc 名称-描述条目。
type NameDesc struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// FramesParams frames 的参数（全部可选，零值即不传）。
type FramesParams struct {
	// Skip 跳过的帧数（每次调用从头重扫，成本 O(游标)）。
	Skip int `json:"skip,omitempty"`
	// Limit 最多返回的行数。
	Limit int `json:"limit,omitempty"`
	// Filter display filter（sharkd 语法）。
	Filter string `json:"filter,omitempty"`
}

// FrameRow frames 的单行（列值与 info.columns 对应）。未知字段解码时丢弃；
// 字段随 sharkd 版本可能增减，客户端路径不做零解析损耗的承诺——需全部
// 原始字段时直接用 Frames 返回的 RawMessage。
type FrameRow struct {
	// C 列值数组（No./Time/Source/.../Info，与 info.columns 一一对应）。
	C []string `json:"c"`
	// Num 帧号。
	Num int64 `json:"num"`
	// Bg/Fg 背景前景色（如着色规则命中；六位十六进制，无色为空）。
	Bg string `json:"bg,omitempty"`
	Fg string `json:"fg,omitempty"`
	// I/M/CT 可选字符串标记（含义随 sharkd 版本）。
	I  string `json:"i,omitempty"`
	M  string `json:"m,omitempty"`
	CT string `json:"ct,omitempty"`
	// Comments 帧注释。
	Comments []string `json:"comments,omitempty"`
}

// FrameParams frame 的参数。
type FrameParams struct {
	// Frame 帧号。
	Frame int64 `json:"frame"`
	// Proto 是否返回协议树。
	Proto bool `json:"proto"`
	// Bytes 是否返回原始字节（base64）。
	Bytes bool `json:"bytes"`
}

// Frame 单帧详情：协议树、原始字节与着色；fol/followers 为流跟随入口，
// 形状未实测，原样保留。
type Frame struct {
	// Tree 协议树根节点数组。
	Tree []TreeNode `json:"tree"`
	// Bytes 帧原始字节（base64 解码后的原文）。
	Bytes []byte `json:"bytes"`
	// Fol/Followers 可跟随的流描述（形状未实测，原样透传）。
	Fol       json.RawMessage `json:"fol"`
	Followers json.RawMessage `json:"followers"`
	// Bg/Fg 着色规则命中的背景前景色。
	Bg string `json:"bg"`
	Fg string `json:"fg"`
}

// TreeNode 协议树节点。层级树：l 必有，n 为子节点；h/i 为该节点覆盖的
// 字节区间 [起始, 长度]（h 展示区间，i 选中区间）。
type TreeNode struct {
	// L 节点标签（协议树面板显示文本）。
	L string `json:"l"`
	// H/I 字节区间 [start,len]。
	H []int `json:"h,omitempty"`
	I []int `json:"i,omitempty"`
	// E 字段取值（数值类字段）。
	E int64 `json:"e,omitempty"`
	// T 字段类型（如 "str"、"uint"）。
	T string `json:"t,omitempty"`
	// F 字段所属协议的 display filter 串。
	F string `json:"f,omitempty"`
	// FN 字段缩写（如 "icmp.type"）。
	FN string `json:"fn,omitempty"`
	// N 子节点。
	N []TreeNode `json:"n,omitempty"`
	// DS 专家信息严重度级别（数值）。
	DS int `json:"ds,omitempty"`
	// FNum 关联帧号。
	FNum int64 `json:"fnum,omitempty"`
	// URL 关联链接。
	URL string `json:"url,omitempty"`
	// S 专家信息 severity 文本。
	S string `json:"s,omitempty"`
	// G 专家信息标记。
	G bool `json:"g,omitempty"`
}

// CompleteParams complete 的参数：field 补全协议/字段，pref 补全偏好项
// （二者取其一）。
type CompleteParams struct {
	Field string `json:"field,omitempty"`
	Pref  string `json:"pref,omitempty"`
}

// CompletionSet complete 的响应：按请求键返回对应集合（field 与 pref 至多
// 一个非空）。
type CompletionSet struct {
	// Field display filter 补全项。
	Field []CompletionItem `json:"field,omitempty"`
	// Pref 偏好项补全。
	Pref []PrefItem `json:"pref,omitempty"`
}

// CompletionItem display filter 补全项（f 补全串，t 类型码，n 显示名）。
type CompletionItem struct {
	F string `json:"f"`
	T int    `json:"t"`
	N string `json:"n"`
}

// PrefItem 偏好项（f 偏好键如 "gui.console_open"，d 描述）。
type PrefItem struct {
	F string `json:"f"`
	D string `json:"d"`
}

// FollowParams follow 的参数。
type FollowParams struct {
	// Follow 流类型（如 "TCP"、"UDP"）。
	Follow string `json:"follow"`
	// Filter 定位流的 display filter（如 "tcp.stream eq 0"）。
	Filter string `json:"filter,omitempty"`
}

// FollowResult 流跟随结果：两端地址/端口/字节数与可选分片。注意 sport/cport
// 实测为字符串（sharkd 侧即为字符串类型）。
type FollowResult struct {
	SHost  string `json:"shost"`
	SPort  string `json:"sport"`
	SBytes int64  `json:"sbytes"`
	CHost  string `json:"chost"`
	CPort  string `json:"cport"`
	CBytes int64  `json:"cbytes"`
	// Payloads 流分片（n 帧号，d base64 解码后的数据）；无数据时缺省。
	Payloads []FollowPayload `json:"payloads,omitempty"`
}

// FollowPayload 流跟随的单个数据分片。
type FollowPayload struct {
	// N 该分片所在帧号。
	N int64 `json:"n"`
	// D 该帧方向上的载荷数据。
	D []byte `json:"d"`
}

// TapParams tap 的参数：协议键固定为 "tap0"（sharkd 允许多 tap，本 SDK 单路）。
type TapParams struct {
	// Tap tap 名（取自 info.taps 等，如 "expert"）。
	Tap string `json:"tap0"`
}

// TapResult tap 的响应。
type TapResult struct {
	Taps []TapOutput `json:"taps"`
}

// TapOutput 单个 tap 的输出。
type TapOutput struct {
	// Tap 请求的 tap 名；Type 输出类型。
	Tap  string `json:"tap"`
	Type string `json:"type"`
	// Details 明细行（形状随 tap 类型；expert 类型见 TapDetail）。
	Details []TapDetail `json:"details,omitempty"`
}

// TapDetail expert tap 的明细行。
type TapDetail struct {
	// F 帧号。
	F int64 `json:"f"`
	// S 严重度（如 "Warning"）。
	S string `json:"s"`
	// G 分组（如 "Checksum"）。
	G string `json:"g"`
	// P 协议（如 "ICMP"）。
	P string `json:"p"`
	// M 明细消息。
	M string `json:"m"`
}

// IntervalsResult intervals 的响应：按时间桶聚合的流量分布。
type IntervalsResult struct {
	// Intervals 桶三元组数组 [起始偏移, 帧数, 字节数]（起始相对首帧）。
	Intervals [][3]int64 `json:"intervals"`
	// Last 最后桶的起始偏移。
	Last int64 `json:"last"`
	// Frames/Bytes 覆盖范围内的总帧数/总字节数。
	Frames int64 `json:"frames"`
	Bytes  int64 `json:"bytes"`
}
