package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	chatv1 "inlinechat/services/gateway-service/internal/gen/chatv1"
)

func (h *HTTPHandler) registerChatRoutes(r *gin.Engine) {
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
	if !h.applyVisitorRateLimit(c, "create_conversation", req.SiteID, 0, req.VisitorToken) {
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
		if !h.applyAgentRateLimit(c, "get_conversation", actor.GetAgentId()) {
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
		if !h.applyVisitorRateLimit(c, "get_conversation", "", conversationID, visitorToken) {
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
	if !h.applyAgentRateLimit(c, "list_conversations", actor.GetAgentId()) {
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
		if !h.applyAgentRateLimit(c, "create_message", actor.GetAgentId()) {
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
		if !h.applyVisitorRateLimit(c, "create_message", "", conversationID, req.VisitorToken) {
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
		if !h.applyAgentRateLimit(c, "list_messages", actor.GetAgentId()) {
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
		if !h.applyVisitorRateLimit(c, "list_messages", "", conversationID, visitorToken) {
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
		if !h.applyAgentRateLimit(c, "mark_read", actor.GetAgentId()) {
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
	} else if !h.applyVisitorRateLimit(c, "mark_read", "", conversationID, visitorToken) {
		return
	} else if _, convErr := h.requireConversationForVisitor(c, conversationID, visitorToken); convErr != nil {
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
	if !h.applyAgentRateLimit(c, "claim_conversation", actor.GetAgentId()) {
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
	if !h.applyAgentRateLimit(c, "transfer_conversation", actor.GetAgentId()) {
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
	if !h.applyAgentRateLimit(c, "confirm_transfer_conversation", actor.GetAgentId()) {
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
	if !h.applyAgentRateLimit(c, "reject_transfer_conversation", actor.GetAgentId()) {
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
	if !h.applyAgentRateLimit(c, "close_conversation", actor.GetAgentId()) {
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
