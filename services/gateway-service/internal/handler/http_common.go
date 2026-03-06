package handler

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"inlinechat/services/gateway-service/internal/middleware"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	authv1 "inlinechat/services/gateway-service/internal/gen/authv1"
	chatv1 "inlinechat/services/gateway-service/internal/gen/chatv1"
)

func (h *HTTPHandler) newCallContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), h.callTimeout)
}

func (h *HTTPHandler) applyLoginRateLimit(c *gin.Context, email string) bool {
	if h.loginLimiter == nil {
		return true
	}
	key := strings.ToLower(strings.TrimSpace(email))
	if key == "" {
		key = "unknown"
	}
	key = fmt.Sprintf("login:%s:%s", sanitizeRateLimitSegment(c.ClientIP()), sanitizeRateLimitSegment(key))
	if h.loginLimiter.Allow(key) {
		return true
	}
	middleware.AbortWithError(c, http.StatusTooManyRequests, "rate_limited", "too many login attempts, please retry later")
	return false
}

func (h *HTTPHandler) applyVisitorRateLimit(c *gin.Context, action string, siteID string, conversationID uint64, visitorToken string) bool {
	if h.visitorLimiter == nil {
		return true
	}
	keys := buildVisitorRateLimitKeys(c.ClientIP(), action, siteID, conversationID, visitorToken)
	for _, key := range keys {
		if h.visitorLimiter.Allow(key) {
			continue
		}
		middleware.AbortWithError(c, http.StatusTooManyRequests, "rate_limited", "too many requests, please retry later")
		return false
	}
	return true
}

func (h *HTTPHandler) applyAgentRateLimit(c *gin.Context, action string, agentID uint64) bool {
	if h.agentLimiter == nil {
		return true
	}
	return applyActorRateLimit(c, h.agentLimiter, "agent", action, agentID)
}

func (h *HTTPHandler) applyAdminRateLimit(c *gin.Context, action string, agentID uint64) bool {
	if h.adminLimiter == nil {
		return true
	}
	return applyActorRateLimit(c, h.adminLimiter, "admin", action, agentID)
}

func applyActorRateLimit(c *gin.Context, limiter limiterAllow, scope string, action string, actorID uint64) bool {
	keys := buildActorRateLimitKeys(scope, c.ClientIP(), action, actorID)
	for _, key := range keys {
		if limiter.Allow(key) {
			continue
		}
		middleware.AbortWithError(c, http.StatusTooManyRequests, "rate_limited", "too many requests, please retry later")
		return false
	}
	return true
}

type limiterAllow interface {
	Allow(key string) bool
}

func buildActorRateLimitKeys(scope string, clientIP string, action string, actorID uint64) []string {
	scopeSeg := sanitizeRateLimitSegment(scope)
	if scopeSeg == "" {
		scopeSeg = "actor"
	}
	actionSeg := sanitizeRateLimitSegment(action)
	if actionSeg == "" {
		actionSeg = "unknown_action"
	}
	ip := sanitizeRateLimitSegment(clientIP)
	if ip == "" {
		ip = "ip_unknown"
	}

	keys := []string{
		fmt.Sprintf("%s:ip:%s", scopeSeg, ip),
		fmt.Sprintf("%s:ip_action:%s:%s", scopeSeg, actionSeg, ip),
	}
	if actorID > 0 {
		keys = append(keys,
			fmt.Sprintf("%s:id:%d", scopeSeg, actorID),
			fmt.Sprintf("%s:id_action:%s:%d", scopeSeg, actionSeg, actorID),
			fmt.Sprintf("%s:id_ip:%d:%s", scopeSeg, actorID, ip),
		)
	}
	return dedupeRateLimitKeys(keys)
}

func buildVisitorRateLimitKeys(clientIP string, action string, siteID string, conversationID uint64, visitorToken string) []string {
	actionSeg := sanitizeRateLimitSegment(action)
	if actionSeg == "" {
		actionSeg = "unknown_action"
	}

	visitor := sanitizeRateLimitSegment(visitorToken)
	site := sanitizeRateLimitSegment(siteID)
	ip := sanitizeRateLimitSegment(clientIP)
	if ip == "" {
		ip = "ip_unknown"
	}

	keys := []string{
		fmt.Sprintf("visitor:ip:%s", ip),
		fmt.Sprintf("visitor:ip_action:%s:%s", actionSeg, ip),
	}

	if site != "" {
		keys = append(keys,
			fmt.Sprintf("visitor:site:%s:%s", site, ip),
			fmt.Sprintf("visitor:site_action:%s:%s:%s", actionSeg, site, ip),
		)
	}

	if visitor != "" {
		keys = append(keys,
			fmt.Sprintf("visitor:token:%s", visitor),
			fmt.Sprintf("visitor:token_action:%s:%s", actionSeg, visitor),
			fmt.Sprintf("visitor:token_ip:%s:%s", visitor, ip),
		)
	}

	if conversationID > 0 {
		keys = append(keys, fmt.Sprintf("visitor:conversation:%d", conversationID))
		keys = append(keys, fmt.Sprintf("visitor:conversation_action:%s:%d", actionSeg, conversationID))
		if visitor != "" {
			keys = append(keys, fmt.Sprintf("visitor:conversation_token:%d:%s", conversationID, visitor))
		}
	}

	return dedupeRateLimitKeys(keys)
}

