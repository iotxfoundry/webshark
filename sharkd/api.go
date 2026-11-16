package sharkd

import (
	"context"
	"encoding/json"
	"fmt"
)

// 本文件是 Call 之上的 typed 方法层，签名统一为
// func (c *Client) Xxx(ctx context.Context, p XxxParams) (T, error)。
// 方法集覆盖 sharkd 的全部 RPC 方法，参数名与响应字段保持协议原文；动态键
// （column0..N、tap0..15、graph0..9 等）由参数结构体展开。会话状态提醒：
// 重拨后的新会话无 load 状态，长生命周期客户端应在 Call 错误后重新 Load。

// callInto 发起请求并把 result 解码到 T（对象型结果的公共路径）。
func callInto[T any](ctx context.Context, c *Client, method string, params any) (*T, error) {
	res, err := c.Call(ctx, method, params)
	if err != nil {
		return nil, err
	}
	var v T
	if err := json.Unmarshal(res, &v); err != nil {
		return nil, fmt.Errorf("解析 %s 响应失败: %w", method, err)
	}
	return &v, nil
}

// checkOK 操作型方法共用的 status 断言：Err != 0（load 失败的 result 体形态）
// 与 Status != "OK" 都视为失败。
func checkOK(res *OKResult) error {
	if res.Err != 0 {
		return fmt.Errorf("sharkd 操作失败: %s (err=%d)", res.Status, res.Err)
	}
	if res.Status != "OK" {
		return fmt.Errorf("sharkd 状态非 OK: %s", res.Status)
	}
	return nil
}

// Load 加载 pcapng/pcap 文件。失败有两种形态：envelope error（如文件不存在
// -2001）或 result 体 {status:<errstr>,err:<code>}，统一返回 error。
func (c *Client) Load(ctx context.Context, p LoadParams) (*OKResult, error) {
	res, err := callInto[OKResult](ctx, c, "load", p)
	if err != nil {
		return nil, err
	}
	return res, checkOK(res)
}

// Info 查询 sharkd 能力清单：版本、默认列定义、可用 tap 名单等。
func (c *Client) Info(ctx context.Context) (*Info, error) {
	return callInto[Info](ctx, c, "info", map[string]any{})
}

// Status 查询当前已加载文件的概览：帧数、时长、文件名/大小、列定义。
func (c *Client) Status(ctx context.Context) (*StatusResult, error) {
	return callInto[StatusResult](ctx, c, "status", map[string]any{})
}

// Analyse 取整份文件出现的协议清单与首末帧时间。
func (c *Client) Analyse(ctx context.Context) (*AnalyseResult, error) {
	return callInto[AnalyseResult](ctx, c, "analyse", map[string]any{})
}

// Field 查询单个字段（filter 名）的 id/parent_id/类型等描述。
func (c *Client) Field(ctx context.Context, p FieldParams) (*FieldInfo, error) {
	return callInto[FieldInfo](ctx, c, "field", p)
}

// Fields 查询全部已启用协议的字段描述（约 23 万条、10MB 级；单查用 Field）。
func (c *Client) Fields(ctx context.Context) (*FieldsResult, error) {
	return callInto[FieldsResult](ctx, c, "fields", map[string]any{})
}

// Frames 按游标取帧列表原始行（数组元素为 RawMessage，字段随 sharkd 版本，
// 服务端路径零解析损耗）。需结构化行时用 DecodeFrameRows。
// Columns 一旦给出即整列集替换（column0..N 须一次给全，见 FramesParams）。
func (c *Client) Frames(ctx context.Context, p FramesParams) ([]json.RawMessage, error) {
	params := map[string]any{}
	if p.Skip != 0 {
		params["skip"] = p.Skip
	}
	if p.Limit != 0 {
		params["limit"] = p.Limit
	}
	if p.Filter != "" {
		params["filter"] = p.Filter
	}
	if p.Refs != "" {
		params["refs"] = p.Refs
	}
	for i, col := range p.Columns {
		params[fmt.Sprintf("column%d", i)] = col
	}
	res, err := c.Call(ctx, "frames", params)
	if err != nil {
		return nil, err
	}
	var rows []json.RawMessage
	if err := json.Unmarshal(res, &rows); err != nil {
		return nil, fmt.Errorf("解析 frames 响应失败: %w", err)
	}
	return rows, nil
}

// DecodeFrameRows 把 Frames 的行解析为 FrameRow（未知字段丢弃）。
func DecodeFrameRows(rows []json.RawMessage) ([]FrameRow, error) {
	out := make([]FrameRow, len(rows))
	for i, r := range rows {
		if err := json.Unmarshal(r, &out[i]); err != nil {
			return nil, fmt.Errorf("解析 frames 第 %d 行失败: %w", i+1, err)
		}
	}
	return out, nil
}

