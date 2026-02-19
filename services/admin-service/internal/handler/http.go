package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"inlinechat/services/admin-service/internal/security"
	"inlinechat/services/admin-service/internal/service"
)

type HTTPHandler struct {
	adminService *service.AdminService
}

func NewHTTPHandler(adminService *service.AdminService) *HTTPHandler {
	return &HTTPHandler{adminService: adminService}
}

type createSiteRequest struct {
	Name   string `json:"name" binding:"required,min=1,max=128"`
	Domain string `json:"domain" binding:"required,min=3,max=255"`
}

type createAgentRequest struct {
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=12,max=72"`
	DisplayName string `json:"display_name" binding:"required,min=1,max=128"`
	Role        string `json:"role" binding:"omitempty,oneof=agent"`
}

func (h *HTTPHandler) RegisterRoutes(rg *gin.RouterGroup) {
	rg.POST("/sites", h.createSite)
	rg.GET("/sites", h.listSites)
	rg.POST("/agents", h.createAgent)
	rg.GET("/agents", h.listAgents)
}

func (h *HTTPHandler) createSite(c *gin.Context) {
	var req createSiteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	site, err := h.adminService.CreateSite(c.Request.Context(), service.CreateSiteInput{Name: req.Name, Domain: req.Domain})
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

	agent, err := h.adminService.CreateAgent(c.Request.Context(), service.CreateAgentInput{
		Email:       req.Email,
		Password:    req.Password,
		DisplayName: req.DisplayName,
		Role:        req.Role,
	})
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