func dedupeRateLimitKeys(keys []string) []string {
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		normalized := strings.TrimSpace(key)
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

func sanitizeRateLimitSegment(v string) string {
	text := strings.TrimSpace(strings.ToLower(v))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, ":", "_")
	if len(text) > 64 {
		return text[:64]
	}
	return text
}

func (h *HTTPHandler) requireActor(c *gin.Context) (*authv1.MeResponse, error) {
	ctx, cancel := h.newCallContext(c)
	defer cancel()

	return h.clients.Auth.Me(ctx, &authv1.MeRequest{
		Authorization: c.GetHeader("Authorization"),
	})
}

func (h *HTTPHandler) requireAgentActor(c *gin.Context) (*authv1.MeResponse, error) {
	actor, err := h.requireActor(c)
	if err != nil {
		return nil, err
	}
	if actor.GetRole() != "agent" {
		return nil, status.Error(codes.PermissionDenied, "agent role required")
	}
	return actor, nil
}

func (h *HTTPHandler) requireAdminActor(c *gin.Context) (*authv1.MeResponse, error) {
	actor, err := h.requireActor(c)
	if err != nil {
		return nil, err
	}
	role := strings.ToLower(strings.TrimSpace(actor.GetRole()))
	if role != "admin" && role != "super_admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	return actor, nil
}

func (h *HTTPHandler) fetchConversation(c *gin.Context, conversationID uint64) (*chatv1.Conversation, error) {
	ctx, cancel := h.newCallContext(c)
	defer cancel()
	return h.clients.Chat.GetConversation(ctx, &chatv1.GetConversationRequest{Id: conversationID})
}

func (h *HTTPHandler) fetchSiteBySiteID(c *gin.Context, siteID string) (*adminv1.Site, error) {
	ctx, cancel := h.newCallContext(c)
	defer cancel()
	return h.clients.Admin.GetSiteBySiteID(ctx, &adminv1.GetSiteBySiteIDRequest{SiteId: siteID})
}

func (h *HTTPHandler) requireActiveConversationSite(c *gin.Context, conversation *chatv1.Conversation) error {
	siteID := strings.TrimSpace(conversation.GetSiteId())
	if siteID == "" {
		abortConflict(c, "site is unavailable")
		return status.Error(codes.FailedPrecondition, "site is unavailable")
	}

	site, err := h.fetchSiteBySiteID(c, siteID)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			abortConflict(c, "site is unavailable")
			return status.Error(codes.FailedPrecondition, "site is unavailable")
		}
		handleGRPCError(c, err)
		return err
	}
	if strings.TrimSpace(strings.ToLower(site.GetStatus())) != "active" {
		abortConflict(c, "site is not active")
		return status.Error(codes.FailedPrecondition, "site is not active")
	}
	return nil
}

func (h *HTTPHandler) requireConversationForVisitor(c *gin.Context, conversationID uint64, visitorToken string) (*chatv1.Conversation, error) {
	conversation, err := h.fetchConversation(c, conversationID)
	if err != nil {
		handleGRPCError(c, err)
		return nil, err
	}
	if conversation.GetVisitorToken() != visitorToken {
		abortForbidden(c, "forbidden")
		return nil, status.Error(codes.PermissionDenied, "forbidden")
	}
	if err := h.requireActiveConversationSite(c, conversation); err != nil {
		return nil, err
	}
	return conversation, nil
}

func (h *HTTPHandler) requireConversationForAgent(c *gin.Context, conversationID uint64, agentID uint64) (*chatv1.Conversation, error) {
	conversation, err := h.fetchConversation(c, conversationID)
	if err != nil {
		handleGRPCError(c, err)
		return nil, err
	}
	if assignedAgentID := conversation.GetAssignedAgentId(); assignedAgentID != 0 && assignedAgentID != agentID {
		if conversation.GetPendingTransferToAgentId() != agentID {
			abortForbidden(c, "forbidden")
			return nil, status.Error(codes.PermissionDenied, "forbidden")
		}
	}
	if err := h.requireActiveConversationSite(c, conversation); err != nil {
		return nil, err
	}
	return conversation, nil
}

func handleGRPCError(c *gin.Context, err error) {
	st, ok := status.FromError(err)
	if !ok {
		middleware.AbortWithError(c, http.StatusBadGateway, "upstream_unavailable", "upstream service unavailable")
		return
	}

	switch st.Code() {
	case codes.InvalidArgument:
		abortBadRequest(c, st.Message())
	case codes.NotFound:
		middleware.AbortWithError(c, http.StatusNotFound, "not_found", st.Message())
	case codes.AlreadyExists:
		middleware.AbortWithError(c, http.StatusConflict, "already_exists", st.Message())
	case codes.Unauthenticated:
		middleware.AbortWithError(c, http.StatusUnauthorized, "unauthorized", st.Message())
	case codes.PermissionDenied:
		middleware.AbortWithError(c, http.StatusForbidden, "forbidden", st.Message())
	case codes.DeadlineExceeded:
		middleware.AbortWithError(c, http.StatusGatewayTimeout, "upstream_timeout", "upstream timeout")
	case codes.Unavailable:
		middleware.AbortWithError(c, http.StatusBadGateway, "upstream_unavailable", "upstream service unavailable")
	case codes.FailedPrecondition:
		middleware.AbortWithError(c, http.StatusConflict, "failed_precondition", st.Message())
	default:
		msg := strings.TrimSpace(st.Message())
		if msg == "" || msg == "internal error" {
			msg = "internal error"
		}
		middleware.AbortWithError(c, http.StatusInternalServerError, "internal_error", msg)
	}
}

