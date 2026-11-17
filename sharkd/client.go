// Package sharkd 是 sharkd 的 Go 客户端 SDK。
//
// sharkd 是 Wireshark 自带的协议解析守护进程，经 Unix socket 提供行式 JSON-RPC
// 接口：加载 pcapng 文件、按游标取帧行、取单帧协议树与原始字节、校验与补全
// display filter、流跟随、专家信息与统计等。本包负责连接管理（懒拨号、断线
// 重拨、单会话串行请求）、超时与 envelope 错误映射。
//
// 方法集覆盖 sharkd 的全部 RPC 方法（19 个），参数名与响应字段保持协议原文，
// 动态键由参数结构体展开：
//
//	Analyse      分析整份文件的协议清单
//	Bye          结束会话
//	Check        校验 display filter / 字段
//	Complete     display filter / 偏好项补全
//	Download     下载导出对象（token）
//	DumpConf     导出偏好项
//	Field        查询单个字段描述
//	Fields       查询全部字段描述
//	Follow       流跟随
//	Frame        单帧详情（协议树/列/字节）
//	Frames       帧列表原始行
//	Info         sharkd 能力清单
//	Intervals    按时间桶的流量分布
//	IOGraph      时序图数据
//	Load         加载抓包文件
//	SetComment   追加帧注释
//	SetConf      设置偏好项
//	Status       已加载文件概览
//	Tap          tap 采集（最多 16 路）
//
// 稳定性：本包尚未打版本 tag，导出名字与类型可能随实测与 sharkd 版本调整。
//
// 最小用法：
//
//	c := sharkd.NewClient("/run/webshark/sharkd.sock")
//	defer c.Close()
//
//	if _, err := c.Load(ctx, sharkd.LoadParams{File: "/var/lib/webshark/net.pcapng"}); err != nil {
//		log.Fatal(err)
//	}
//	rows, err := c.Frames(ctx, sharkd.FramesParams{Limit: 100})
//
// 并发：一个 Client 对应一条 sharkd 会话，内部互斥串行（sharkd 协议是一问一答
// 的行式协议，并发请求会串话）。需要并发就各建一个 Client（sharkd 每连接一个
// 子进程）。单次请求默认超时 DefaultTimeout，ctx 的更早 deadline 优先；ctx
// 取消会丢弃当前连接——半拆的响应会让会话失步，下次调用自愈重拨。
package sharkd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	// DefaultTimeout 单次请求默认超时（含拨号与整条响应读取）。
	DefaultTimeout = 10 * time.Second
	// dialTimeout 拨号超时上限。
	dialTimeout = 5 * time.Second
	// readBuf bufio.Scanner 初始缓冲。
	readBuf = 64 << 10
	// maxMsg 单条响应上限（8MiB 分片装配出的 envelope；info 约 30KB）。
	maxMsg = 32 << 20
)

// Error 是 sharkd envelope 错误（协议层拒绝，如 load 文件不存在、dfilter
// 语法错误、帧号越界）。传输失败（拨号/读写/超时）走普通 error，两者可用
// errors.As 区分：调用方通常把 *Error 映射为输入类错误（4xx），其余映射为
// 服务不可用（5xx）。
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("sharkd 错误 code=%d: %s", e.Code, e.Message)
}

// Client 一条 sharkd 会话的串行 JSON-RPC 客户端（懒拨号、断线重拨）。
type Client struct {
	sock string

	mu     sync.Mutex
	conn   net.Conn
	sc     *bufio.Scanner
	nextID int
}

// NewClient 创建客户端（懒拨号：首次 Call 才建连接）。
func NewClient(sock string) *Client {
	return &Client{sock: sock}
}

