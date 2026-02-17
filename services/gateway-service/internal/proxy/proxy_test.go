package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestReverseProxy_ErrorHandlerIncludeRequestID(t *testing.T) {
	handler, err := NewReverseProxy("http://127.0.0.1:1", "/api/chat", "X-Request-ID", zap.NewNop())
	if err != nil {
		t.Fatalf("create proxy failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations", nil)
	req.Header.Set("X-Request-ID", "req_abc")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if got := w.Header().Get("X-Request-ID"); got != "req_abc" {
		t.Fatalf("unexpected request id header: %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "\"code\":\"upstream_unavailable\"") {
		t.Fatalf("missing code in body: %s", body)
	}
	if !strings.Contains(body, "\"request_id\":\"req_abc\"") {
		t.Fatalf("missing request_id in body: %s", body)
	}
}
