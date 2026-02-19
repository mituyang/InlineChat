package httpmiddleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestContextGeneratesAndPropagatesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestContext(DefaultRequestIDHeader, nil))
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rr.Code)
	}
	requestID := rr.Header().Get(DefaultRequestIDHeader)
	if requestID == "" {
		t.Fatal("expected response header request id, got empty")
	}
}

func TestRecoveryIncludesRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(RequestContext(DefaultRequestIDHeader, nil), Recovery(nil))
	r.GET("/panic", func(_ *gin.Context) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}

	if body["request_id"] == "" {
		t.Fatalf("expected request_id in recovery response, got %v", body)
	}
}

func TestRecoveryWithHandlerCustomResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(
		RequestContext(DefaultRequestIDHeader, nil),
		RecoveryWithHandler(nil, func(c *gin.Context, _ any, requestID string) {
			c.AbortWithStatusJSON(http.StatusTeapot, gin.H{
				"error":      "custom panic",
				"request_id": requestID,
			})
		}),
	)
	r.GET("/panic", func(_ *gin.Context) {
		panic("boom")
	})

	req := httptest.NewRequest(http.MethodGet, "/panic", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusTeapot {
		t.Fatalf("expected status %d, got %d", http.StatusTeapot, rr.Code)
	}

	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid json response: %v", err)
	}
	if body["error"] != "custom panic" {
		t.Fatalf("unexpected body: %v", body)
	}
	if body["request_id"] == "" {
		t.Fatalf("expected request_id in custom response, got %v", body)
	}
}
