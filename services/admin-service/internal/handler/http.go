package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"inlinechat/services/admin-service/internal/model"
	"inlinechat/services/admin-service/internal/security"
	"inlinechat/services/admin-service/internal/service"
)

type HTTPHandler struct {
	// HTTP Handler 负责参数解析、鉴权上下文读取与 JSON 响应格式统一。
	adminService *service.AdminService
}

func NewHTTPHandler(adminService *service.AdminService) *HTTPHandler {
	return &HTTPHandler{adminService: adminService}
}

type createSiteRequest struct {
	SiteID string `json:"site_id" binding:"required,min=4,max=64"`
	Name   string `json:"name" binding:"required,min=1,max=128"`
	Domain string `json:"domain" binding:"required,min=3,max=255"`
}

type createAgentRequest struct {
	AgentID     string `json:"agent_id" binding:"required,len=4,numeric"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=12,max=72"`
	DisplayName string `json:"display_name" binding:"required,min=1,max=128"`
	Role        string `json:"role" binding:"omitempty,oneof=agent"`
}

type updateSiteStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active disabled"`
}

type updateAgentStatusRequest struct {
	Status string `json:"status" binding:"required,oneof=active inactive"`
}

type resetAgentPasswordRequest struct {
	NewPassword string `json:"new_password" binding:"required,min=12,max=72"`
}

func (h *HTTPHandler) RegisterRoutes(rg *gin.RouterGroup) {
	// 管理后台能力：站点管理、坐席管理、审计查询。
	rg.POST("/sites", h.createSite)
	rg.GET("/sites", h.listSites)
	rg.PATCH("/sites/:site_id/status", h.updateSiteStatus)
	rg.POST("/sites/:site_id/rotate-widget-key", h.rotateSiteWidgetKey)
	rg.POST("/agents", h.createAgent)
	rg.GET("/agents", h.listAgents)
	rg.PATCH("/agents/:id/status", h.updateAgentStatus)
	rg.POST("/agents/:id/reset-password", h.resetAgentPassword)
	rg.POST("/agents/:id/force-logout", h.forceAgentLogout)
	rg.GET("/audit-logs", h.listAuditLogs)
}

func (h *HTTPHandler) createSite(c *gin.Context) {
	var req createSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	site, err := h.adminService.CreateSiteWithActor(c.Request.Context(), service.CreateSiteInput{
		SiteID: req.SiteID,
		Name:   req.Name,
		Domain: req.Domain,
	}, extractActor(c))
	if err != nil {
		if errors.Is(err, service.ErrConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, site)
}

func (h *HTTPHandler) updateSiteStatus(c *gin.Context) {
	claims, ok := requireSuperAdminClaims(c)
	if !ok {
		return
	}

	var req updateSiteStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	site, err := h.adminService.UpdateSiteStatus(c.Request.Context(), service.UpdateSiteStatusInput{
		SiteID: c.Param("site_id"),
		Status: req.Status,
	}, extractActorWithClaims(c, claims))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, site)
}

func (h *HTTPHandler) rotateSiteWidgetKey(c *gin.Context) {
	claims, ok := requireSuperAdminClaims(c)
	if !ok {
		return
	}

	site, err := h.adminService.RotateSiteWidgetKey(c.Request.Context(), service.RotateSiteWidgetKeyInput{
		SiteID: c.Param("site_id"),
	}, extractActorWithClaims(c, claims))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, site)
}

func (h *HTTPHandler) listSites(c *gin.Context) {
	limit, offset, ok := parsePaging(c)
	if !ok {
		return
	}
	sites, err := h.adminService.ListSites(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": sites})
}

func (h *HTTPHandler) createAgent(c *gin.Context) {
	var req createAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	claimsAny, ok := c.Get("claims")
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "missing auth claims"})
		return
	}
	claims, ok := claimsAny.(*security.Claims)
	if !ok || claims.Role != "super_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "super_admin role required"})
		return
	}

	agent, err := h.adminService.CreateAgentWithActor(c.Request.Context(), service.CreateAgentInput{
		AgentID:     req.AgentID,
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Role:        req.Role,
	}, extractActorWithClaims(c, claims))
	if err != nil {
		if errors.Is(err, service.ErrConflict) {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, agent)
}

