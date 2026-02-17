package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type ErrorPayload struct {
	Error     ErrorDetail `json:"error"`
	RequestID string      `json:"request_id,omitempty"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func AbortWithError(c *gin.Context, status int, code string, message string) {
	requestID, _ := c.Get(ContextKeyRequestID)
	requestIDText, _ := requestID.(string)
	c.AbortWithStatusJSON(status, ErrorPayload{
		Error: ErrorDetail{
			Code:    code,
			Message: message,
		},
		RequestID: requestIDText,
	})
}

func NoRouteHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		AbortWithError(c, http.StatusNotFound, "route_not_found", "route not found")
	}
}

func NoMethodHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		AbortWithError(c, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}
