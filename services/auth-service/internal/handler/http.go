package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"inlinechat/services/auth-service/internal/service"
)

type HTTPHandler struct {
	// HTTP 层承担输入校验和响应封装，复用 authService 完成认证逻辑。
	authService *service.AuthService
}

func NewHTTPHandler(authService *service.AuthService) *HTTPHandler {
	return &HTTPHandler{authService: authService}
}

type loginRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}

func (h *HTTPHandler) RegisterRoutes(rg *gin.RouterGroup) {
	// 仅暴露最小认证面：登录 + token 自检。
	rg.POST("/auth/login", h.login)
	rg.GET("/auth/me", h.me)
}

func (h *HTTPHandler) login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	result, err := h.authService.Login(c.Request.Context(), service.LoginInput{
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCredential):
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		case errors.Is(err, service.ErrUnauthorized):
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		}
		return
	}

	c.JSON(http.StatusOK, result)
}

// me 通过 bearer token 返回当前身份，供前端启动时恢复会话。
func (h *HTTPHandler) me(c *gin.Context) {
	token := parseBearerToken(c.GetHeader("Authorization"))
	if token == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
		return
	}

	claims, err := h.authService.ValidateToken(c.Request.Context(), token)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	siteID := ""
	if claims.Role == "agent" || claims.Role == "admin" {
		agent, getErr := h.authService.GetAgentByID(c.Request.Context(), claims.AgentID)
		if getErr != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		siteID = agent.SiteID
	}

	c.JSON(http.StatusOK, gin.H{
		"agent_id": claims.AgentID,
		"email":    claims.Email,
		"role":     claims.Role,
		"exp":      claims.ExpiresAt,
		"site_id":  siteID,
	})
}

// parseBearerToken 解析标准 Authorization: Bearer <token>。
func parseBearerToken(header string) string {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
