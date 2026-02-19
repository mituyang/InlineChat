package httpmiddleware

import (
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type PanicHandler func(c *gin.Context, recovered any, requestID string)

func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return RecoveryWithHandler(logger, nil)
}

func RecoveryWithHandler(logger *zap.Logger, panicHandler PanicHandler) gin.HandlerFunc {
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

				if panicHandler != nil {
					panicHandler(c, rec, reqID)
					return
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
