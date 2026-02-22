package handler

import (
	"time"

	"github.com/gin-gonic/gin"

	"inlinechat/services/gateway-service/internal/grpcclient"
	"inlinechat/services/gateway-service/internal/ratelimit"
)

const maxMessageContentChars = 2000

type HTTPHandler struct {
	clients        *grpcclient.Clients
	callTimeout    time.Duration
	loginLimiter   *ratelimit.Limiter
	visitorLimiter *ratelimit.Limiter
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

func (h *HTTPHandler) SetRateLimiters(loginLimiter *ratelimit.Limiter, visitorLimiter *ratelimit.Limiter) {
	h.loginLimiter = loginLimiter
	h.visitorLimiter = visitorLimiter
}

func (h *HTTPHandler) RegisterRoutes(r *gin.Engine) {
	h.registerChatRoutes(r)
	h.registerAuthRoutes(r)
	h.registerAdminRoutes(r)
}
