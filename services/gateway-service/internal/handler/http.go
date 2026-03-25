package handler

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"inlinechat/services/gateway-service/internal/aiclient"
	"inlinechat/services/gateway-service/internal/grpcclient"
	"inlinechat/services/gateway-service/internal/ratelimit"
)

const maxMessageContentChars = 2000

// HTTPHandler 负责承接网关入口请求，并分发到 chat/auth/admin 三类上游。
type HTTPHandler struct {
	clients         *grpcclient.Clients
	aiClient        aiReloader
	callTimeout     time.Duration
	loginLimiter    *ratelimit.Limiter
	visitorLimiter  *ratelimit.Limiter
	agentLimiter    *ratelimit.Limiter
	adminLimiter    *ratelimit.Limiter
	widgetIndexHTML []byte
	now             func() time.Time
}

type aiReloader interface {
	Reload(ctx context.Context, siteID string) (*aiclient.ReloadResponse, error)
}

func NewHTTPHandler(clients *grpcclient.Clients, callTimeout time.Duration) *HTTPHandler {
	if callTimeout <= 0 {
		callTimeout = 8 * time.Second
	}
	return &HTTPHandler{
		clients:     clients,
		callTimeout: callTimeout,
		now:         time.Now,
	}
}

func (h *HTTPHandler) SetRateLimiters(loginLimiter *ratelimit.Limiter, visitorLimiter *ratelimit.Limiter) {
	h.loginLimiter = loginLimiter
	h.visitorLimiter = visitorLimiter
}

func (h *HTTPHandler) SetStaffRateLimiters(agentLimiter *ratelimit.Limiter, adminLimiter *ratelimit.Limiter) {
	h.agentLimiter = agentLimiter
	h.adminLimiter = adminLimiter
}

func (h *HTTPHandler) SetWidgetIndexHTML(indexHTML []byte) {
	if len(indexHTML) == 0 {
		h.widgetIndexHTML = nil
		return
	}
	h.widgetIndexHTML = append([]byte(nil), indexHTML...)
}

func (h *HTTPHandler) SetAIClient(client aiReloader) {
	h.aiClient = client
}

func (h *HTTPHandler) RegisterRoutes(r *gin.Engine) {
	// 路由按业务域拆分，便于后续按服务边界继续扩展。
	h.registerChatRoutes(r)
	h.registerAuthRoutes(r)
	h.registerAdminRoutes(r)
}
