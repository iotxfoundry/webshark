package stream

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWithRef 锁定行复合键手写拼接：常规对象追加 ref；空对象 `{}` 同样正确；
// ref.num 与行内 num 一致；非法 JSON 退化原行不拼接。
func TestWithRef(t *testing.T) {
	// 常规对象
	out := withRef([]byte(`{"num":12,"t":"1700000000.000001"}`), "s1.pcapng", 12)
	require.JSONEq(t, `{"num":12,"t":"1700000000.000001","ref":{"shard":"s1.pcapng","num":12}}`, string(out))
	// 原字段不丢失且 ref 可独立解析
	var m map[string]any
	require.NoError(t, json.Unmarshal(out, &m))
	require.Equal(t, "s1.pcapng", m["ref"].(map[string]any)["shard"])

	// 空对象
	require.JSONEq(t, `{"ref":{"shard":"x.pcapng","num":0}}`,
		string(withRef([]byte(`{}`), "x.pcapng", 0)))

	// 带尾部空白
	require.JSONEq(t, `{"num":1,"ref":{"shard":"s","num":1}}`,
		string(withRef([]byte(`{"num":1}  `), "s", 1)))

	// 多层嵌套行（结尾仍是对象闭合）
	nested := `{"num":3,"tree":{"c":[{"i":"a"}]}}`
	require.JSONEq(t, `{"num":3,"tree":{"c":[{"i":"a"}]},"ref":{"shard":"s","num":3}}`,
		string(withRef([]byte(nested), "s", 3)))

	// 非对象行（数组）：保底包一层不丢 ref
	arr := withRef([]byte(`[1,2]`), "s", 5)
	var w struct {
		Row []any          `json:"row"`
		Ref map[string]any `json:"ref"`
	}
	require.NoError(t, json.Unmarshal(arr, &w))
	require.Equal(t, "s", w.Ref["shard"])

	// 非法 JSON：退化原行
	bad := []byte(`{bad`)
	require.Equal(t, bad, []byte(withRef(bad, "s", 1)))
}

// TestWithRefRow 锁定 num 自动取自行内 num 字段。
func TestWithRefRow(t *testing.T) {
	out := withRefRow(json.RawMessage(`{"num":7,"l":42}`), "a.pcapng")
	var row struct {
		Num int64 `json:"num"`
		Ref struct {
			Shard string `json:"shard"`
			Num   int64  `json:"num"`
		} `json:"ref"`
	}
	require.NoError(t, json.Unmarshal(out, &row))
	require.Equal(t, int64(7), row.Num)
	require.Equal(t, "a.pcapng", row.Ref.Shard)
	require.Equal(t, int64(7), row.Ref.Num)
}

// TestWireFrames 锁定下行线格式契约（docs/api.md 第 3 节，字段名/取值不得变更）。
func TestWireFrames(t *testing.T) {
	require.JSONEq(t, `{"type":"hello","mode":"live"}`, string(HelloFrame("live")))
	require.JSONEq(t,
		`{"type":"rows","mode":"filter","shard":"s1.pcapng","rows":[{"num":1}]}`,
		string(RowsFrame("filter", "s1.pcapng", []json.RawMessage{json.RawMessage(`{"num":1}`)})))
	require.JSONEq(t, `{"type":"rotated","shard":"s2.pcapng"}`, string(RotatedFrame("s2.pcapng")))
	require.JSONEq(t, `{"type":"reset","reason":"realign"}`, string(ResetFrame("realign")))
	require.JSONEq(t, `{"type":"error","reason":"x"}`, string(ErrorFrame("x")))
}

// TestHandleCtl 锁定上行 _ctl 流控制解析：pause/resume、filter 进/出、
// 非 _ctl/坏 JSON/缺 filter 字段静默忽略。
func TestHandleCtl(t *testing.T) {
	spool := t.TempDir()
	m := NewManager(Config{Sock: "", SpoolDir: spool})
	t.Cleanup(m.Close)
	s, err := m.GetOrCreate("ctl")
	require.NoError(t, err)

	HandleCtl(s, []byte(`{"type":"_ctl","op":"capture","state":"pause"}`))
	require.True(t, s.Paused())
	HandleCtl(s, []byte(`{"type":"_ctl","op":"capture","state":"resume"}`))
	require.False(t, s.Paused())

	HandleCtl(s, []byte(`{"type":"_ctl","op":"filter","filter":"tcp"}`))
	f, ok := s.takePendingFilter()
	require.True(t, ok)
	require.Equal(t, "tcp", f)

	// 空串=退出筛选（仍是一条待生效请求）
	HandleCtl(s, []byte(`{"type":"_ctl","op":"filter","filter":""}`))
	f, ok = s.takePendingFilter()
	require.True(t, ok)
	require.Equal(t, "", f)

	// 其他形态/坏 JSON/缺 filter 键：静默忽略
	HandleCtl(s, []byte(`{"type":"hello","op":"capture","state":"pause"}`))
	HandleCtl(s, []byte(`{bad json`))
	HandleCtl(s, []byte(`{"type":"_ctl","op":"filter"}`))
	_, ok = s.takePendingFilter()
	require.False(t, ok)
	require.False(t, s.Paused())
}