func abortBadRequest(c *gin.Context, message string) {
	middleware.AbortWithError(c, http.StatusBadRequest, "invalid_argument", message)
}

func abortConflict(c *gin.Context, message string) {
	middleware.AbortWithError(c, http.StatusConflict, "conflict", message)
}

func abortForbidden(c *gin.Context, message string) {
	middleware.AbortWithError(c, http.StatusForbidden, "forbidden", message)
}

func conversationToJSON(item *chatv1.Conversation) gin.H {
	payload := gin.H{
		"id":         item.GetId(),
		"site_id":    item.GetSiteId(),
		"status":     item.GetStatus(),
		"created_at": item.GetCreatedAt(),
		"updated_at": item.GetUpdatedAt(),
	}
	if item.GetAssignedAgentId() > 0 {
		payload["assigned_agent_id"] = item.GetAssignedAgentId()
	}
	if item.GetClosedAt() != "" {
		payload["closed_at"] = item.GetClosedAt()
	}
	if item.GetClosedByAgentId() > 0 {
		payload["closed_by_agent_id"] = item.GetClosedByAgentId()
	}
	if item.GetPendingTransferToAgentId() > 0 {
		payload["pending_transfer_to_agent_id"] = item.GetPendingTransferToAgentId()
	}
	if item.GetPendingTransferFromAgentId() > 0 {
		payload["pending_transfer_from_agent_id"] = item.GetPendingTransferFromAgentId()
	}
	if item.GetPendingTransferRequestedAt() != "" {
		payload["pending_transfer_requested_at"] = item.GetPendingTransferRequestedAt()
	}
	return payload
}

func conversationToPublicJSON(item *chatv1.Conversation) gin.H {
	return gin.H{
		"id":         item.GetId(),
		"status":     item.GetStatus(),
		"created_at": item.GetCreatedAt(),
		"updated_at": item.GetUpdatedAt(),
	}
}

func messageToJSON(item *chatv1.Message) gin.H {
	payload := gin.H{
		"id":              item.GetId(),
		"conversation_id": item.GetConversationId(),
		"sender_type":     item.GetSenderType(),
		"content":         item.GetContent(),
		"client_msg_id":   item.GetClientMsgId(),
		"status":          item.GetStatus(),
		"created_at":      item.GetCreatedAt(),
		"updated_at":      item.GetUpdatedAt(),
	}
	if item.GetSenderId() != "" {
		payload["sender_id"] = item.GetSenderId()
	}
	return payload
}

func authResultToJSON(item *authv1.AuthResult) gin.H {
	agent := item.GetAgent()
	return gin.H{
		"token": item.GetToken(),
		"agent": gin.H{
			"id":           agent.GetId(),
			"email":        agent.GetEmail(),
			"display_name": agent.GetDisplayName(),
			"role":         agent.GetRole(),
			"status":       agent.GetStatus(),
			"created_at":   agent.GetCreatedAt(),
			"updated_at":   agent.GetUpdatedAt(),
		},
	}
}

func siteToJSON(item *adminv1.Site) gin.H {
	return gin.H{
		"id":         item.GetId(),
		"site_id":    item.GetSiteId(),
		"name":       item.GetName(),
		"domain":     item.GetDomain(),
		"widget_key": item.GetWidgetKey(),
		"status":     item.GetStatus(),
		"created_at": item.GetCreatedAt(),
		"updated_at": item.GetUpdatedAt(),
	}
}

func adminAgentToJSON(item *adminv1.Agent) gin.H {
	return gin.H{
		"id":           item.GetId(),
		"email":        item.GetEmail(),
		"display_name": item.GetDisplayName(),
		"role":         item.GetRole(),
		"status":       item.GetStatus(),
		"created_at":   item.GetCreatedAt(),
		"updated_at":   item.GetUpdatedAt(),
	}
}

func auditLogToJSON(item *adminv1.AuditLog) gin.H {
	return gin.H{
		"id":             item.GetId(),
		"actor_agent_id": item.GetActorAgentId(),
		"actor_email":    item.GetActorEmail(),
		"actor_role":     item.GetActorRole(),
		"action":         item.GetAction(),
		"resource_type":  item.GetResourceType(),
		"resource_id":    item.GetResourceId(),
		"summary":        item.GetSummary(),
		"ip":             item.GetIp(),
		"user_agent":     item.GetUserAgent(),
		"created_at":     item.GetCreatedAt(),
	}
}
