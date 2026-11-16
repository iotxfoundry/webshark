package sharkd

import "encoding/json"

// 本文件的类型与 JSON tag 保持 sharkd 协议原文键名（如 max_packets、
// sub_stream、tap0、column0），以便直接与协议文档对照。
//
// 参数语义注意：sharkd 对可选数值参数要求正整数（发 0 会被整体拒绝 -32600），
// 对布尔参数只看键是否存在（发 false 同样视为开启），因此所有可选参数都
// omitempty，零值一律不发。

// OKResult load/setconf/setcomment/bye 等操作型方法的响应。load 失败有两种
// 形态：envelope error（不经 result）或本结构体 {status:<errstr>,err:<code>}；
// SDK 把 Err != 0 或 Status != "OK" 统一转成 error。
type OKResult struct {
	Status string `json:"status"`
	Err    int    `json:"err,omitempty"`
}

// LoadParams load 的参数（file 必填；限额 0 = 不限制且不发该键）。
type LoadParams struct {
	// File 要加载的 pcapng/pcap 文件路径（load 是内存快照，文件追加后须重 load）。
	File string `json:"file"`
	// MaxPackets 最多加载的帧数。
	MaxPackets uint32 `json:"max_packets,omitempty"`
	// MaxBytes 最多加载的字节数。
	MaxBytes uint64 `json:"max_bytes,omitempty"`
}

// StatusResult status 的响应：当前已加载文件的概览。
type StatusResult struct {
	// Frames 已加载帧数。
	Frames uint32 `json:"frames"`
	// Duration 首帧与末帧的时间差（秒）。
	Duration float64 `json:"duration"`
	// Filename 文件基名（无文件时缺省）。
	Filename string `json:"filename,omitempty"`
	// Filesize 文件字节数（无文件时缺省）。
	Filesize int64 `json:"filesize,omitempty"`
	// Columns 帧列表列标题（frames 行 c 数组与之一一对应）。
	Columns []string `json:"columns,omitempty"`
	// ColumnInfo 列定义明细。
	ColumnInfo []StatusColumn `json:"column_info,omitempty"`
}

// StatusColumn status.column_info 的列定义项（实测 visible 是 JSON 布尔）。
type StatusColumn struct {
	Title   string `json:"title"`
	Format  string `json:"format"`
	Visible bool   `json:"visible"`
	// Display 列显示格式："U"、"R" 或 "D"。
	Display string `json:"display"`
}

// AnalyseResult analyse 的响应：整份文件的协议清单（hash 表收集，按首见顺序）。
type AnalyseResult struct {
	// Frames 已加载帧数。
	Frames uint32 `json:"frames"`
	// Protocols 出现过的协议 filter 名（如 "eth"、"ip"、"tcp"）。
	Protocols []string `json:"protocols"`
	// First/Last 首帧/末帧时间（unix 秒，浮点；无帧时缺省）。
	First float64 `json:"first,omitempty"`
	Last  float64 `json:"last,omitempty"`
}

