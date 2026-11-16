package sharkd

import (
	"context"
	"encoding/json"
	"fmt"
)

// 本文件的 typed 方法是 Call 之上的薄封装：统一签名
// func (c *Client) Xxx(ctx context.Context, ...) (T, error)，内部走 Call +
// json.Unmarshal。未实测的方法（iograph、download）不提供 typed 封装，
// 用 Call 自行构造参数并解析 RawMessage。会话状态提醒：重拨后的新会话无
// load 状态，长生命周期客户端应在 Call 错误后重新 Load。

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

// checkOK 操作型方法共用的 status 断言。
func checkOK(res *LoadResult) error {
	if res.Status != "OK" {
		return fmt.Errorf("sharkd 状态非 OK: %s", res.Status)
	}
	return nil
}

// Load 加载 pcapng/pcap 文件（load 是内存快照，文件追加后须重 load 才可见）。
func (c *Client) Load(ctx context.Context, file string) (*LoadResult, error) {
	return callInto[LoadResult](ctx, c, "load", map[string]string{"file": file})
}

// Info 查询 sharkd 能力清单：版本、默认列定义、可用 tap 名单等。
func (c *Client) Info(ctx context.Context) (*Info, error) {
	return callInto[Info](ctx, c, "info", map[string]any{})
}

// Frames 按游标取帧列表原始行（数组元素为 RawMessage，字段随 sharkd 版本，
// 服务端路径零解析损耗）。需结构化行时用 DecodeFrameRows。
func (c *Client) Frames(ctx context.Context, p FramesParams) ([]json.RawMessage, error) {
	res, err := c.Call(ctx, "frames", p)
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

// Frame 取单帧详情：协议树（proto）与原始字节（bytes）按参数开关返回。
func (c *Client) Frame(ctx context.Context, p FrameParams) (*Frame, error) {
	return callInto[Frame](ctx, c, "frame", p)
}

// Check 校验 display filter 语法；结果含可选 warning 文本（filter 仍有效）。
func (c *Client) Check(ctx context.Context, filter string) (*CheckResult, error) {
	return callInto[CheckResult](ctx, c, "check", map[string]string{"filter": filter})
}

// Complete display filter 字段/协议或偏好项补全（field 与 pref 二选一）。
func (c *Client) Complete(ctx context.Context, p CompleteParams) (*CompletionSet, error) {
	return callInto[CompletionSet](ctx, c, "complete", p)
}

// Follow 流跟随：返回两端地址/端口/字节数与分片载荷。
func (c *Client) Follow(ctx context.Context, p FollowParams) (*FollowResult, error) {
	return callInto[FollowResult](ctx, c, "follow", p)
}

// Tap 执行一路 tap 采集（如 expert 专家信息）。
func (c *Client) Tap(ctx context.Context, p TapParams) (*TapResult, error) {
	return callInto[TapResult](ctx, c, "tap", p)
}

// Intervals 取整份文件的按时间桶聚合的流量分布。
func (c *Client) Intervals(ctx context.Context) (*IntervalsResult, error) {
	return callInto[IntervalsResult](ctx, c, "intervals", map[string]any{})
}

// SetConf 设置偏好项（如 "gui.console_open"）；status 非 OK 报错。
func (c *Client) SetConf(ctx context.Context, name, value string) error {
	res, err := callInto[LoadResult](ctx, c, "setconf", map[string]string{"name": name, "value": value})
	if err != nil {
		return err
	}
	return checkOK(res)
}

// Bye 结束 sharkd 会话（子进程退出）；status 非 OK 报错。
func (c *Client) Bye(ctx context.Context) error {
	res, err := callInto[LoadResult](ctx, c, "bye", map[string]any{})
	if err != nil {
		return err
	}
	return checkOK(res)
}
