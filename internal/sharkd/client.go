// Package sharkd 提供与 sharkd 守护进程之间的 JSON-RPC over Unix socket 客户端。
package sharkd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

const (
	// dialTimeout 拨号超时。
	dialTimeout = 5 * time.Second
	// callTimeout 单请求超时（load 8MiB 分片解析 ~0.2s 量级，取宽裕值）。
	callTimeout = 10 * time.Second
	// readBuf bufio.Scanner 初始缓冲。
	readBuf = 64 << 10
	// maxMsg 单条响应上限（8MiB 分片装配出的 envelope）。
	maxMsg = 32 << 20
)

// Error 是 sharkd envelope error（与传输错误区分：如 load 文件不存在、
// dfilter 语法错误属 envelope 错误，由调用方映射 4xx；传输故障重拨自愈）。
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("sharkd 错误 code=%d: %s", e.Code, e.Message)
}

// Client 是一条 sharkd 会话的串行 JSON-RPC 客户端（懒拨号、断线重拨、互斥串行）。
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

// Call 串行发起一条 JSON-RPC 请求：返回 envelope 的 result（裸 JSON）。
// envelope error 返回 *Error；传输失败丢弃连接，下次调用自动重拨。
func (c *Client) Call(method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.ensureConn(); err != nil {
		return nil, err
	}
	c.nextID++
	req, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params,
	})
	if err != nil {
		return nil, fmt.Errorf("序列化 sharkd 请求失败: %w", err)
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(callTimeout))
	if _, err := c.conn.Write(append(req, '\n')); err != nil {
		c.drop()
		return nil, fmt.Errorf("写 sharkd 失败: %w", err)
	}
	return c.readResp()
}

// Close 显式关停客户端。
func (c *Client) Close() error {
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

// Probe 拨号探测可达性后立即关闭（不建长连接、不 load 文件）。
func Probe(sock string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ensureConn 懒拨号/重拨（掉线后 conn 置空，下次 Call 重建会话）。
func (c *Client) ensureConn() error {
	if c.conn != nil {
		return nil
	}
	conn, err := net.DialTimeout("unix", c.sock, dialTimeout)
	if err != nil {
		return fmt.Errorf("拨号 sharkd 失败: %w", err)
	}
	c.conn = conn
	c.sc = bufio.NewScanner(conn)
	c.sc.Buffer(make([]byte, readBuf), maxMsg)
	return nil
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
func (c *Client) readResp() (json.RawMessage, error) {
	_ = c.conn.SetReadDeadline(time.Now().Add(callTimeout))
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
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(acc, &env); err != nil {
			c.drop()
			return nil, fmt.Errorf("解析 sharkd 响应失败: %w", err)
		}
		if env.Error != nil {
			return nil, &Error{Code: env.Error.Code, Message: env.Error.Message}
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
