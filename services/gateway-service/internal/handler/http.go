package handler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"inlinechat/services/gateway-service/internal/grpcclient"
	"inlinechat/services/gateway-service/internal/middleware"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	authv1 "inlinechat/services/gateway-service/internal/gen/authv1"
	chatv1 "inlinechat/services/gateway-service/internal/gen/chatv1"
)

const maxMessageContentChars = 2000

type HTTPHandler struct {
	clients     *grpcclient.Clients
	callTimeout time.Duration
}

func NewHTTPHandler(clients *grpcclient.Clients, callTimeout time.Duration) *HTTPHandler {
	if callTimeout <= 0 {
		callTimeout = 8 * time.Second
	}
	return &HTTPHandler{
		clients:     clients,
		callTimeout: callTimeout,
	}
}

func (h *HTTPHandler) RegisterRoutes(r *gin.Engine) {
	chatV1 := r.Group("/api/chat/v1")
	chatV1.POST("/conversations", h.createConversation)
	chatV1.GET("/conversations", h.listConversations)
	chatV1.GET("/conversations/:id", h.getConversation)
	chatV1.POST("/conversations/:id/messages", h.createMessage)
	chatV1.GET("/conversations/:id/messages", h.listMessages)
	chatV1.POST("/conversations/:id/read", h.markMessagesRead)
	chatV1.POST("/conversations/:id/claim", h.claimConversation)
	chatV1.POST("/conversations/:id/transfer", h.transferConversation)
	chatV1.POST("/conversations/:id/transfer/confirm", h.confirmTransferConversation)
	chatV1.POST("/conversations/:id/transfer/reject", h.rejectTransferConversation)
	chatV1.POST("/conversations/:id/close", h.closeConversation)

	authV1 := r.Group("/api/auth/v1/auth")
	authV1.POST("/login", h.login)
	authV1.GET("/me", h.me)

	adminV1 := r.Group("/api/admin/v1/admin")
	adminV1.POST("/sites", h.createSite)
	adminV1.GET("/sites", h.listSites)
	adminV1.POST("/agents", h.createAgent)
	adminV1.GET("/agents", h.listAgents)
}

type createConversationRequest struct {
	SiteID       string `json:"site_id" binding:"required"`
	VisitorToken string `json:"visitor_token" binding:"required"`
}

func (h *HTTPHandler) createConversation(c *gin.Context) {
	var req createConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}
	req.SiteID = strings.TrimSpace(req.SiteID)
	req.VisitorToken = strings.TrimSpace(req.VisitorToken)
	if req.SiteID == "" || req.VisitorToken == "" {
		abortBadRequest(c, "site_id and visitor_token are required")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	siteResp, err := h.clients.Admin.GetSiteBySiteID(ctx, &adminv1.GetSiteBySiteIDRequest{
		SiteId: req.SiteID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			abortBadRequest(c, "invalid site_id")
			return
		}
		handleGRPCError(c, err)
		return
	}
	if strings.TrimSpace(strings.ToLower(siteResp.GetStatus())) != "active" {
		abortConflict(c, "site is not active")
		return
	}

	resp, err := h.clients.Chat.CreateConversation(ctx, &chatv1.CreateConversationRequest{
		SiteId:       req.SiteID,
		VisitorToken: req.VisitorToken,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, conversationToPublicJSON(resp))
}

func (h *HTTPHandler) getConversation(c *gin.Context) {
	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	var conversation *chatv1.Conversation
	if strings.TrimSpace(c.GetHeader("Authorization")) != "" {
		actor, actorErr := h.requireAgentActor(c)
		if actorErr != nil {
			handleGRPCError(c, actorErr)
			return
		}
		conversation, err = h.requireConversationForAgent(c, conversationID, actor.GetAgentId())
		if err != nil {
			return
		}
	} else {
		visitorToken := strings.TrimSpace(c.Query("visitor_token"))
		if visitorToken == "" {
			abortBadRequest(c, "visitor_token is required when Authorization is missing")
			return
		}
		conversation, err = h.requireConversationForVisitor(c, conversationID, visitorToken)
		if err != nil {
			return
		}
	}

	c.JSON(http.StatusOK, conversationToJSON(conversation))
}

func (h *HTTPHandler) listConversations(c *gin.Context) {
	actor, err := h.requireAgentActor(c)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	limit := 50
	offset := 0
	if raw := c.Query("limit"); raw != "" {
		v, convErr := strconv.Atoi(raw)
		if convErr != nil || v <= 0 || v > 200 {
			abortBadRequest(c, "invalid limit")
			return
		}
		limit = v
	}
	if raw := c.Query("offset"); raw != "" {
		v, convErr := strconv.Atoi(raw)
		if convErr != nil || v < 0 {
			abortBadRequest(c, "invalid offset")
			return
		}
		offset = v
	}

	statusFilter := strings.TrimSpace(c.Query("status"))
	if statusFilter != "" && statusFilter != "open" && statusFilter != "closed" {
		abortBadRequest(c, "invalid status")
		return
	}

	siteID := strings.TrimSpace(c.Query("site_id"))
	var assignedAgentID uint64
	if raw := strings.TrimSpace(c.Query("assigned_agent_id")); raw != "" {
		v, convErr := strconv.ParseUint(raw, 10, 64)
		if convErr != nil || v == 0 {
			abortBadRequest(c, "invalid assigned_agent_id")
			return
		}
		assignedAgentID = v
	}

	unassignedOnly := false
	if raw := strings.TrimSpace(c.Query("unassigned_only")); raw != "" {
		v, convErr := strconv.ParseBool(raw)
		if convErr != nil {
			abortBadRequest(c, "invalid unassigned_only")
			return
		}
		unassignedOnly = v
	}

	req := &chatv1.ListConversationsRequest{
		Status:         statusFilter,
		SiteId:         siteID,
		UnassignedOnly: unassignedOnly,
		Limit:          int32(limit),
		Offset:         int32(offset),
	}
	if assignedAgentID > 0 {
		req.AssignedAgentId = assignedAgentID
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.ListConversations(ctx, req)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]any, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		conv := conversationToJSON(item)
		// 普通坐席默认只能看到自己负责、未分配，或待自己确认转接的会话（除非明确传过滤条件）。
		if actor.GetRole() == "agent" && assignedAgentID == 0 && !unassignedOnly {
			if id, ok := conv["assigned_agent_id"].(uint64); ok && id != 0 && id != actor.GetAgentId() {
				if pendingTo, hasPending := conv["pending_transfer_to_agent_id"].(uint64); !hasPending || pendingTo != actor.GetAgentId() {
					continue
				}
			}
		}
		items = append(items, conv)
	}

	c.JSON(http.StatusOK, gin.H{"items": items})
}

