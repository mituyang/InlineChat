package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
)

func TestHTTPMetricsMiddlewareAndHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reg := prometheus.NewRegistry()
	metrics := NewHTTPMetrics("gateway-service", reg)

	r := gin.New()
	r.Use(metrics.Middleware())
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	r.GET("/metrics", MetricsHandler(reg))

	req1 := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr1 := httptest.NewRecorder()
	r.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr1.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rr2 := httptest.NewRecorder()
	r.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 from /metrics, got %d", rr2.Code)
	}
	body := rr2.Body.String()
	if !strings.Contains(body, "inlinechat_gateway_service_http_requests_total") {
		t.Fatalf("metrics output missing requests_total: %s", body)
	}
	if !strings.Contains(body, `route="/healthz"`) {
		t.Fatalf("metrics output missing /healthz route label: %s", body)
	}
}
