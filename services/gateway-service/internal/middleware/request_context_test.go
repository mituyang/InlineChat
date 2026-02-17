package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestRequestContext_GenerateAndExposeRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := zap.NewNop()
	r := gin.New()
	r.Use(RequestContext("X-Request-ID", logger))

	r.GET("/test", func(c *gin.Context) {
		requestIDAny, ok := c.Get(ContextKeyRequestID)
		if !ok {
			t.Fatalf("request id missing in context")
		}
		requestID, _ := requestIDAny.(string)
		if requestID == "" {
			t.Fatalf("request id is empty")
		}
		if got := c.GetHeader("X-Request-ID"); got != requestID {
			t.Fatalf("request header not injected, got=%q expected=%q", got, requestID)
		}
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatalf("response missing X-Request-ID")
	}
}

func TestRequestContext_KeepExistingRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	logger := zap.NewNop()
	r := gin.New()
	r.Use(RequestContext("X-Request-ID", logger))

	r.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Request-ID", "req_123")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("X-Request-ID"); got != "req_123" {
		t.Fatalf("unexpected response request id: %q", got)
	}
}