// FieldInfo field/fields 的字段描述。id/parent_id 是字符串（sharkd 用带引号
// 的 %i 输出），root 字段的 parent_id 为 "-1"。
type FieldInfo struct {
	ID          string `json:"id"`
	ParentID    string `json:"parent_id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Type        string `json:"type"`
}

// FieldParams field 的参数（name 必填，字段 filter 名如 "ip.src"）。
type FieldParams struct {
	Name string `json:"name"`
}

// FieldsResult fields 的响应。全部字段约 23 万条（10MB 级），单查用 Field。
type FieldsResult struct {
	Fields []FieldInfo `json:"fields"`
}

// FramesParams frames 的参数（全部可选，零值不发）。
type FramesParams struct {
	// Skip 跳过的帧数（每次调用从头重扫，成本 O(游标)）。
	Skip uint32 `json:"skip,omitempty"`
	// Limit 最多返回的行数。
	Limit uint32 `json:"limit,omitempty"`
	// Filter display filter（sharkd 语法）。
	Filter string `json:"filter,omitempty"`
	// Refs 逗号分隔、升序排列的时间参考帧号列表（如 "1,5,100"）。
	Refs string `json:"refs,omitempty"`
	// Columns 自定义列集，展开为 column0..columnN（给出即替换默认列集，数量任意）。
	// 元素取值二选一：列格式编号（源码枚举 0..3 可用；4 即 COL_CUSTOM，用编号
	// 会被拒 "Column definition invalid"）或自定义列 "<dfilter>:<occurrence>"
	// （如 "frame.number:1"）。
	Columns []string `json:"-"`
}

// FrameRow frames 的单行（列值与 status.columns 对应）。
type FrameRow struct {
	// C 列值数组。
	C []string `json:"c"`
	// Num 帧号。
	Num int64 `json:"num"`
	// CT 该帧带注释。
	CT bool `json:"ct,omitempty"`
	// Comments 帧注释。
	Comments []string `json:"comments,omitempty"`
	// I/M 该帧被忽略/被标记。
	I bool `json:"i,omitempty"`
	M bool `json:"m,omitempty"`
	// Bg/Fg 着色规则命中的背景前景色（六位十六进制，无色缺省）。
	Bg string `json:"bg,omitempty"`
	Fg string `json:"fg,omitempty"`
}

// FrameParams frame 的参数（frame 必填）。布尔参数为存在即开启（sharkd 只看
// 键是否存在，故一律 omitempty）。注意 hidden：4.6.x 源码读的是 "v"，"hidden"
// 能过参数校验但在该版本可能不生效（上游缺陷），此处按参数表命名。
type FrameParams struct {
	// Frame 帧号（必填，正整数）。
	Frame uint32 `json:"frame"`
	// Proto 返回协议树。
	Proto bool `json:"proto,omitempty"`
	// Columns 返回列值（col）。
	Columns bool `json:"columns,omitempty"`
	// Color 返回着色（bg/fg）。
	Color bool `json:"color,omitempty"`
	// Bytes 返回原始字节（base64）。
	Bytes bool `json:"bytes,omitempty"`
	// Hidden 协议树包含隐藏字段。
	Hidden bool `json:"hidden,omitempty"`
	// RefFrame 时间参考帧号（须 ≤ Frame 且 > 0）。
	RefFrame uint32 `json:"ref_frame,omitempty"`
	// PrevFrame 前一已显示帧号（须 < Frame 且 > 0）。
	PrevFrame uint32 `json:"prev_frame,omitempty"`
}

// Frame 单帧详情。fol 为 [协议, filter] 二元组数组；followers 为流跟随入口
// 对象数组；ds 为其余数据源（多数据源帧才有，形状未逐条实测，原样透传）。
type Frame struct {
	// Tree 协议树根节点数组。
	Tree []TreeNode `json:"tree"`
	// Columns 列值（参数 columns=true 时返回）。
	Columns []string `json:"col,omitempty"`
	// Bytes 帧原始字节（base64 解码后的原文）。
	Bytes []byte `json:"bytes"`
	// Comments 帧注释（pcapng OPT_COMMENT，数组）。
	Comments []string `json:"comment,omitempty"`
	// DS 其余数据源（原样透传）。
	DS json.RawMessage `json:"ds,omitempty"`
	// Fol 可跟随的流（[协议, filter] 二元组数组）。
	Fol json.RawMessage `json:"fol"`
	// Followers 可跟随的流明细（对象数组，形状随协议）。
	Followers json.RawMessage `json:"followers"`
	// Bg/Fg 着色规则命中的背景前景色。
	Bg string `json:"bg"`
	Fg string `json:"fg"`
	// I/M 该帧被忽略/被标记。
	I bool `json:"i,omitempty"`
	M bool `json:"m,omitempty"`
}

// TreeNode 协议树节点。层级树：l 必有，n 为子节点；h/i 为该节点覆盖的
// 字节区间 [起始, 长度]（h 展示区间，i 选中区间）。
type TreeNode struct {
	// L 节点标签（协议树面板显示文本）。
	L string `json:"l"`
	// H/I 字节区间 [start,len]。
	H []int `json:"h,omitempty"`
	I []int `json:"i,omitempty"`
	// P 字节区间 [start,len]（源码标注 RESERVED，保留字段）。
	P []int `json:"p,omitempty"`
	// E 子树 ett 索引（源码 doc 标 "字段取值"，实测协议树节点为 ett 索引）。
	E int64 `json:"e,omitempty"`
	// T 节点类型（"proto"、"framenum"、"url"）。
	T string `json:"t,omitempty"`
	// F 字段所属协议的 display filter 串。
	F string `json:"f,omitempty"`
	// FN 字段缩写（如 "icmp.type"）。
	FN string `json:"fn,omitempty"`
	// N 子节点。
	N []TreeNode `json:"n,omitempty"`
	// DS 该节点数据所在的数据源索引（多数据源帧）。
	DS int64 `json:"ds,omitempty"`
	// FNum 关联帧号（t=framenum 时）。
	FNum int64 `json:"fnum,omitempty"`
	// URL 关联链接（t=url 时）。
	URL string `json:"url,omitempty"`
	// S 专家信息 severity 文本。
	S string `json:"s,omitempty"`
	// G 该字段由 Wireshark 生成。
	G bool `json:"g,omitempty"`
	// V 该字段为隐藏字段（参数 hidden 时可见）。
	V bool `json:"v,omitempty"`
}

// CheckParams check 的参数：filter 校验 display filter，field 校验字段存在性
// （可只给一个；都不给 sharkd 返回 OK）。
type CheckParams struct {
	Filter string `json:"filter,omitempty"`
	Field  string `json:"field,omitempty"`
}

// CheckResult check 的响应：status 为 "OK" 或 "Warning"（另有 warning 文本，
// filter 仍有效）；字段不存在走 envelope error（-5002）。
type CheckResult struct {
	Status  string `json:"status"`
	Warning string `json:"warning,omitempty"`
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

// CompletionItem display filter 补全项（f 补全串，t FT 类型码，n 显示名）。
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

// FollowParams follow 的参数（follow/filter 必填）。
type FollowParams struct {
	// Follow 流类型（如 "TCP"、"UDP"，取自 info.follow）。
	Follow string `json:"follow"`
	// Filter 定位流的 display filter（如 "tcp.stream eq 0"）。
	Filter string `json:"filter"`
	// SubStream 子流号（HTTP/2、QUIC 等；0 = 不限制且不发该键）。
	SubStream uint32 `json:"sub_stream,omitempty"`
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
	// Payloads 流分片（n 帧号，d base64 解码后的数据，s 服务端方向标记）；无数据时缺省。
	Payloads []FollowPayload `json:"payloads,omitempty"`
}

// FollowPayload 流跟随的单个数据分片。
type FollowPayload struct {
	// N 该分片所在帧号。
	N int64 `json:"n"`
	// D 该帧方向上的载荷数据。
	D []byte `json:"d"`
	// S 非 0 表示该分片由服务端发出（源码 anyf "1" 输出数字）。
	S int `json:"s,omitempty"`
}

// TapParams tap 的参数：Taps 展开为 tap0..tapN（源码最多 16 路，超出报错；
// 多路时 sharkd 按序返回 taps 数组）。
type TapParams struct {
	// Taps tap 名列表（取自 info.taps 等，如 "expert"、"stat:http"）。
	Taps []string `json:"-"`
	// Filter tap 的 display filter（可选）。
	Filter string `json:"filter,omitempty"`
}

// TapResult tap 的响应。
type TapResult struct {
	Taps []TapOutput `json:"taps"`
}

// TapOutput 单个 tap 的输出。details 形状随 Type 变化，本 SDK 只对实测过的
// expert 类型提供字段，其余类型请改用 Call 透传。
type TapOutput struct {
	// Tap 请求的 tap 名；Type 输出类型（如 "expert"）。
	Tap  string `json:"tap"`
	Type string `json:"type"`
	// Details 明细行（expert 类型见 TapDetail）。
	Details []TapDetail `json:"details,omitempty"`
}

// TapDetail expert tap 的明细行。
type TapDetail struct {
	// F 帧号。
	F int64 `json:"f"`
	// S 严重度（如 "Warning"、"Comment"）。
	S string `json:"s"`
	// G 分组（如 "Checksum"、"Comment"）。
	G string `json:"g"`
	// P 协议（如 "ICMP"、"Packet"）。
	P string `json:"p"`
	// M 明细消息。
	M string `json:"m"`
}

// IntervalsParams intervals 的参数（全部可选，零值不发）。
type IntervalsParams struct {
	// Interval 桶宽（毫秒，默认 1000）。
	Interval uint32 `json:"interval,omitempty"`
	// Filter display filter（sharkd 语法）。
	Filter string `json:"filter,omitempty"`
}

// IntervalsResult intervals 的响应：按时间桶聚合的流量分布。
type IntervalsResult struct {
	// Intervals 桶三元组数组 [桶号, 帧数, 字节数]（桶号相对首帧）。
	Intervals [][3]int64 `json:"intervals"`
	// Last 最后桶的桶号。
	Last int64 `json:"last"`
	// Frames/Bytes 覆盖范围内的总帧数/总字节数。
	Frames int64 `json:"frames"`
	Bytes  int64 `json:"bytes"`
}

// IOGraphParams iograph 的参数。Graphs 必填（graph0），展开为 graph0..graph9
// （源码最多 10 路）；Filters/AOT 按下标与 Graphs 对齐，展开为 filter0..9、
// aot0..9。
type IOGraphParams struct {
	// Interval 桶宽（默认 1000）。
	Interval uint32 `json:"-"`
	// IntervalUnits Interval 的单位："s"、"ms"（默认）或 "us"。
	IntervalUnits string `json:"-"`
	// Graphs 每条曲线的计算方式："packets"、"bytes"、"bits"、
	// "sum:<field>"、"frames:<field>"、"fields:<field>"、"max:<field>"、
	// "min:<field>"、"avg:<field>"、"load:<field>"、"throughput:<field>"。
	Graphs []string `json:"-"`
	// Filters 每条曲线的 display filter（带 <field> 的 Graphs 须给字段所属 filter）。
	Filters []string `json:"-"`
	// AOT 逾时合计（advance over time）：true 时把此前各桶累计计入当前桶。
	AOT []bool `json:"-"`
}

// IOGraphResult iograph 的响应。
type IOGraphResult struct {
	// IOGraph 每条曲线一个元素（顺序同请求 graph0..N）。
	IOGraph []IOGraphItem `json:"iograph"`
}

// IOGraphItem 单条曲线的输出。
type IOGraphItem struct {
	// Items 图形值序列。0 值被服务端跳过，跳过的桶以下标十六进制字符串占位
	// （如 [1.0,"127",1.0]，"127" 表示第 0x127 桶）——故元素须按
	// json.RawMessage 区分数字与占位串。
	Items []json.RawMessage `json:"items,omitempty"`
}

// DownloadParams download 的参数（token 必填）。
type DownloadParams struct {
	// Token 导出对象令牌，形如 "eo:<name>_<row>"（export-object tap 产出）；
	// "ssl-secrets"、"rtp:..." 等其它前缀由 sharkd 自行处理，本包原样透传。
	Token string `json:"token"`
}

// DownloadResult download 的响应（data 为 base64 解码后的原文）。token 无对应
// 条目时 sharkd 返回空对象，此时三字段均为零值。
type DownloadResult struct {
	// File 建议文件名。
	File string `json:"file"`
	// Mime 建议 Content-Type。
	Mime string `json:"mime"`
	// Data 载荷。
	Data []byte `json:"data"`
}

// DumpConfParams dumpconf 的参数（pref 可为模块名、全名偏好项或缺省取全部）。
type DumpConfParams struct {
	Pref string `json:"pref,omitempty"`
}

// DumpConfResult dumpconf 的响应。prefs 是扁平的全名键（"模块.偏好项"），
// 即使请求只给了模块名也不按模块嵌套（实测确认）。
type DumpConfResult struct {
	Prefs map[string]PrefValue `json:"prefs"`
}

// PrefValue 单个偏好项的值对象（u/ub/b/s/e/r 至多一种；t 为 UAT 表）。
// u/ub/b 用指针以区分缺省（如 b:0 与未返回 b）。
type PrefValue struct {
	// U PREF_UINT 当前值。
	U *uint32 `json:"u,omitempty"`
	// UB PREF_UINT 建议显示基数（仅基数为 10 时缺省）。
	UB *uint32 `json:"ub,omitempty"`
	// B PREF_BOOL 当前值（0/1）。
	B *int `json:"b,omitempty"`
	// S PREF_STRING/文件名/目录/密码/dissector 类的当前值（形状按源码，未单独实测）。
	S string `json:"s,omitempty"`
	// E PREF_ENUM 可选值列表。
	E []PrefEnumVal `json:"e,omitempty"`
	// R PREF_RANGE 当前区间串（如 "67-68,4011"）。
	R string `json:"r,omitempty"`
	// T PREF_UAT 表内容（数组套数组，未逐条实测，原样透传）。
	T json.RawMessage `json:"t,omitempty"`
}

// PrefEnumVal PREF_ENUM 的单个可选值（s=1 表示当前选中）。
type PrefEnumVal struct {
	V int `json:"v"`
	// S 当前选中标记（1 = 选中，源码 anyf "1" 输出数字）。
	S int `json:"s,omitempty"`
	// D 该取值的描述。
	D string `json:"d"`
}

// SetConfParams setconf 的参数（name/value 必填，value 随偏好类型可为数字串）。
type SetConfParams struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// SetCommentParams setcomment 的参数（frame/comment 必填；sharkd 只增不删）。
type SetCommentParams struct {
	// Frame 帧号（正整数）。
	Frame uint32 `json:"frame"`
	// Comment 要追加的注释。
	Comment string `json:"comment"`
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
