// errors.go - 统一错误应答助手：HTTP 状态码 + {"code","message"} JSON。
// code 为 sharkd envelope 的 code（透传时）或 HTTP 状态码；成功应答不经此路径。
package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/iotxfoundry/webshark/internal/sharkd"
)

// writeErr 错误应答（status 与 message 见 docs/api.md 第 1 节）。
func writeErr(w http.ResponseWriter, status, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: msg})
}

// writeJSON 成功 JSON 应答。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// writeSharkErr sharkd 调用错误分级应答：envelope 错误透传 code/message（HTTP
// 状态取 fallback——历史分片 load 失败 404、业务错误 400）；传输失败（连接断、
// 超时等）恒 503。
func writeSharkErr(w http.ResponseWriter, err error, fallback int, fallbackMsg string) {
	var se *sharkd.Error
	if errors.As(err, &se) { // As 而非断言：被 %w 包装后仍透传，不静默降级 503
		writeErr(w, fallback, se.Code, se.Message)
		return
	}
	writeErr(w, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "sharkd 查询失败: "+err.Error())
}
