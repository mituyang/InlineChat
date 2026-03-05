package ws

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait = 10 * time.Second
	pongWait  = 60 * time.Second
	pingWait  = 25 * time.Second
)

type Client struct {
	conn *websocket.Conn
	send chan []byte
	once sync.Once
}

func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn: conn,
		send: make(chan []byte, 64),
	}
}

func (c *Client) ReadLoop(onMessage func([]byte) error, onClose func()) {
	defer onClose()

	// 读超时由 pong 续期；若对端长期无响应则自动断开。
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		if err := onMessage(message); err != nil {
			errPayload, _ := json.Marshal(map[string]any{
				"type":  "error",
				"error": err.Error(),
			})
			_ = c.TrySend(errPayload)
		}
	}
}

func (c *Client) WriteLoop() {
	ticker := time.NewTicker(pingWait)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			// 单独的发送队列把业务处理与网络写入解耦，避免互相阻塞。
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

func (c *Client) Close() {
	c.once.Do(func() {
		close(c.send)
	})
}

func (c *Client) TrySend(payload []byte) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()

	// 非阻塞写入：队列满直接失败，由上层决定丢弃或关闭连接。
	select {
	case c.send <- payload:
		return true
	default:
		return false
	}
}
