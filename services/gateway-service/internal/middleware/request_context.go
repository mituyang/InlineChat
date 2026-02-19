package middleware

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	httpmiddleware "inlinechat/packages/httpmiddleware"
)

const (
	DefaultRequestIDHeader = httpmiddleware.DefaultRequestIDHeader
	ContextKeyRequestID    = httpmiddleware.ContextKeyRequestID
)

func RequestContext(requestIDHeader string, logger *zap.Logger) gin.HandlerFunc {
	return httpmiddleware.RequestContext(requestIDHeader, logger)
}