func (h *HTTPHandler) listAgents(c *gin.Context) {
	limit, offset, ok := parsePaging(c)
	if !ok {
		return
	}
	agents, err := h.adminService.ListAgents(c.Request.Context(), limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": agents})
}

func (h *HTTPHandler) updateAgentStatus(c *gin.Context) {
	claims, ok := requireSuperAdminClaims(c)
	if !ok {
		return
	}

	agentID, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || agentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent_id"})
		return
	}

	var req updateAgentStatusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	agent, err := h.adminService.UpdateAgentStatus(c.Request.Context(), service.UpdateAgentStatusInput{
		AgentID: agentID,
		Status:  req.Status,
	}, extractActorWithClaims(c, claims))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, agent)
}

func (h *HTTPHandler) resetAgentPassword(c *gin.Context) {
	claims, ok := requireSuperAdminClaims(c)
	if !ok {
		return
	}

	agentID, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || agentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent_id"})
		return
	}

	var req resetAgentPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	agent, err := h.adminService.ResetAgentPassword(c.Request.Context(), service.ResetAgentPasswordInput{
		AgentID:     agentID,
		NewPassword: req.NewPassword,
	}, extractActorWithClaims(c, claims))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, agent)
}

func (h *HTTPHandler) forceAgentLogout(c *gin.Context) {
	claims, ok := requireSuperAdminClaims(c)
	if !ok {
		return
	}

	agentID, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || agentID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid agent_id"})
		return
	}

	agent, err := h.adminService.ForceAgentLogout(c.Request.Context(), service.ForceAgentLogoutInput{
		AgentID: agentID,
	}, extractActorWithClaims(c, claims))
	if err != nil {
		if errors.Is(err, service.ErrNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, agent)
}

func (h *HTTPHandler) listAuditLogs(c *gin.Context) {
	limit, offset, ok := parsePaging(c)
	if !ok {
		return
	}
	actorAgentID := uint64(0)
	if raw := strings.TrimSpace(c.Query("actor_agent_id")); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid actor_agent_id"})
			return
		}
		actorAgentID = parsed
	}

	items, err := h.adminService.ListAuditLogs(c.Request.Context(), service.ListAuditLogsInput{
		Limit:        limit,
		Offset:       offset,
		ActorAgentID: actorAgentID,
		Action:       c.Query("action"),
		ResourceType: c.Query("resource_type"),
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	payload := make([]any, 0, len(items))
	for i := range items {
		payload = append(payload, auditLogToJSON(items[i]))
	}
	c.JSON(http.StatusOK, gin.H{"items": payload})
}

// requireSuperAdminClaims 用于高危接口（改状态、改密码、强制下线）做角色兜底。
func requireSuperAdminClaims(c *gin.Context) (*security.Claims, bool) {
	claimsAny, ok := c.Get("claims")
	if !ok {
		c.JSON(http.StatusForbidden, gin.H{"error": "missing auth claims"})
		return nil, false
	}
	claims, ok := claimsAny.(*security.Claims)
	if !ok || claims.Role != "super_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "super_admin role required"})
		return nil, false
	}
	return claims, true
}

func extractActor(c *gin.Context) service.ActorContext {
	claimsAny, ok := c.Get("claims")
	if !ok {
		return service.ActorContext{
			IP:        c.ClientIP(),
			UserAgent: c.GetHeader("User-Agent"),
		}
	}
	claims, _ := claimsAny.(*security.Claims)
	return extractActorWithClaims(c, claims)
}

func extractActorWithClaims(c *gin.Context, claims *security.Claims) service.ActorContext {
	actor := service.ActorContext{
		IP:        c.ClientIP(),
		UserAgent: c.GetHeader("User-Agent"),
	}
	if claims == nil {
		return actor
	}
	actor.AgentID = claims.AgentID
	actor.Email = claims.Email
	actor.Role = claims.Role
	return actor
}

func auditLogToJSON(item model.AuditLog) gin.H {
	return gin.H{
		"id":             item.ID,
		"actor_agent_id": item.ActorAgentID,
		"actor_email":    item.ActorEmail,
		"actor_role":     item.ActorRole,
		"action":         item.Action,
		"resource_type":  item.ResourceType,
		"resource_id":    item.ResourceID,
		"summary":        item.Summary,
		"ip":             item.IP,
		"user_agent":     item.UserAgent,
		"created_at":     item.CreatedAt,
	}
}

// parsePaging 统一分页参数约束，避免各接口重复实现。
func parsePaging(c *gin.Context) (int, int, bool) {
	limit := 50
	offset := 0
	if raw := c.Query("limit"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v <= 0 || v > 200 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit"})
			return 0, 0, false
		}
		limit = v
	}
	if raw := c.Query("offset"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid offset"})
			return 0, 0, false
		}
		offset = v
	}
	return limit, offset, true
}
