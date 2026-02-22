package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	authv1 "inlinechat/services/gateway-service/internal/gen/authv1"
)

func (h *HTTPHandler) registerAuthRoutes(r *gin.Engine) {
	authV1 := r.Group("/api/auth/v1/auth")
	authV1.POST("/login", h.login)
	authV1.GET("/me", h.me)
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
	if !h.applyLoginRateLimit(c, req.Email) {
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
