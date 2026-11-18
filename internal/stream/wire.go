// wire.go - WebSocket 下行线格式（与前端泳道对齐的契约，字段名/取值不得变更）
// 与上行 _ctl 流控制解析。下行 JSON Lines：hello/rows/rotated/reset/error；
// 上行仅 _ctl（capture 暂停恢复 + filter 筛选进退），其余形态静默忽略不断连接。
package stream

import (
	"encoding/json"
	"fmt"
)

// HelloFrame 序列化握手确认帧 {"type":"hello","mode":"live"}（hello 帧仅 live 模式使用，mode 恒为 "live"）。
func HelloFrame() []byte {
	// 帧体恒定，序列化必成功。
	return mustJSON(struct {
		Type string `json:"type"`
		Mode string `json:"mode"`
	}{"hello", "live"})
}

// RowsFrame 序列化微批行帧：rows 为 sharkd frames 行原样 JSON（已含 ref 复合键）。
func RowsFrame(mode, shard string, rows []json.RawMessage) []byte {
	// rows 原样透传，不重编码。
	return mustJSON(struct {
		Type  string            `json:"type"`
		Mode  string            `json:"mode"`
		Shard string            `json:"shard"`
		Rows  []json.RawMessage `json:"rows"`
	}{"rows", mode, shard, rows})
}

// RotatedFrame 序列化轮换通知帧 {"type":"rotated","shard":shard}。
func RotatedFrame(shard string) []byte {
	// 帧体恒定，序列化必成功。
	return mustJSON(struct {
		Type  string `json:"type"`
		Shard string `json:"shard"`
	}{"rotated", shard})
}

// ResetFrame 序列化对齐重推帧 {"type":"reset","reason":reason}。
func ResetFrame(reason string) []byte {
	// 帧体恒定，序列化必成功。
	return mustJSON(struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}{"reset", reason})
}

// ErrorFrame 序列化错误提示帧（筛选校验失败等 UI 提示场景）。
func ErrorFrame(reason string) []byte {
	// 帧体恒定，序列化必成功。
	return mustJSON(struct {
		Type   string `json:"type"`
		Reason string `json:"reason"`
	}{"error", reason})
}

// mustJSON 序列化纯静态结构（marshal 恒成功）。
func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// HandleCtl 解析一条 WS 上行 _ctl 文本（其他形态静默忽略）。提交仅入待生效队列，
// worker tick 取走执行（走查等重活不占 WS 读循环）。受控形态：
//
//	_ctl{op:"capture",state:"pause"|"resume"} —— 追帧暂停/恢复；
//	_ctl{op:"filter",filter} —— 筛选模式进/出/切换（空串=退出）。
func HandleCtl(s *Session, msg []byte) {
	// 坏 JSON 或非 _ctl 形态：静默忽略，连接不断。
	var m struct {
		Type   string  `json:"type"`
		Op     string  `json:"op"`
		State  string  `json:"state"`
		Filter *string `json:"filter"`
	}
	if json.Unmarshal(msg, &m) != nil || m.Type != "_ctl" {
		return // 非 _ctl 上行（坏 JSON/其他形态）忽略，连接不断
	}
	switch m.Op {
	case "capture":
		switch m.State {
		case "pause":
			s.SetPaused(true)
		case "resume":
			s.SetPaused(false)
		}
	case "filter":
		if m.Filter != nil {
			s.RequestFilter(*m.Filter)
		}
	}
}

// withRef 行透传 + 追加 ref 复合键（{"shard":分片名,"num":帧号}，前端点行查
// 详情的查询键）。字节拼接而非再解一层 JSON：{} 空对象与常规对象都正确，
// 拼接失败（非法 JSON 行）退化为原行。
func withRef(row []byte, shard string, num int64) []byte {
	if !json.Valid(row) {
		return row
	}
	end := stripTailObject(row)
	ref := fmt.Sprintf(`{"shard":%q,"num":%d}`, shard, num)
	if end == nil {
		// 非对象（数组等）：包一层对象退化，保住 ref 键可用
		return fmt.Appendf(nil, `{"row":%s,"ref":%s}`, row, ref)
	}
	if emptyObject(end) { // 空对象 {}：无前导逗号
		out := make([]byte, 0, len(end)+len(`"ref":`)+len(ref)+1)
		out = append(out, end...)
		out = append(out, `"ref":`...)
		out = append(out, ref...)
		out = append(out, '}')
		return out
	}
	return append(end, []byte(`,"ref":`+ref+`}`)...)
}

// withRefRow 行透传 + 追加 ref 复合键；num 取自行内 num 字段（sharkd 行恒带）。
func withRefRow(row json.RawMessage, shard string) json.RawMessage {
	var head struct {
		Num int64 `json:"num"`
	}
	_ = json.Unmarshal(row, &head)
	return json.RawMessage(withRef(row, shard, head.Num))
}

// stripTailObject 返回去掉尾部 '}' 的对象字节（row 须为 JSON 对象，空对象
// `{}` 与含成员对象均可，返回值为去 '}' 后的前缀）；非对象返回 nil。
func stripTailObject(row []byte) []byte {
	i := len(row) - 1
	for i >= 0 && (row[i] == ' ' || row[i] == '\t' || row[i] == '\n' || row[i] == '\r') {
		i--
	}
	if i < 0 || row[i] != '}' || row[0] != '{' {
		return nil
	}
	return row[:i]
}

// emptyObject 报告 b（已去尾 '}' 的对象体）是否为空对象体（b 为 `{` 起始的
// 已剥外括号片段，含尾部残空白即视为空——仅用于恒定输入的 sharkd 行）。
func emptyObject(body []byte) bool {
	for _, c := range body {
		if c == '{' {
			continue // 保留开头大括号：stripTailObject 只剥 '}'
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			return false
		}
	}
	return true
}
