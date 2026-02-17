package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"inlinechat/services/realtime-service/internal/chatclient"
)

type Handler struct {
	hub             *Hub
	chatClient      *chatclient.Client
	allowedOrigins  map[string]struct{}
	chatCallTimeout time.Duration
	logger          *zap.Logger
}

type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type sendMessagePayload struct {
	Content      string `json:"content"`
	ClientMsgID  string `json:"client_msg_id"`
	VisitorToken string `json:"visitor_token"`
	SenderID     string `json:"sender_id"`
}

func NewHandler(hub *Hub, chatClient *chatclient.Client, allowedOrigins []string, chatCallTimeout time.Duration, logger *zap.Logger) *Handler {
	originMap := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		originMap[origin] = struct{}{}
	}
	if chatCallTimeout <= 0 {
		chatCallTimeout = 8 * time.Second
	}
	return &Handler{
		hub:             hub,
		chatClient:      chatClient,
		allowedOrigins:  originMap,
		chatCallTimeout: chatCallTimeout,
		logger:          logger,
	}
}

func (h *Handler) Serve(c *gin.Context) {
	conversationID := c.Param("conversation_id")
	if conversationID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "conversation_id is required"})
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			if _, ok := h.allowedOrigins["*"]; ok {
				return true
			}
			origin := r.Header.Get("Origin")
			_, ok := h.allowedOrigins[origin]
			return ok
		},
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Warn("websocket upgrade failed", zap.Error(err))
		return
	}

	client := NewClient(conn)
	h.hub.Register(conversationID, client)

	go client.WriteLoop()
	client.ReadLoop(func(message []byte) error {
		return h.handleMessage(c.Request.Context(), conversationID, message, client)
	}, func() {
		h.hub.Unregister(conversationID, client)
		_ = conn.Close()
	})
}

func (h *Handler) handleMessage(ctx context.Context, conversationID string, raw []byte, client *Client) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("invalid payload")
	}

	switch env.Type {
	case "message.send":
		return h.onSendMessage(ctx, conversationID, env.Payload, client)
	default:
		return fmt.Errorf("unsupported message type")
	}
}

func (h *Handler) onSendMessage(ctx context.Context, conversationID string, raw json.RawMessage, client *Client) error {
	var payload sendMessagePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("invalid message.send payload")
	}
	if strings.TrimSpace(payload.Content) == "" {
		return fmt.Errorf("content is required")
	}
	if strings.TrimSpace(payload.ClientMsgID) == "" {
		return fmt.Errorf("client_msg_id is required")
	}

	conversationIDUint, err := strconv.ParseUint(conversationID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid conversation_id")
	}

	ctx, cancel := context.WithTimeout(ctx, h.chatCallTimeout)
	defer cancel()

	msg, err := h.chatClient.CreateMessage(ctx, conversationIDUint, chatclient.CreateMessageRequest{
		SenderType:   "visitor",
		SenderID:     payload.SenderID,
		Content:      payload.Content,
		ClientMsgID:  payload.ClientMsgID,
		VisitorToken: payload.VisitorToken,
	})
	if err != nil {
		return err
	}

	ack := map[string]any{
		"type": "message.ack",
		"payload": map[string]any{
			"client_msg_id": payload.ClientMsgID,
			"message_id":    msg.ID,
		},
	}
	ackBytes, _ := json.Marshal(ack)
	if !client.TrySend(ackBytes) {
		return fmt.Errorf("client ack queue is full")
	}

	return nil
}
