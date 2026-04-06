package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"inlinechat/services/realtime-service/internal/adminclient"
	"inlinechat/services/realtime-service/internal/authclient"
	"inlinechat/services/realtime-service/internal/chatclient"
	"inlinechat/services/realtime-service/internal/security"
)

const (
	maxMessageContentChars = 2000
	replayPageSize         = 100
)

var maxReplayMessages = 500

// Handler 负责 WS 握手鉴权、消息收发和断线补拉。
type Handler struct {
	hub             *Hub
	chatClient      messageClient
	authClient      authMeClient
	siteClient      siteLookupClient
	jwtSecrets      [][]byte
	jwtIssuer       string
	allowAllOrigins bool
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
	SiteID       string
	SiteDomains  []string
	VisitorToken string
}

type messageClient interface {
	CreateMessage(ctx context.Context, conversationID uint64, reqBody chatclient.CreateMessageRequest) (*chatclient.Message, error)
	GetConversation(ctx context.Context, conversationID uint64) (*chatclient.Conversation, error)
	ListMessages(ctx context.Context, conversationID uint64, in chatclient.ListMessagesInput) ([]*chatclient.Message, error)
}

type authMeClient interface {
	Me(ctx context.Context, authorization string) (*authclient.MeResult, error)
}

type siteLookupClient interface {
	GetSiteBySiteID(ctx context.Context, siteID string) (*adminclient.Site, error)
}

func NewHandler(
	hub *Hub,
	chatClient messageClient,
	authClient authMeClient,
	siteClient siteLookupClient,
	allowedOrigins []string,
	chatCallTimeout time.Duration,
	jwtSecret string,
	jwtPreviousSecret string,
	jwtIssuer string,
	logger *zap.Logger,
) *Handler {
	originMap := make(map[string]struct{}, len(allowedOrigins))
	allowAllOrigins := false
	for _, origin := range allowedOrigins {
		trimmed := strings.TrimSpace(origin)
		if trimmed == "*" {
			allowAllOrigins = true
			continue
		}
		if normalized := normalizeOrigin(trimmed); normalized != "" {
			originMap[normalized] = struct{}{}
		}
	}
	if chatCallTimeout <= 0 {
		chatCallTimeout = 8 * time.Second
	}
	return &Handler{
		hub:             hub,
		chatClient:      chatClient,
		authClient:      authClient,
		siteClient:      siteClient,
		jwtSecrets:      buildJWTSecrets(jwtSecret, jwtPreviousSecret),
		jwtIssuer:       jwtIssuer,
		allowAllOrigins: allowAllOrigins,
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
	// last_message_id 用于断线补拉，缺省为 0（不回放）。
	lastMessageIDRaw := strings.TrimSpace(c.Query("last_message_id"))
	var lastMessageID uint64
	if lastMessageIDRaw != "" {
		parsed, parseErr := strconv.ParseUint(lastMessageIDRaw, 10, 64)
		if parseErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid last_message_id"})
			return
		}
		lastMessageID = parsed
	}

	connCtx, code, err := h.resolveConnectionContext(c, conversationIDUint)
	if err != nil {
		c.JSON(code, gin.H{"error": err.Error()})
		return
	}
	if !h.isOriginAllowedForConnection(c.GetHeader("Origin"), connCtx) {
		c.JSON(http.StatusForbidden, gin.H{"error": "origin is not allowed"})
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(_ *http.Request) bool { return true },
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Warn("websocket upgrade failed", zap.Error(err))
		return
	}

	client := NewClient(conn)
	h.hub.Register(conversationID, client)

	go client.WriteLoop()
	// 升级成功后先做历史补拉，再开始实时读循环，避免消息窗口丢失。
	if err := h.replayMessages(c.Request.Context(), conversationIDUint, lastMessageID, client); err != nil {
		h.logger.Warn("replay websocket messages failed",
			zap.Error(err),
			zap.Uint64("conversation_id", conversationIDUint),
			zap.Uint64("last_message_id", lastMessageID),
		)
		h.hub.Unregister(conversationID, client)
		client.Close()
		_ = conn.Close()
		return
	}

	client.ReadLoop(func(message []byte) error {
		return h.handleMessage(c.Request.Context(), conversationID, message, client, connCtx)
	}, func() {
		h.hub.Unregister(conversationID, client)
		_ = conn.Close()
	})
}

