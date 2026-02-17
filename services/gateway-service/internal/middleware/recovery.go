package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				requestID, _ := c.Get(ContextKeyRequestID)
				requestIDText, _ := requestID.(string)

				logger.Error("panic recovered",
					zap.Any("panic", rec),
					zap.String("request_id", requestIDText),
				)

				AbortWithError(c, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()

		c.Next()
	}
}
