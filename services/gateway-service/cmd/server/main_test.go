package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"inlinechat/services/gateway-service/internal/grpcclient"
	"inlinechat/services/gateway-service/internal/handler"
)

func TestMountWidgetRouteServesStaticAssetWithoutRouteConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<!doctype html><html><body>widget</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "app.js"), []byte("console.log('widget');"), 0o644); err != nil {
		t.Fatalf("write app.js failed: %v", err)
	}

	r := gin.New()
	h := handler.NewHTTPHandler(&grpcclient.Clients{}, time.Second)
	mountWidgetRoute(r, zap.NewNop(), h, tmpDir)

	req := httptest.NewRequest(http.MethodGet, "/app/widget/app.js", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if body := rr.Body.String(); body != "console.log('widget');" {
		t.Fatalf("unexpected asset body: %q", body)
	}
}

func TestMountWidgetRouteDispatchesRootToWidgetHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmpDir, "index.html"), []byte("<!doctype html><html><body>widget</body></html>"), 0o644); err != nil {
		t.Fatalf("write index.html failed: %v", err)
	}

	r := gin.New()
	h := handler.NewHTTPHandler(&grpcclient.Clients{}, time.Second)
	mountWidgetRoute(r, zap.NewNop(), h, tmpDir)

	req := httptest.NewRequest(http.MethodGet, "/app/widget/", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}