func (h *Handler) resolveConnectionContext(c *gin.Context, conversationID uint64) (connectionContext, int, error) {
	accessToken := strings.TrimSpace(c.Query("access_token"))
	connCtx := connectionContext{}
	if accessToken != "" {
		// 客服链路：JWT 本地验签 + auth-service Me 二次校验。
		claims, err := security.ParseTokenAny(h.jwtSecrets, h.jwtIssuer, accessToken)
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
		if h.authClient == nil {
			return connectionContext{}, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
		}

		ctx, cancel := context.WithTimeout(c.Request.Context(), h.chatCallTimeout)
		defer cancel()

		me, err := h.authClient.Me(ctx, "Bearer "+accessToken)
		if err != nil {
			if st, ok := status.FromError(err); ok {
				switch st.Code() {
				case codes.Unauthenticated:
					return connectionContext{}, http.StatusUnauthorized, fmt.Errorf("invalid access_token")
				case codes.PermissionDenied:
					return connectionContext{}, http.StatusForbidden, fmt.Errorf("agent role required")
				case codes.DeadlineExceeded:
					return connectionContext{}, http.StatusGatewayTimeout, fmt.Errorf("upstream timeout")
				default:
					return connectionContext{}, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
				}
			}
			return connectionContext{}, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
		}
		if strings.TrimSpace(me.Role) != "agent" {
			return connectionContext{}, http.StatusForbidden, fmt.Errorf("agent role required")
		}
		if me.AgentID == 0 || me.AgentID != claims.AgentID {
			return connectionContext{}, http.StatusUnauthorized, fmt.Errorf("invalid access_token")
		}

		connCtx = connectionContext{
			Role:    "agent",
			AgentID: me.AgentID,
			SiteID:  strings.TrimSpace(me.SiteID),
		}
	} else {
		visitorToken := strings.TrimSpace(c.Query("visitor_token"))
		if visitorToken == "" {
			return connectionContext{}, http.StatusUnauthorized, fmt.Errorf("visitor_token is required")
		}
		// 访客链路：要求 token 与会话绑定 token 一致。
		connCtx = connectionContext{
			Role:         "visitor",
			VisitorToken: visitorToken,
		}
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
	if connCtx.Role == "visitor" && (strings.TrimSpace(conversation.VisitorToken) == "" || conversation.VisitorToken != connCtx.VisitorToken) {
		return connectionContext{}, http.StatusForbidden, fmt.Errorf("invalid visitor_token")
	}

	siteID := strings.TrimSpace(conversation.SiteID)
	if siteID == "" {
		return connectionContext{}, http.StatusConflict, fmt.Errorf("site is unavailable")
	}
	site, code, err := h.validateConversationSite(ctx, siteID)
	if err != nil {
		return connectionContext{}, code, err
	}
	if connCtx.Role == "agent" {
		if connCtx.SiteID == "" {
			return connectionContext{}, http.StatusForbidden, fmt.Errorf("agent site is unavailable")
		}
		if connCtx.SiteID != siteID {
			return connectionContext{}, http.StatusForbidden, fmt.Errorf("forbidden")
		}
	}

	connCtx.SiteID = siteID
	connCtx.SiteDomains = allowedSiteDomains(site)
	return connCtx, 0, nil
}

func normalizeOrigin(raw string) string {
	trimmed := strings.TrimSpace(strings.ToLower(raw))
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + strings.ToLower(parsed.Host)
}

func (h *Handler) isOriginAllowed(origin string) bool {
	if h.allowAllOrigins {
		return true
	}
	if len(h.allowedOrigins) == 0 {
		return false
	}
	normalized := normalizeOrigin(origin)
	if normalized == "" {
		return false
	}
	_, ok := h.allowedOrigins[normalized]
	return ok
}

func (h *Handler) isOriginAllowedForConnection(origin string, connCtx connectionContext) bool {
	if h.allowAllOrigins {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(connCtx.Role), "visitor") {
		if len(connCtx.SiteDomains) > 0 {
			return matchesAnySiteDomain(connCtx.SiteDomains, origin)
		}
	}
	return h.isOriginAllowed(origin)
}

func buildJWTSecrets(primary string, previous string) [][]byte {
	out := make([][]byte, 0, 2)
	primaryText := strings.TrimSpace(primary)
	if primaryText != "" {
		out = append(out, []byte(primaryText))
	}
	previousText := strings.TrimSpace(previous)
	if previousText != "" && previousText != primaryText {
		out = append(out, []byte(previousText))
	}
	return out
}

func (h *Handler) replayMessages(ctx context.Context, conversationID uint64, lastMessageID uint64, client *Client) error {
	if h == nil || h.chatClient == nil || client == nil || lastMessageID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	beforeID := uint64(0)
	missed := make([]*chatclient.Message, 0, replayPageSize)
	truncated := false
	// 分页反查直到命中 last_message_id，或达到回放上限。
	for len(missed) < maxReplayMessages {
		callCtx, cancel := context.WithTimeout(ctx, h.chatCallTimeout)
		items, err := h.chatClient.ListMessages(callCtx, conversationID, chatclient.ListMessagesInput{
			Limit:    replayPageSize,
			BeforeID: beforeID,
		})
		cancel()
		if err != nil {
			return err
		}
		if len(items) == 0 {
			break
		}

		stop := false
		for i := range items {
			item := items[i]
			if item == nil {
				continue
			}
			if item.ID <= lastMessageID {
				stop = true
				break
			}
			missed = append(missed, item)
			if len(missed) >= maxReplayMessages {
				truncated = true
				stop = true
				break
			}
		}
		if stop {
			break
		}

		last := items[len(items)-1]
		if last == nil || last.ID == 0 {
			break
		}
		beforeID = last.ID
	}

	sort.Slice(missed, func(i, j int) bool {
		return missed[i].ID < missed[j].ID
	})

	for i := range missed {
		payload, err := marshalMessageNewEvent(conversationID, missed[i])
		if err != nil {
			return err
		}
		if !client.TrySend(payload) {
			return fmt.Errorf("client replay queue is full")
		}
	}

	lastReplayedID := lastMessageID
	nextBeforeID := uint64(0)
	if len(missed) > 0 {
		lastReplayedID = missed[len(missed)-1].ID
		if truncated {
			nextBeforeID = missed[0].ID
		}
	}
	replayEndPayload, err := marshalReplayEndEvent(conversationID, lastReplayedID, len(missed), truncated, nextBeforeID)
	if err != nil {
		return err
	}
	if !client.TrySend(replayEndPayload) {
		return fmt.Errorf("client replay queue is full")
	}

	return nil
}

func marshalMessageNewEvent(conversationID uint64, msg *chatclient.Message) ([]byte, error) {
	if msg == nil {
		return nil, fmt.Errorf("message is nil")
	}
	eventConversationID := msg.ConversationID
	if eventConversationID == 0 {
		eventConversationID = conversationID
	}
	env := map[string]any{
		"type": "message.new",
		"payload": map[string]any{
			"conversation_id": eventConversationID,
			"message": map[string]any{
				"id":              msg.ID,
				"conversation_id": eventConversationID,
				"sender_type":     msg.SenderType,
				"sender_id":       msg.SenderID,
				"content":         msg.Content,
				"client_msg_id":   msg.ClientMsgID,
				"status":          msg.Status,
				"created_at":      msg.CreatedAt,
				"updated_at":      msg.UpdatedAt,
			},
		},
	}
	return json.Marshal(env)
}

func marshalReplayEndEvent(conversationID uint64, lastMessageID uint64, replayedCount int, truncated bool, nextBeforeID uint64) ([]byte, error) {
	payload := map[string]any{
		"conversation_id": conversationID,
		"last_message_id": lastMessageID,
		"replayed_count":  replayedCount,
		"truncated":       truncated,
		"next_before_id":  nextBeforeID,
	}
	env := map[string]any{
		"type":    "replay.end",
		"payload": payload,
	}
	return json.Marshal(env)
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
	if utf8.RuneCountInString(payload.Content) > maxMessageContentChars {
		h.sendNack(client, payload.ClientMsgID, fmt.Sprintf("content is too long (max %d characters)", maxMessageContentChars))
		return nil
	}

	conversationIDUint, err := strconv.ParseUint(conversationID, 10, 64)
	if err != nil {
		h.sendNack(client, payload.ClientMsgID, "invalid conversation_id")
		return nil
	}
	if connCtx.SiteID == "" {
		h.sendNack(client, payload.ClientMsgID, "site is unavailable")
		return nil
	}
	validateCtx, validateCancel := context.WithTimeout(ctx, h.chatCallTimeout)
	defer validateCancel()
	if _, _, err := h.validateConversationSite(validateCtx, connCtx.SiteID); err != nil {
		h.sendNack(client, payload.ClientMsgID, err.Error())
		return nil
	}

	senderType := strings.ToLower(strings.TrimSpace(payload.SenderType))
	if senderType == "" {
		senderType = "visitor"
	}
	senderID := strings.TrimSpace(payload.SenderID)
	switch senderType {
	case "visitor":
		// 访客连接仅允许 visitor 发送，并要求 visitor_token 与连接上下文一致。
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
		// 客服连接强制使用连接上下文里的 agent_id，避免伪造 sender_id。
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

	// WS 写入成功先返回 ack，真正广播由 chat->redis->realtime 链路异步完成。
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

func (h *Handler) validateConversationSite(ctx context.Context, siteID string) (*adminclient.Site, int, error) {
	if h.siteClient == nil {
		return nil, 0, nil
	}
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return nil, http.StatusConflict, fmt.Errorf("site is unavailable")
	}

	site, err := h.siteClient.GetSiteBySiteID(ctx, siteID)
	if err != nil {
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				return nil, http.StatusConflict, fmt.Errorf("site is unavailable")
			case codes.DeadlineExceeded:
				return nil, http.StatusGatewayTimeout, fmt.Errorf("upstream timeout")
			default:
				return nil, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
			}
		}
		return nil, http.StatusBadGateway, fmt.Errorf("upstream unavailable")
	}
	if strings.TrimSpace(strings.ToLower(site.Status)) != "active" {
		return nil, http.StatusConflict, fmt.Errorf("site is not active")
	}
	return site, 0, nil
}

