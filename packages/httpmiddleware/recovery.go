package httpmiddleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				requestID, _ := c.Get(ContextKeyRequestID)
				reqID, _ := requestID.(string)
				if logger != nil {
					logger.Error("panic recovered",
						zap.Any("panic", rec),
						zap.ByteString("stack", debug.Stack()),
						zap.String("request_id", reqID),
						zap.String("method", c.Request.Method),
						zap.String("path", c.Request.URL.Path),
					)
				}
				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error":      "internal error",
					"request_id": reqID,
				})
			}
		}()
		c.Next()
	}
}
