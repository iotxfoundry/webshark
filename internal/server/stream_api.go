// stream_api.go - 流接入面：POST /api/subscribe（建流并回读状态）、GET /api/frame
// （单帧详情，按复合键路由实时实例/分片缓存实例）、POST /api/follow|tap（流级
// 交互查询透传）。参数集与行为契约见 docs/api.md 第 2 节。
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/iotxfoundry/webshark/internal/stream"
)

// maxTapDetails 单次 tap 明细上限，按帧序保留前 N 条：界面上万条明细也无法
// 人工阅读，超出部分由 Wireshark 桌面端查看——传输与 JSON 解析随分片大小
// 线性增长（8MB 分片实测 72000 条 ≈ 6.5MB）。
const maxTapDetails = 5000

// handleSubscribe POST /api/subscribe：{handle, filter?, paused?} 建流/保活并
// 回读流状态（handle 由前端生成并长期复用，服务端幂等）。流数达上限 429；
// 应答带 cold：该 handle 原先没有可用会话（新建/宽限过期/已被容量淘汰），
// 前端据此清空本地视图（追帧位置已丢失，新流锚定最新分片不回放历史）。
func (s *Server) handleSubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handle string  `json:"handle"`
		Filter *string `json:"filter"`
		Paused *bool   `json:"paused"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil || req.Handle == "" {
		writeErr(w, http.StatusBadRequest, http.StatusBadRequest, "subscribe 须携带 handle")
		return
	}
	// cold 判定在 GetOrCreate 之前查一次（两者非原子）：并发建同一 handle 时
	// 两方都报 cold 只是多清一次空视图，无正确性影响
	cold := s.mgr.Lookup(req.Handle) == nil
	sess, err := s.mgr.GetOrCreate(req.Handle)
	if errors.Is(err, stream.ErrStreamLimit) {
		writeErr(w, http.StatusTooManyRequests, http.StatusTooManyRequests, err.Error())
		return
	}
	if errors.Is(err, stream.ErrClosed) { // 服务已关闭：暂不可用，非内部错误
		writeErr(w, http.StatusServiceUnavailable, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, http.StatusInternalServerError, err.Error())
		return
	}
	if req.Filter != nil { // 重连时原子重建流状态，免得数据先于 _ctl 到达
		sess.RequestFilter(*req.Filter)
	}
	if req.Paused != nil {
		sess.SetPaused(*req.Paused)
	}
	sess.Touch()
	writeJSON(w, map[string]any{
		"ok": true, "handle": sess.Handle(),
		"paused": sess.Paused(), "filter": sess.Filter(),
		"shard": sess.Shard(), "cold": cold,
	})
}

// handleFrame GET /api/frame?handle=&shard?=&num=&proto?=&bytes?=：单帧详情，
// 一律走分片缓存实例（按需 load，LRU 12 片）；shard 缺省取会话当前分片，仍未
// 就绪 409。sharkd envelope 错误：load 失败 404；传输失败 503。
func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sess := s.mgr.Lookup(q.Get("handle"))
	if sess == nil {
		writeErr(w, http.StatusNotFound, http.StatusNotFound, "流不存在或已过期")
		return
	}
	num, err := strconv.Atoi(q.Get("num"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, http.StatusBadRequest, "num 须为整数帧号")
		return
	}
	// 单帧详情标志透传：proto/bytes 仅存在时解析为布尔（缺省不携键）。校验前移：
	// 参数合法性先于路由就绪性。
	params := map[string]any{"frame": num}
	for _, key := range []string{"proto", "bytes"} {
		v, ok := extractBool(q, key)
		if !ok {
			writeErr(w, http.StatusBadRequest, http.StatusBadRequest, key+" 须为布尔值")
			return
		}
		if v != nil {
			params[key] = *v
		}
	}
	cur := sess.Shard()
	shard := q.Get("shard")
	if shard == "" {
		shard = cur
	}
	if shard == "" {
		writeErr(w, http.StatusConflict, http.StatusConflict, "实时实例尚未就绪（未 load 任何分片）")
		return
	}
	if strings.ContainsAny(shard, `/\`) { // 纯文件名校验，防任意路径穿越
		writeErr(w, http.StatusBadRequest, http.StatusBadRequest, "shard 须为纯文件名")
		return
	}
	// 不按 shard==cur 分流实时实例：worker 侧 load 与 setCurShard 两步非原子，
	// 轮换瞬间 handler 读到旧 cur、请求落到已换文件的新实例上，会取到另一分片
	// 的同号帧。一律走分片缓存实例（按复合键 load 目标文件后查询，无此竞态；
	// 代价是同片首次多一次 load ≈ 百毫秒，LRU 驻留后命中零 load）。
	res, err := s.cache.frame(r.Context(), shard, params)
	if err != nil {
		writeSharkErr(w, err, http.StatusNotFound, "分片不存在或已被清理")
		return
	}
	sess.Touch()
	writeJSON(w, json.RawMessage(res))
}

// extractBool 取查询参数的布尔值：缺失返回 (nil, true)；存在但非法 (nil, false)。
func extractBool(q map[string][]string, key string) (*bool, bool) {
	v, ok := q[key]
	if !ok || len(v) == 0 || v[0] == "" {
		return nil, true
	}
	b, err := strconv.ParseBool(v[0])
	if err != nil {
		return nil, false
	}
	return &b, true
}

// handleStreamRPC follow/tap 共用体：body {handle, ...方法参数}——删除 handle
// 后原样透传给该流实时实例，响应 JSON 透传。参数不做语义校验（参数集由前端与
// sharkd 协议定义）。envelope 错误→400，传输失败→503，流不存在→404。
func (s *Server) handleStreamRPC(method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			writeErr(w, http.StatusBadRequest, http.StatusBadRequest, "body 须为 JSON 对象")
			return
		}
		handle, _ := body["handle"].(string)
		if handle == "" {
			writeErr(w, http.StatusBadRequest, http.StatusBadRequest, "须携带 handle")
			return
		}
		sess := s.mgr.Lookup(handle)
		if sess == nil {
			writeErr(w, http.StatusNotFound, http.StatusNotFound, "流不存在或已过期")
			return
		}
		delete(body, "handle")
		before := sess.Shard() // tap 补注分片的判据（见下）
		res, err := sess.Call(method, body)
		if err != nil {
			writeSharkErr(w, err, http.StatusBadRequest, "查询失败")
			return
		}
		if method == "tap" { // 同级补注实际载入分片：前端无从得知 sharkd 会话此刻
			// load 的文件（轮换瞬间猜测必错），服务端权威告知。调用前后不一致说明
			// tap 期间发生过换片，无法判定实际文件 —— 留空让前端提示而非给错分片。
			// 并按帧序截断明细（maxTapDetails），丢弃总数经 truncated 披露。
			var obj map[string]json.RawMessage
			if json.Unmarshal(res, &obj) == nil {
				loaded := before
				if sess.Shard() != before {
					loaded = ""
				}
				shard, _ := json.Marshal(loaded)
				obj["shard"] = shard
				truncated, _ := json.Marshal(0)
				if capped, dropped := capTapDetails(obj["taps"]); capped != nil {
					obj["taps"] = capped
					truncated, _ = json.Marshal(dropped)
				}
				obj["truncated"] = truncated
				if b, merr := json.Marshal(obj); merr == nil {
					res = b
				} // 解析/重编码失败原样透传，不因附加键报错
			}
		}
		sess.Touch()
		writeJSON(w, json.RawMessage(res))
	}
}

// capTapDetails 截断 taps 各明细数组：跨 tap 共享 maxTapDetails 预算，按帧序
// 保留前 N 条，返回截断后的 taps 与丢弃总条数。taps 缺失/形态不符返回
// (nil, 0)（调用方原样保留，不因截断逻辑报错）。
func capTapDetails(raw json.RawMessage) (json.RawMessage, int) {
	var taps []struct {
		Tap     string            `json:"tap"`
		Type    string            `json:"type"`
		Details []json.RawMessage `json:"details"`
	}
	if json.Unmarshal(raw, &taps) != nil {
		return nil, 0
	}
	dropped, budget := 0, maxTapDetails
	for i := range taps {
		if n := len(taps[i].Details); n <= budget {
			budget -= n
			continue
		}
		dropped += len(taps[i].Details) - budget
		taps[i].Details = taps[i].Details[:budget]
		budget = 0
	}
	if dropped == 0 {
		return raw, 0
	}
	b, err := json.Marshal(taps)
	if err != nil {
		return nil, 0
	}
	return b, dropped
}
