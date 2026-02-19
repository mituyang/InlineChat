package httpmiddleware

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	DefaultRequestIDHeader = "X-Request-ID"
	ContextKeyRequestID    = "request_id"
)

func RequestContext(requestIDHeader string, logger *zap.Logger) gin.HandlerFunc {
	header := strings.TrimSpace(requestIDHeader)
	if header == "" {
		header = DefaultRequestIDHeader
	}

	return func(c *gin.Context) {
		start := time.Now()

		requestID := strings.TrimSpace(c.GetHeader(header))
		if requestID == "" {
			requestID = generateRequestID()
		}

		c.Request.Header.Set(header, requestID)
		c.Writer.Header().Set(header, requestID)
		c.Set(ContextKeyRequestID, requestID)

		c.Next()

		if logger != nil {
			logger.Info("request completed",
				zap.String("request_id", requestID),
				zap.String("method", c.Request.Method),
				zap.String("path", c.Request.URL.Path),
				zap.Int("status", c.Writer.Status()),
				zap.Duration("latency", time.Since(start)),
				zap.String("client_ip", c.ClientIP()),
			)
		}
	}
}

func generateRequestID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().UTC().Format("20060102150405.000000000")
	}
	return hex.EncodeToString(buf)
}