type createMessageRequest struct {
	SenderType   string `json:"sender_type" binding:"required"`
	SenderID     string `json:"sender_id"`
	Content      string `json:"content" binding:"required"`
	ClientMsgID  string `json:"client_msg_id" binding:"required"`
	VisitorToken string `json:"visitor_token"`
}

func (h *HTTPHandler) createMessage(c *gin.Context) {
	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	var req createMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}

	req.SenderType = strings.ToLower(strings.TrimSpace(req.SenderType))
	req.VisitorToken = strings.TrimSpace(req.VisitorToken)
	if strings.TrimSpace(req.Content) == "" {
		abortBadRequest(c, "content is required")
		return
	}
	if utf8.RuneCountInString(req.Content) > maxMessageContentChars {
		abortBadRequest(c, fmt.Sprintf("content is too long (max %d characters)", maxMessageContentChars))
		return
	}

	switch req.SenderType {
	case "agent":
		actor, actorErr := h.requireAgentActor(c)
		if actorErr != nil {
			handleGRPCError(c, actorErr)
			return
		}
		req.SenderID = strconv.FormatUint(actor.GetAgentId(), 10)
		conversation, convErr := h.requireConversationForAgent(c, conversationID, actor.GetAgentId())
		if convErr != nil {
			return
		}
		if conversation.GetAssignedAgentId() == 0 {
			abortConflict(c, "conversation must be claimed before agent can send message")
			return
		}
	case "visitor":
		if req.VisitorToken == "" {
			abortBadRequest(c, "visitor_token is required for visitor sender_type")
			return
		}
		if _, convErr := h.requireConversationForVisitor(c, conversationID, req.VisitorToken); convErr != nil {
			return
		}
	default:
		abortBadRequest(c, "invalid sender_type")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.CreateMessage(ctx, &chatv1.CreateMessageRequest{
		ConversationId: conversationID,
		SenderType:     req.SenderType,
		SenderId:       req.SenderID,
		Content:        req.Content,
		ClientMsgId:    req.ClientMsgID,
		VisitorToken:   req.VisitorToken,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, messageToJSON(resp))
}

func (h *HTTPHandler) listMessages(c *gin.Context) {
	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	limit := 50
	if raw := c.Query("limit"); raw != "" {
		v, convErr := strconv.Atoi(raw)
		if convErr != nil || v <= 0 || v > 200 {
			abortBadRequest(c, "invalid limit")
			return
		}
		limit = v
	}

	var beforeID uint64
	if raw := c.Query("before_id"); raw != "" {
		v, convErr := strconv.ParseUint(raw, 10, 64)
		if convErr != nil {
			abortBadRequest(c, "invalid before_id")
			return
		}
		beforeID = v
	}

	if strings.TrimSpace(c.GetHeader("Authorization")) != "" {
		actor, actorErr := h.requireAgentActor(c)
		if actorErr != nil {
			handleGRPCError(c, actorErr)
			return
		}
		if _, convErr := h.requireConversationForAgent(c, conversationID, actor.GetAgentId()); convErr != nil {
			return
		}
	} else {
		visitorToken := strings.TrimSpace(c.Query("visitor_token"))
		if visitorToken == "" {
			abortBadRequest(c, "visitor_token is required when Authorization is missing")
			return
		}
		if _, convErr := h.requireConversationForVisitor(c, conversationID, visitorToken); convErr != nil {
			return
		}
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.ListMessages(ctx, &chatv1.ListMessagesRequest{
		ConversationId: conversationID,
		Limit:          int32(limit),
		BeforeId:       beforeID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]any, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		items = append(items, messageToJSON(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type markMessagesReadRequest struct {
	LastReadMessageID uint64 `json:"last_read_message_id" binding:"required"`
	VisitorToken      string `json:"visitor_token"`
}

func (h *HTTPHandler) markMessagesRead(c *gin.Context) {
	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	var req markMessagesReadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}
	if req.LastReadMessageID == 0 {
		abortBadRequest(c, "last_read_message_id is required")
		return
	}

	actorType := "visitor"
	var actorAgentID uint64
	visitorToken := strings.TrimSpace(req.VisitorToken)
	if strings.TrimSpace(c.GetHeader("Authorization")) != "" {
		actor, actorErr := h.requireAgentActor(c)
		if actorErr != nil {
			handleGRPCError(c, actorErr)
			return
		}
		actorType = "agent"
		actorAgentID = actor.GetAgentId()
		conversation, convErr := h.requireConversationForAgent(c, conversationID, actor.GetAgentId())
		if convErr != nil {
			return
		}
		if conversation.GetAssignedAgentId() != actor.GetAgentId() {
			abortForbidden(c, "conversation must be assigned before mark read")
			return
		}
	} else if visitorToken == "" {
		abortBadRequest(c, "visitor_token is required when Authorization is missing")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.MarkMessagesRead(ctx, &chatv1.MarkMessagesReadRequest{
		ConversationId:    conversationID,
		LastReadMessageId: req.LastReadMessageID,
		ActorType:         actorType,
		ActorAgentId:      actorAgentID,
		VisitorToken:      visitorToken,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"updated_count": resp.GetUpdatedCount()})
}

func (h *HTTPHandler) claimConversation(c *gin.Context) {
	actor, err := h.requireAgentActor(c)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || conversationID == 0 {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.ClaimConversation(ctx, &chatv1.ClaimConversationRequest{
		ConversationId: conversationID,
		AgentId:        actor.GetAgentId(),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	h.tryMarkConversationReadAfterClaim(c, conversationID, actor.GetAgentId())

	c.JSON(http.StatusOK, conversationToJSON(resp))
}

func (h *HTTPHandler) tryMarkConversationReadAfterClaim(c *gin.Context, conversationID uint64, agentID uint64) {
	ctx, cancel := h.newCallContext(c)
	defer cancel()

	listResp, err := h.clients.Chat.ListMessages(ctx, &chatv1.ListMessagesRequest{
		ConversationId: conversationID,
		Limit:          1,
	})
	if err != nil {
		return
	}
	items := listResp.GetItems()
	if len(items) == 0 {
		return
	}
	lastReadMessageID := items[0].GetId()
	if lastReadMessageID == 0 {
		return
	}

	_, _ = h.clients.Chat.MarkMessagesRead(ctx, &chatv1.MarkMessagesReadRequest{
		ConversationId:    conversationID,
		LastReadMessageId: lastReadMessageID,
		ActorType:         "agent",
		ActorAgentId:      agentID,
	})
}

type transferConversationRequest struct {
	ToAgentID uint64 `json:"to_agent_id" binding:"required"`
}

func (h *HTTPHandler) transferConversation(c *gin.Context) {
	actor, err := h.requireAgentActor(c)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || conversationID == 0 {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	var req transferConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ToAgentID == 0 {
		abortBadRequest(c, "to_agent_id is required")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.TransferConversation(ctx, &chatv1.TransferConversationRequest{
		ConversationId: conversationID,
		ActorAgentId:   actor.GetAgentId(),
		ActorRole:      actor.GetRole(),
		ToAgentId:      req.ToAgentID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, conversationToJSON(resp))
}

func (h *HTTPHandler) confirmTransferConversation(c *gin.Context) {
	actor, err := h.requireAgentActor(c)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || conversationID == 0 {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.ConfirmTransferConversation(ctx, &chatv1.ConfirmTransferConversationRequest{
		ConversationId: conversationID,
		ActorAgentId:   actor.GetAgentId(),
		ActorRole:      actor.GetRole(),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	h.tryMarkConversationReadAfterClaim(c, conversationID, actor.GetAgentId())

	c.JSON(http.StatusOK, conversationToJSON(resp))
}

func (h *HTTPHandler) rejectTransferConversation(c *gin.Context) {
	actor, err := h.requireAgentActor(c)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || conversationID == 0 {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.RejectTransferConversation(ctx, &chatv1.RejectTransferConversationRequest{
		ConversationId: conversationID,
		ActorAgentId:   actor.GetAgentId(),
		ActorRole:      actor.GetRole(),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, conversationToJSON(resp))
}

func (h *HTTPHandler) closeConversation(c *gin.Context) {
	actor, err := h.requireAgentActor(c)
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || conversationID == 0 {
		abortBadRequest(c, "invalid conversation id")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Chat.CloseConversation(ctx, &chatv1.CloseConversationRequest{
		ConversationId: conversationID,
		ActorAgentId:   actor.GetAgentId(),
		ActorRole:      actor.GetRole(),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, conversationToJSON(resp))
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *HTTPHandler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Auth.Login(ctx, &authv1.LoginRequest{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, authResultToJSON(resp))
}

func (h *HTTPHandler) me(c *gin.Context) {
	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Auth.Me(ctx, &authv1.MeRequest{
		Authorization: c.GetHeader("Authorization"),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"agent_id": resp.GetAgentId(),
		"email":    resp.GetEmail(),
		"role":     resp.GetRole(),
		"exp":      resp.GetExp(),
	})
}

type createSiteRequest struct {
	SiteID string `json:"site_id" binding:"required,min=4,max=64"`
	Name   string `json:"name" binding:"required,min=1,max=128"`
	Domain string `json:"domain" binding:"required,min=3,max=255"`
}

func (h *HTTPHandler) createSite(c *gin.Context) {
	var req createSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.CreateSite(ctx, &adminv1.CreateSiteRequest{
		Authorization: c.GetHeader("Authorization"),
		SiteId:        req.SiteID,
		Name:          req.Name,
		Domain:        req.Domain,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, siteToJSON(resp))
}

func (h *HTTPHandler) listSites(c *gin.Context) {
	limit := 50
	offset := 0
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 200 {
			abortBadRequest(c, "invalid limit")
			return
		}
		limit = v
	}
	if raw := c.Query("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			abortBadRequest(c, "invalid offset")
			return
		}
		offset = v
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.ListSites(ctx, &adminv1.ListSitesRequest{
		Authorization: c.GetHeader("Authorization"),
		Limit:         int32(limit),
		Offset:        int32(offset),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]any, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		items = append(items, siteToJSON(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

type createAgentRequest struct {
	AgentID     string `json:"agent_id" binding:"required,len=4,numeric"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=12,max=72"`
	DisplayName string `json:"display_name" binding:"required,min=1,max=128"`
	Role        string `json:"role" binding:"omitempty,oneof=agent"`
}

func (h *HTTPHandler) createAgent(c *gin.Context) {
	var req createAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.CreateAgent(ctx, &adminv1.CreateAgentRequest{
		Authorization: c.GetHeader("Authorization"),
		AgentId:       req.AgentID,
		Email:         req.Email,
		Password:      req.Password,
		DisplayName:   req.DisplayName,
		Role:          req.Role,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusCreated, adminAgentToJSON(resp))
}

func (h *HTTPHandler) listAgents(c *gin.Context) {
	limit := 50
	offset := 0
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 200 {
			abortBadRequest(c, "invalid limit")
			return
		}
		limit = v
	}
	if raw := c.Query("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			abortBadRequest(c, "invalid offset")
			return
		}
		offset = v
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.ListAgents(ctx, &adminv1.ListAgentsRequest{
		Authorization: c.GetHeader("Authorization"),
		Limit:         int32(limit),
		Offset:        int32(offset),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]any, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		items = append(items, adminAgentToJSON(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

func (h *HTTPHandler) newCallContext(c *gin.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), h.callTimeout)
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

func (h *HTTPHandler) fetchConversation(c *gin.Context, conversationID uint64) (*chatv1.Conversation, error) {
	ctx, cancel := h.newCallContext(c)
	defer cancel()
	return h.clients.Chat.GetConversation(ctx, &chatv1.GetConversationRequest{Id: conversationID})
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
