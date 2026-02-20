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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"inlinechat/services/realtime-service/internal/chatclient"
	"inlinechat/services/realtime-service/internal/security"
)

type Handler struct {
	hub             *Hub
	chatClient      messageClient
	jwtSecret       []byte
	jwtIssuer       string
	allowedOrigins  map[string]struct{}
	chatCallTimeout time.Duration
	logger          *zap.Logger
}

type envelope struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type sendMessagePayload struct {
	SenderType   string `json:"sender_type"`
	Content      string `json:"content"`
	ClientMsgID  string `json:"client_msg_id"`
	VisitorToken string `json:"visitor_token"`
	SenderID     string `json:"sender_id"`
}

type connectionContext struct {
	Role         string
	AgentID      uint64
	VisitorToken string
}

type messageClient interface {
	CreateMessage(ctx context.Context, conversationID uint64, reqBody chatclient.CreateMessageRequest) (*chatclient.Message, error)
	GetConversation(ctx context.Context, conversationID uint64) (*chatclient.Conversation, error)
}

func NewHandler(
	hub *Hub,
	chatClient messageClient,
	allowedOrigins []string,
	chatCallTimeout time.Duration,
	jwtSecret string,
	jwtIssuer string,
	logger *zap.Logger,
) *Handler {
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
		jwtSecret:       []byte(jwtSecret),
		jwtIssuer:       jwtIssuer,
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
	conversationIDUint, err := strconv.ParseUint(conversationID, 10, 64)
	if err != nil || conversationIDUint == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation_id"})
		return
	}

	connCtx, code, err := h.resolveConnectionContext(c, conversationIDUint)
	if err != nil {
		c.JSON(code, gin.H{"error": err.Error()})
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
	h.hub.Register(conversationID, client, ClientMeta{Role: connCtx.Role})

	go client.WriteLoop()
	client.ReadLoop(func(message []byte) error {
		return h.handleMessage(c.Request.Context(), conversationID, message, client, connCtx)
	}, func() {
		h.hub.Unregister(conversationID, client)
		_ = conn.Close()
	})
}

func (h *Handler) resolveConnectionContext(c *gin.Context, conversationID uint64) (connectionContext, int, error) {
	accessToken := strings.TrimSpace(c.Query("access_token"))
	if accessToken != "" {
		claims, err := security.ParseToken(h.jwtSecret, h.jwtIssuer, accessToken)
		if err != nil {
			h.logger.Warn("invalid ws access_token", zap.Error(err))
			return connectionContext{}, http.StatusUnauthorized, fmt.Errorf("invalid access_token")
		}
		if claims.Role != "agent" {
			return connectionContext{}, http.StatusForbidden, fmt.Errorf("agent role required")
		}
		if claims.AgentID == 0 {
			return connectionContext{}, http.StatusUnauthorized, fmt.Errorf("invalid access_token")
		}

		return connectionContext{
			Role:    "agent",
			AgentID: claims.AgentID,
		}, 0, nil
	}

	visitorToken := strings.TrimSpace(c.Query("visitor_token"))
	if visitorToken == "" {
		return connectionContext{}, http.StatusUnauthorized, fmt.Errorf("visitor_token is required")
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), h.chatCallTimeout)
	defer cancel()

	conversation, err := h.chatClient.GetConversation(ctx, conversationID)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				return connectionContext{}, http.StatusNotFound, fmt.Errorf("conversation not found")
			case codes.DeadlineExceeded:
				return connectionContext{}, http.StatusGatewayTimeout, fmt.Errorf("upstream timeout")
			default:
				return connectionContext{}, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
			}
		}
		return connectionContext{}, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
	}
	if strings.TrimSpace(conversation.VisitorToken) == "" || conversation.VisitorToken != visitorToken {
		return connectionContext{}, http.StatusForbidden, fmt.Errorf("invalid visitor_token")
	}
	return connectionContext{
		Role:         "visitor",
		VisitorToken: visitorToken,
	}, 0, nil
}

func (h *Handler) handleMessage(ctx context.Context, conversationID string, raw []byte, client *Client, connCtx connectionContext) error {
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("invalid payload")
	}

	switch env.Type {
	case "message.send":
		return h.onSendMessage(ctx, conversationID, env.Payload, client, connCtx)
	default:
		return fmt.Errorf("unsupported message type")
	}
}

func (h *Handler) onSendMessage(ctx context.Context, conversationID string, raw json.RawMessage, client *Client, connCtx connectionContext) error {
	var payload sendMessagePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("invalid message.send payload")
	}

	payload.ClientMsgID = strings.TrimSpace(payload.ClientMsgID)
	if payload.ClientMsgID == "" {
		return fmt.Errorf("client_msg_id is required")
	}
	if strings.TrimSpace(payload.Content) == "" {
		h.sendNack(client, payload.ClientMsgID, "content is required")
		return nil
	}

	conversationIDUint, err := strconv.ParseUint(conversationID, 10, 64)
	if err != nil {
		h.sendNack(client, payload.ClientMsgID, "invalid conversation_id")
		return nil
	}

	senderType := strings.ToLower(strings.TrimSpace(payload.SenderType))
	if senderType == "" {
		senderType = "visitor"
	}
	senderID := strings.TrimSpace(payload.SenderID)
	switch senderType {
	case "visitor":
		if connCtx.Role != "visitor" {
			h.sendNack(client, payload.ClientMsgID, "agent connection cannot send visitor message")
			return nil
		}
		payload.VisitorToken = strings.TrimSpace(payload.VisitorToken)
		if payload.VisitorToken == "" {
			payload.VisitorToken = connCtx.VisitorToken
		}
		if payload.VisitorToken != connCtx.VisitorToken {
			h.sendNack(client, payload.ClientMsgID, "invalid visitor_token")
			return nil
		}
	case "agent":
		if connCtx.Role != "agent" {
			h.sendNack(client, payload.ClientMsgID, "agent access_token is required")
			return nil
		}
		senderID = strconv.FormatUint(connCtx.AgentID, 10)
	default:
		h.sendNack(client, payload.ClientMsgID, "invalid sender_type")
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, h.chatCallTimeout)
	defer cancel()

	msg, err := h.chatClient.CreateMessage(ctx, conversationIDUint, chatclient.CreateMessageRequest{
		SenderType:   senderType,
		SenderID:     senderID,
		Content:      payload.Content,
		ClientMsgID:  payload.ClientMsgID,
		VisitorToken: payload.VisitorToken,
	})
	if err != nil {
		h.sendNack(client, payload.ClientMsgID, err.Error())
		return nil
	}

	ack := map[string]any{
		"type": "message.ack",
		"payload": map[string]any{
			"client_msg_id": payload.ClientMsgID,
			"message_id":    msg.ID,
			"status":        msg.Status,
		},
	}
	ackBytes, _ := json.Marshal(ack)
	if !client.TrySend(ackBytes) {
		return fmt.Errorf("client ack queue is full")
	}

	return nil
}

func (h *Handler) sendNack(client *Client, clientMsgID string, reason string) {
	nack := map[string]any{
		"type": "message.nack",
		"payload": map[string]any{
			"client_msg_id": clientMsgID,
			"error":         reason,
		},
	}
	nackBytes, _ := json.Marshal(nack)
	if !client.TrySend(nackBytes) {
		h.logger.Warn("client nack queue is full", zap.String("client_msg_id", clientMsgID))
	}
}
