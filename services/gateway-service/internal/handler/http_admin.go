package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	"inlinechat/services/gateway-service/internal/middleware"
)

func (h *HTTPHandler) registerAdminRoutes(r *gin.Engine) {
	adminV1 := r.Group("/api/admin/v1/admin")
	adminV1.POST("/sites", h.createSite)
	adminV1.GET("/sites", h.listSites)
	adminV1.PATCH("/sites/:site_id/status", h.updateSiteStatus)
	adminV1.POST("/sites/:site_id/rotate-widget-key", h.rotateSiteWidgetKey)
	adminV1.GET("/sites/:site_id/ai-config", h.getSiteAIConfig)
	adminV1.PATCH("/sites/:site_id/ai-config", h.updateSiteAIConfig)
	adminV1.POST("/sites/:site_id/ai/reload", h.reloadSiteAIKnowledge)
	adminV1.POST("/agents", h.createAgent)
	adminV1.GET("/agents", h.listAgents)
	adminV1.PATCH("/agents/:id/status", h.updateAgentStatus)
	adminV1.POST("/agents/:id/reset-password", h.resetAgentPassword)
	adminV1.POST("/agents/:id/force-logout", h.forceAgentLogout)
	adminV1.GET("/audit-logs", h.listAuditLogs)
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
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "create_site", actor.GetAgentId()) {
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
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "list_sites", actor.GetAgentId()) {
		return
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

func (h *HTTPHandler) updateSiteStatus(c *gin.Context) {
	var req updateSiteStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "update_site_status", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.UpdateSiteStatus(ctx, &adminv1.UpdateSiteStatusRequest{
		Authorization: c.GetHeader("Authorization"),
		SiteId:        c.Param("site_id"),
		Status:        req.Status,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, siteToJSON(resp))
}

func (h *HTTPHandler) rotateSiteWidgetKey(c *gin.Context) {
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "rotate_site_widget_key", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.RotateSiteWidgetKey(ctx, &adminv1.RotateSiteWidgetKeyRequest{
		Authorization: c.GetHeader("Authorization"),
		SiteId:        c.Param("site_id"),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, siteToJSON(resp))
}

func (h *HTTPHandler) getSiteAIConfig(c *gin.Context) {
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "get_site_ai_config", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.GetSiteAIConfig(ctx, &adminv1.GetSiteAIConfigRequest{
		SiteId: c.Param("site_id"),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, siteAIConfigToJSON(resp))
}

func (h *HTTPHandler) updateSiteAIConfig(c *gin.Context) {
	var req updateSiteAIConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}

	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if actor.GetRole() != "super_admin" {
		abortForbidden(c, "super_admin role required")
		return
	}
	if !h.applyAdminRateLimit(c, "update_site_ai_config", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.UpdateSiteAIConfig(ctx, &adminv1.UpdateSiteAIConfigRequest{
		Authorization: c.GetHeader("Authorization"),
		SiteId:        c.Param("site_id"),
		Enabled:       req.Enabled,
		ReplyMode:     req.ReplyMode,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, siteAIConfigToJSON(resp))
}

func (h *HTTPHandler) reloadSiteAIKnowledge(c *gin.Context) {
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "reload_site_ai_knowledge", actor.GetAgentId()) {
		return
	}
	if h.aiClient == nil {
		middleware.AbortWithError(c, http.StatusServiceUnavailable, "service_unavailable", "ai reload service is unavailable")
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.aiClient.Reload(ctx, c.Param("site_id"))
	if err != nil {
		middleware.AbortWithError(c, http.StatusBadGateway, "upstream_error", err.Error())
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"site_id":     resp.SiteID,
		"chunk_count": resp.ChunkCount,
		"reloaded_at": resp.ReloadedAt,
	})
}

type createAgentRequest struct {
	AgentID     string `json:"agent_id" binding:"required,len=4,numeric"`
	SiteID      string `json:"site_id" binding:"required,min=4,max=64"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=12,max=72"`
	DisplayName string `json:"display_name" binding:"required,min=1,max=128"`
	Role        string `json:"role" binding:"omitempty,oneof=agent"`
}

type updateSiteStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active disabled"`
}

type updateSiteAIConfigRequest struct {
	Enabled   bool   `json:"enabled"`
	ReplyMode string `json:"reply_mode" binding:"omitempty,oneof=unassigned_auto_reply"`
}

type updateAgentStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active inactive"`
}

type resetAgentPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=12,max=72"`
}

func (h *HTTPHandler) createAgent(c *gin.Context) {
	var req createAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "create_agent", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.CreateAgent(ctx, &adminv1.CreateAgentRequest{
		Authorization: c.GetHeader("Authorization"),
		AgentId:       req.AgentID,
		SiteId:        req.SiteID,
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
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "list_agents", actor.GetAgentId()) {
		return
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

func (h *HTTPHandler) updateAgentStatus(c *gin.Context) {
	agentID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || agentID == 0 {
		abortBadRequest(c, "invalid agent_id")
		return
	}

	var req updateAgentStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "update_agent_status", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.UpdateAgentStatus(ctx, &adminv1.UpdateAgentStatusRequest{
		Authorization: c.GetHeader("Authorization"),
		AgentId:       agentID,
		Status:        req.Status,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, adminAgentToJSON(resp))
}

func (h *HTTPHandler) resetAgentPassword(c *gin.Context) {
	agentID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || agentID == 0 {
		abortBadRequest(c, "invalid agent_id")
		return
	}

	var req resetAgentPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abortBadRequest(c, err.Error())
		return
	}
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "reset_agent_password", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.ResetAgentPassword(ctx, &adminv1.ResetAgentPasswordRequest{
		Authorization: c.GetHeader("Authorization"),
		AgentId:       agentID,
		NewPassword:   req.NewPassword,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, adminAgentToJSON(resp))
}

func (h *HTTPHandler) forceAgentLogout(c *gin.Context) {
	agentID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || agentID == 0 {
		abortBadRequest(c, "invalid agent_id")
		return
	}
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "force_agent_logout", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.ForceAgentLogout(ctx, &adminv1.ForceAgentLogoutRequest{
		Authorization: c.GetHeader("Authorization"),
		AgentId:       agentID,
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	c.JSON(http.StatusOK, adminAgentToJSON(resp))
}

func (h *HTTPHandler) listAuditLogs(c *gin.Context) {
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

	var actorAgentID uint64
	if raw := strings.TrimSpace(c.Query("actor_agent_id")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			abortBadRequest(c, "invalid actor_agent_id")
			return
		}
		actorAgentID = parsed
	}
	actor, actorErr := h.requireAdminActor(c)
	if actorErr != nil {
		handleGRPCError(c, actorErr)
		return
	}
	if !h.applyAdminRateLimit(c, "list_audit_logs", actor.GetAgentId()) {
		return
	}

	ctx, cancel := h.newCallContext(c)
	defer cancel()

	resp, err := h.clients.Admin.ListAuditLogs(ctx, &adminv1.ListAuditLogsRequest{
		Authorization: c.GetHeader("Authorization"),
		Limit:         int32(limit),
		Offset:        int32(offset),
		ActorAgentId:  actorAgentID,
		Action:        c.Query("action"),
		ResourceType:  c.Query("resource_type"),
	})
	if err != nil {
		handleGRPCError(c, err)
		return
	}

	items := make([]any, 0, len(resp.GetItems()))
	for _, item := range resp.GetItems() {
		items = append(items, auditLogToJSON(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
