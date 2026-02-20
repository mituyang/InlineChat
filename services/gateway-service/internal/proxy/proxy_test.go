package proxy

import (
	"context"
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

func TestDynamicReverseProxy_ResolveFailed(t *testing.T) {
	handler, err := NewDynamicReverseProxy(func(context.Context) (string, error) {
		return "", context.DeadlineExceeded
	}, "/api/chat", "X-Request-ID", zap.NewNop())
	if err != nil {
		t.Fatalf("create dynamic proxy failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations", nil)
	req.Header.Set("X-Request-ID", "req_dynamic_err")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("unexpected status: %d", w.Code)
	}
	if got := w.Header().Get("X-Request-ID"); got != "req_dynamic_err" {
		t.Fatalf("unexpected request id header: %q", got)
	}
	body := w.Body.String()
	if !strings.Contains(body, "\"code\":\"upstream_unavailable\"") {
		t.Fatalf("missing code in body: %s", body)
	}
}

func TestDynamicReverseProxy_ResolvePerRequest(t *testing.T) {
	upstreamA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream_a"))
	}))
	defer upstreamA.Close()

	upstreamB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("upstream_b"))
	}))
	defer upstreamB.Close()

	turn := 0
	handler, err := NewDynamicReverseProxy(func(context.Context) (string, error) {
		turn++
		if turn%2 == 1 {
			return upstreamA.URL, nil
		}
		return upstreamB.URL, nil
	}, "", "X-Request-ID", zap.NewNop())
	if err != nil {
		t.Fatalf("create dynamic proxy failed: %v", err)
	}

	reqA := httptest.NewRequest(http.MethodGet, "/ws/1", nil)
	wA := httptest.NewRecorder()
	handler.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusOK || strings.TrimSpace(wA.Body.String()) != "upstream_a" {
		t.Fatalf("unexpected response from first target: status=%d body=%s", wA.Code, wA.Body.String())
	}

	reqB := httptest.NewRequest(http.MethodGet, "/ws/1", nil)
	wB := httptest.NewRecorder()
	handler.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusOK || strings.TrimSpace(wB.Body.String()) != "upstream_b" {
		t.Fatalf("unexpected response from second target: status=%d body=%s", wB.Code, wB.Body.String())
	}
}
