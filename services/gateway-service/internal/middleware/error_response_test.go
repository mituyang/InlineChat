package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestAbortWithError_IncludeRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(ContextKeyRequestID, "req_001")
		c.Next()
	})
	r.GET("/test", func(c *gin.Context) {
		AbortWithError(c, http.StatusBadRequest, "bad_request", "invalid payload")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	body := w.Body.String()
	if body == "" {
		t.Fatalf("empty body")
	}
	if got := w.Header().Get("Content-Type"); got == "" {
		t.Fatalf("missing content-type")
	}
	if !strings.Contains(body, "\"request_id\":\"req_001\"") {
		t.Fatalf("response missing request_id: %s", body)
	}
	if !strings.Contains(body, "\"code\":\"bad_request\"") {
		t.Fatalf("response missing error code: %s", body)
	}
}
