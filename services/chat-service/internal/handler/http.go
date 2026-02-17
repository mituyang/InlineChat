package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"inlinechat/services/chat-service/internal/service"
)

type HTTPHandler struct {
	chatService *service.ChatService
}

func NewHTTPHandler(chatService *service.ChatService) *HTTPHandler {
	return &HTTPHandler{chatService: chatService}
}

type createConversationRequest struct {
	SiteID       string `json:"site_id" binding:"required"`
	VisitorToken string `json:"visitor_token" binding:"required"`
}

type createMessageRequest struct {
	SenderType   string `json:"sender_type" binding:"required"`
	SenderID     string `json:"sender_id"`
	Content      string `json:"content" binding:"required"`
	ClientMsgID  string `json:"client_msg_id" binding:"required"`
	VisitorToken string `json:"visitor_token"`
}

func (h *HTTPHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/conversations", h.createConversation)
	rg.GET("/conversations/:id", h.getConversation)
	rg.POST("/conversations/:id/messages", h.createMessage)
	rg.GET("/conversations/:id/messages", h.listMessages)
}

func (h *HTTPHandler) createConversation(c *gin.Context) {
	var req createConversationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	conversation, err := h.chatService.CreateConversation(c.Request.Context(), service.CreateConversationInput{
		SiteID:       req.SiteID,
		VisitorToken: req.VisitorToken,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, conversation)
}

func (h *HTTPHandler) getConversation(c *gin.Context) {
	conversationID, ok := parseConversationID(c)
	if !ok {
		return
	}

	conversation, err := h.chatService.GetConversation(c.Request.Context(), conversationID)
	if err != nil {
		if errors.Is(err, service.ErrConversationNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, conversation)
}

func (h *HTTPHandler) createMessage(c *gin.Context) {
	conversationID, ok := parseConversationID(c)
	if !ok {
		return
	}

	var req createMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	message, err := h.chatService.CreateMessage(c.Request.Context(), service.CreateMessageInput{
		ConversationID: conversationID,
		SenderType:     req.SenderType,
		SenderID:       req.SenderID,
		Content:        req.Content,
		ClientMsgID:    req.ClientMsgID,
		VisitorToken:   req.VisitorToken,
	})
	if err != nil {
		if errors.Is(err, service.ErrConversationNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, message)
}

func (h *HTTPHandler) listMessages(c *gin.Context) {
	conversationID, ok := parseConversationID(c)
	if !ok {
		return
	}

	limit := 50
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return
		}
		limit = v
	}

	var beforeID uint64
	if raw := c.Query("before_id"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid before_id"})
			return
		}
		beforeID = v
	}

	messages, err := h.chatService.ListMessages(c.Request.Context(), conversationID, limit, beforeID)
	if err != nil {
		if errors.Is(err, service.ErrConversationNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"items": messages})
}

func parseConversationID(c *gin.Context) (uint64, bool) {
	conversationID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid conversation id"})
		return 0, false
	}
	return conversationID, true
}