// Frame 取单帧详情：协议树（proto）、列值（columns）、原始字节（bytes）等
// 按参数开关返回（布尔参数存在即开启，零值不发）。
func (c *Client) Frame(ctx context.Context, p FrameParams) (*Frame, error) {
	return callInto[Frame](ctx, c, "frame", p)
}

// Check 校验 display filter（filter）或字段存在性（field），二者可只给其一；
// Warning 表示 filter 含废弃写法但仍有效。
func (c *Client) Check(ctx context.Context, p CheckParams) (*CheckResult, error) {
	return callInto[CheckResult](ctx, c, "check", p)
}

// Complete display filter 字段/协议或偏好项补全（field 与 pref 二选一）。
func (c *Client) Complete(ctx context.Context, p CompleteParams) (*CompletionSet, error) {
	return callInto[CompletionSet](ctx, c, "complete", p)
}

// Follow 流跟随：返回两端地址/端口/字节数与分片载荷（s 标记服务端方向）。
func (c *Client) Follow(ctx context.Context, p FollowParams) (*FollowResult, error) {
	return callInto[FollowResult](ctx, c, "follow", p)
}

// Tap 执行一路或多路 tap 采集（如 expert 专家信息）；最多 16 路（tap0..tap15）。
func (c *Client) Tap(ctx context.Context, p TapParams) (*TapResult, error) {
	if len(p.Taps) > 16 {
		return nil, fmt.Errorf("sharkd 单次最多 16 路 tap（tap0..tap15），请求了 %d 路", len(p.Taps))
	}
	params := map[string]any{}
	for i, t := range p.Taps {
		params[fmt.Sprintf("tap%d", i)] = t
	}
	if p.Filter != "" {
		params["filter"] = p.Filter
	}
	return callInto[TapResult](ctx, c, "tap", params)
}

// Intervals 取整份文件按时间桶聚合的流量分布（可给桶宽与 filter）。
func (c *Client) Intervals(ctx context.Context, p IntervalsParams) (*IntervalsResult, error) {
	return callInto[IntervalsResult](ctx, c, "intervals", p)
}

// IOGraph 计算时序图数据（如包速率曲线）；最多 10 条曲线（graph0..graph9）。
// Items 中的十六进制字符串是 0 值桶的占位下标（见 IOGraphItem）。
func (c *Client) IOGraph(ctx context.Context, p IOGraphParams) (*IOGraphResult, error) {
	if len(p.Graphs) > 10 {
		return nil, fmt.Errorf("sharkd 单次最多 10 条曲线（graph0..graph9），请求了 %d 条", len(p.Graphs))
	}
	if len(p.Filters) > 10 {
		return nil, fmt.Errorf("sharkd 单次最多 10 条 filter（filter0..filter9），请求了 %d 条", len(p.Filters))
	}
	params := map[string]any{}
	if p.Interval != 0 {
		params["interval"] = p.Interval
	}
	if p.IntervalUnits != "" {
		params["interval_units"] = p.IntervalUnits
	}
	for i, g := range p.Graphs {
		params[fmt.Sprintf("graph%d", i)] = g
	}
	for i, f := range p.Filters {
		if f != "" {
			params[fmt.Sprintf("filter%d", i)] = f
		}
	}
	for i, a := range p.AOT {
		if a {
			params[fmt.Sprintf("aot%d", i)] = true
		}
	}
	return callInto[IOGraphResult](ctx, c, "iograph", params)
}

// Download 按 token 下载导出对象（如 "eo:http_3"）。token 无对应条目时
// sharkd 返回空对象，结果的字段均为零值。
func (c *Client) Download(ctx context.Context, p DownloadParams) (*DownloadResult, error) {
	return callInto[DownloadResult](ctx, c, "download", p)
}

// DumpConf 导出偏好项：pref 给模块名（如 "dhcp"）、全名（如
// "tcp.desegment_tcp_streams"）或缺省取全部；结果键恒为全名。
func (c *Client) DumpConf(ctx context.Context, p DumpConfParams) (*DumpConfResult, error) {
	return callInto[DumpConfResult](ctx, c, "dumpconf", p)
}

// SetConf 设置偏好项（如 name="gui.console_open"）；失败走 envelope error。
func (c *Client) SetConf(ctx context.Context, p SetConfParams) error {
	res, err := callInto[OKResult](ctx, c, "setconf", p)
	if err != nil {
		return err
	}
	return checkOK(res)
}

// SetComment 给帧追加注释（sharkd 只增不删）；失败可走 envelope error 或
// result 体 status（统一经 checkOK）。
func (c *Client) SetComment(ctx context.Context, p SetCommentParams) error {
	res, err := callInto[OKResult](ctx, c, "setcomment", p)
	if err != nil {
		return err
	}
	return checkOK(res)
}

// Bye 结束 sharkd 会话（子进程退出）。
func (c *Client) Bye(ctx context.Context) error {
	res, err := callInto[OKResult](ctx, c, "bye", map[string]any{})
	if err != nil {
		return err
	}
	return checkOK(res)
}