// Call 发起一条 JSON-RPC 请求并返回 envelope 的 result（裸 JSON，未解析）。
// 请求串行执行；ctx 的 deadline 早于 DefaultTimeout 时以 ctx 为准。ctx 取消
// 即关闭当前连接并返回 ctx 的错误，下次调用自动重拨（会话无状态，需重新 load）。
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	conn, err := c.ensureConn(ctx)
	if err != nil {
		return nil, err
	}
	// 取消时关闭本次连接的句柄（net.Conn.Close 可与读写并发），使阻塞的
	// 读立即返回；调用结束 stop() 防止迟到的取消伤及下一次请求。
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	deadline := time.Now().Add(DefaultTimeout)
	fromCtx := false
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline, fromCtx = dl, true // 截止时刻由 ctx 决定：超时即视为 ctx 到期
	}

	c.nextID++
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params,
	})
	if err != nil {
		return nil, fmt.Errorf("序列化 sharkd 请求失败: %w", err)
	}
	_ = c.conn.SetWriteDeadline(deadline)
	if _, werr := c.conn.Write(append(req, '\n')); werr != nil {
		c.drop()
		return nil, fmt.Errorf("写 sharkd 失败: %w", werr)
	}
	res, rerr := c.readResp(deadline)
	if rerr != nil {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		// 读写在 ctx 截止时刻超时：ctx 的取消通知可能尚未送达，这里直接归一为
		// DeadlineExceeded，调用方不必区分"传输超时"与"调用方超时"
		if fromCtx {
			var ne net.Error
			if errors.As(rerr, &ne) && ne.Timeout() {
				return nil, context.DeadlineExceeded
			}
		}
	}
	return res, rerr
}

// Close 关闭连接（幂等；nil 接收者安全，下次 Call 自动重拨）。
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	c.sc = nil
	return err
}

// Probe 拨号探测 socket 是否可连，连上即关（不建会话、不 load 文件）。
func Probe(ctx context.Context, sock string) bool {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "unix", sock)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ensureConn 懒拨号/重拨（掉线后 conn 置空，下次 Call 重建会话）。
func (c *Client) ensureConn(ctx context.Context) (net.Conn, error) {
	if c.conn != nil {
		return c.conn, nil
	}
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "unix", c.sock)
	if err != nil {
		return nil, fmt.Errorf("拨号 sharkd 失败: %w", err)
	}
	c.conn = conn
	c.sc = bufio.NewScanner(conn)
	c.sc.Buffer(make([]byte, readBuf), maxMsg)
	return conn, nil
}

// drop 关闭并复位连接（下次 Call 重拨；新会话无 load 状态，调用方须重新 load）。
func (c *Client) drop() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
		c.sc = nil
	}
}

// readResp 行协议累积成完整 JSON envelope（持锁调用）：前导噪声行跳过，
// 逐行累积至 json.Valid；envelope error → *Error（连接仍健康，不重拨）；
// 超时/EOF → 重拨。
func (c *Client) readResp(deadline time.Time) (json.RawMessage, error) {
	_ = c.conn.SetReadDeadline(deadline)
	var acc []byte
	for c.sc.Scan() {
		line := bytes.TrimSpace(c.sc.Bytes())
		if len(acc) == 0 && (len(line) == 0 || (line[0] != '{' && line[0] != '[')) {
			continue // 会话问候等噪声行跳过
		}
		acc = append(acc, line...)
		if !json.Valid(acc) {
			continue // 多行响应未完，继续累积
		}
		var env struct {
			Result json.RawMessage `json:"result"`
			Error  *Error          `json:"error"`
		}
		if err := json.Unmarshal(acc, &env); err != nil {
			c.drop()
			return nil, fmt.Errorf("解析 sharkd 响应失败: %w", err)
		}
		if env.Error != nil {
			return nil, env.Error
		}
		return env.Result, nil
	}
	if err := c.sc.Err(); err != nil {
		c.drop()
		return nil, fmt.Errorf("读 sharkd 响应失败: %w", err)
	}
	c.drop() // EOF：会话已断
	return nil, fmt.Errorf("sharkd 连接已断")
}
