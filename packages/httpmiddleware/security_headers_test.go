package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeadersDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(SecurityHeaders(SecurityHeadersOptions{}))
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("unexpected X-Content-Type-Options: %q", got)
	}
	if got := rr.Header().Get("Referrer-Policy"); got != defaultReferrerPolicy {
		t.Fatalf("unexpected Referrer-Policy: %q", got)
	}
	if got := rr.Header().Get("Permissions-Policy"); got != defaultPermissionsPolicy {
		t.Fatalf("unexpected Permissions-Policy: %q", got)
	}
}

func TestSecurityHeadersDoesNotOverrideExisting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	r.Use(SecurityHeaders(SecurityHeadersOptions{}))
	r.GET("/healthz", func(c *gin.Context) {
		c.Header("Referrer-Policy", "same-origin")
		c.Header("Permissions-Policy", "geolocation=()")
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if got := rr.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("unexpected Referrer-Policy: %q", got)
	}
	if got := rr.Header().Get("Permissions-Policy"); got != "geolocation=()" {
		t.Fatalf("unexpected Permissions-Policy: %q", got)
	}
}
