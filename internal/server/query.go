// query.go - 轻查询路由：GET|POST /api/check（dfilter 校验）与 /api/complete
// （补全候选）。走进程级轻查询实例（不 load 文件，与追帧隔离）。GET 与 POST
// 双形态：同名键 body 覆盖 query。sharkd envelope 错误→400，传输失败→503。
package server

import (
	"encoding/json"
	"io"
	"net/http"
)

// queryBodyMax 轻查询 body 上限。
const queryBodyMax = 4 << 10

// queryInput 合并 GET query 与 POST body 的字符串参数（body 优先；非字符串值忽略）。
func (s *Server) queryInput(w http.ResponseWriter, r *http.Request) (map[string]string, bool) {
	in := map[string]string{}
	if r.Method == http.MethodPost {
		var body map[string]any
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, queryBodyMax))
		if err != nil || json.Unmarshal(raw, &body) != nil {
			writeErr(w, http.StatusBadRequest, http.StatusBadRequest, "body 须为 JSON 对象")
			return nil, false
		}
		for k, v := range body {
			if sv, ok := v.(string); ok {
				in[k] = sv
			}
		}
	}
	for k, vs := range r.URL.Query() {
		if _, ok := in[k]; !ok && len(vs) > 0 {
			in[k] = vs[0]
		}
	}
	return in, true
}

// handleCheck GET|POST /api/check：dfilter 语法校验，响应透传。
func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	in, ok := s.queryInput(w, r)
	if !ok {
		return
	}
	s.lightCall(w, "check", map[string]string{"filter": in["filter"]})
}

// handleComplete GET|POST /api/complete：dfilter 补全。
// 前端统一发 prefix（当前输入前缀）；sharkd 没有 prefix 成员（发它会拒
// -32600 "not a valid member name"），只有 field（字段）与 pref（协议/首选项）
// 两个补全域，故展开为同值双键，响应中 field/pref 两数组由前端合并展示。
// 显式 field 覆盖字段域取值（特殊用法）。
func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	in, ok := s.queryInput(w, r)
	if !ok {
		return
	}
	prefix := in["prefix"]
	field := prefix
	if in["field"] != "" {
		field = in["field"]
	}
	params := map[string]string{}
	if field != "" {
		params["field"] = field
	}
	if prefix != "" {
		params["pref"] = prefix
	}
	s.lightCall(w, "complete", params)
}

// lightCall 轻查询实例调用并应答（JSON 透传）。
func (s *Server) lightCall(w http.ResponseWriter, method string, params map[string]string) {
	res, err := s.mgr.Light().Call(method, params)
	if err != nil {
		writeSharkErr(w, err, http.StatusBadRequest, "查询失败")
		return
	}
	writeJSON(w, json.RawMessage(res))
}