func allowedSiteDomains(site *adminclient.Site) []string {
	if site == nil {
		return nil
	}
	raw := site.Domains
	if len(raw) == 0 && strings.TrimSpace(site.Domain) != "" {
		raw = []string{site.Domain}
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		normalized := normalizeHostLike(item)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func matchesSiteDomain(siteDomain string, raw string) bool {
	normalizedSiteDomain := normalizeHostLike(siteDomain)
	normalizedHost := normalizeHostLike(raw)
	if normalizedSiteDomain == "" || normalizedHost == "" {
		return false
	}
	if normalizedSiteDomain == normalizedHost {
		return true
	}
	return isLocalhostHost(normalizedSiteDomain) && isLocalhostHost(normalizedHost)
}

func matchesAnySiteDomain(siteDomains []string, raw string) bool {
	for _, item := range siteDomains {
		if matchesSiteDomain(item, raw) {
			return true
		}
	}
	return false
}

func normalizeHostLike(raw string) string {
	parsed, ok := parseURLLike(raw)
	if ok {
		return normalizeHostFromURL(parsed)
	}

	text := strings.TrimSpace(strings.ToLower(raw))
	text = strings.TrimPrefix(text, ".")
	text = strings.TrimSuffix(text, ".")
	if text == "" {
		return ""
	}

	hostURL, ok := parseURLLike("https://" + text)
	if !ok {
		return ""
	}
	return normalizeHostFromURL(hostURL)
}

func parseURLLike(raw string) (*url.URL, bool) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, false
	}
	parsed, err := url.Parse(text)
	if err == nil && parsed.Host != "" {
		return parsed, true
	}
	return nil, false
}

func normalizeHostFromURL(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	if host == "" {
		return ""
	}
	port := strings.TrimSpace(parsed.Port())
	switch {
	case port == "":
		return host
	case strings.EqualFold(parsed.Scheme, "http") && port == "80":
		return host
	case strings.EqualFold(parsed.Scheme, "https") && port == "443":
		return host
	default:
		return host + ":" + port
	}
}

func isLocalhostHost(raw string) bool {
	hostURL, ok := parseURLLike("https://" + strings.TrimSpace(raw))
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(hostURL.Hostname()), "localhost")
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
