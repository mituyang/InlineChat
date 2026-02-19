package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	httpmiddleware "inlinechat/packages/httpmiddleware"
)

func Recovery(logger *zap.Logger) gin.HandlerFunc {
	return httpmiddleware.RecoveryWithHandler(logger, func(c *gin.Context, _ any, _ string) {
		AbortWithError(c, http.StatusInternalServerError, "internal_error", "internal server error")
	})
}
