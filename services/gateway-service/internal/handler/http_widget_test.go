package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	authv1 "inlinechat/services/gateway-service/internal/gen/authv1"
	"inlinechat/services/gateway-service/internal/grpcclient"
)

func TestServeWidgetAppInjectsWidgetSession(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, in *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return &adminv1.Site{
					SiteId:    in.GetSiteId(),
					Domains:   []string{"shop.example.com"},
					WidgetKey: "wk_shop",
					Status:    "active",
				}, nil
			},
		},
		Auth: authv1.NewAuthGatewayServiceClient(nil),
	}, time.Second)
	h.SetWidgetIndexHTML([]byte(`<!doctype html><html><body><script src="/app/widget/app.js" defer></script></body></html>`))

	r := gin.New()
	r.GET("/app/widget/", h.ServeWidgetApp)

	req := httptest.NewRequest(http.MethodGet, "/app/widget/?site_id=site_demo&parent_origin=https://shop.example.com", nil)
	req.Header.Set("Referer", "https://shop.example.com/products")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, "__INLINECHAT_WIDGET_SESSION__") {
		t.Fatalf("expected injected widget session, got %s", body)
	}
	if !strings.Contains(body, "__INLINECHAT_WIDGET_SCOPE__") {
		t.Fatalf("expected injected widget scope, got %s", body)
	}
}

func TestServeWidgetAppRejectsMismatchedSource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, in *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return &adminv1.Site{
					SiteId:    in.GetSiteId(),
					Domains:   []string{"shop.example.com"},
					WidgetKey: "wk_shop",
					Status:    "active",
				}, nil
			},
		},
		Auth: authv1.NewAuthGatewayServiceClient(nil),
	}, time.Second)
	h.SetWidgetIndexHTML([]byte(`<!doctype html><html><body></body></html>`))

	r := gin.New()
	r.GET("/app/widget/", h.ServeWidgetApp)

	req := httptest.NewRequest(http.MethodGet, "/app/widget/?site_id=site_demo&parent_origin=https://shop.example.com", nil)
	req.Header.Set("Referer", "https://evil.example.net/hijack")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, rr.Code, rr.Body.String())
	}
}

func TestServeWidgetAppAllowsAnotherBoundDomain(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, in *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return &adminv1.Site{
					SiteId:    in.GetSiteId(),
					Domains:   []string{"shop.example.com", "help.example.com"},
					WidgetKey: "wk_shop",
					Status:    "active",
				}, nil
			},
		},
		Auth: authv1.NewAuthGatewayServiceClient(nil),
	}, time.Second)
	h.SetWidgetIndexHTML([]byte(`<!doctype html><html><body><script src="/app/widget/app.js" defer></script></body></html>`))

	r := gin.New()
	r.GET("/app/widget/", h.ServeWidgetApp)

	req := httptest.NewRequest(http.MethodGet, "/app/widget/?site_id=site_demo&parent_origin=https://help.example.com", nil)
	req.Header.Set("Referer", "https://help.example.com/docs")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}
}

func TestValidateWidgetSessionTokenRejectExpired(t *testing.T) {
	claims := widgetSessionClaims{
		SiteID:       "site_demo",
		SiteDomain:   "shop.example.com",
		ParentOrigin: "https://shop.example.com",
		StorageScope: "site_demo@shop.example.com",
		IssuedAt:     time.Now().Add(-2 * time.Hour).Unix(),
		ExpiresAt:    time.Now().Add(-1 * time.Hour).Unix(),
	}
	token, err := signWidgetSessionToken(claims, "wk_shop")
	if err != nil {
		t.Fatalf("sign widget session failed: %v", err)
	}

	if _, err := validateWidgetSessionToken(token, "wk_shop", time.Now()); err == nil {
		t.Fatal("expected expired widget session to fail")
	}
}

func TestValidateWidgetRequestSourceAllowsLocalhostAnyPort(t *testing.T) {
	parentOrigin, err := validateWidgetRequestSource(
		[]string{"localhost"},
		"http://localhost:8200",
		"http://localhost:3000/demo",
		"",
	)
	if err != nil {
		t.Fatalf("expected localhost any port to be allowed, got err=%v", err)
	}
	if parentOrigin != "http://localhost:8200" {
		t.Fatalf("unexpected parent origin: %s", parentOrigin)
	}
}

func TestMatchesSiteDomainKeepsNonLocalhostPortStrict(t *testing.T) {
	if matchesSiteDomain("demo.example.com", "https://demo.example.com:8200") {
		t.Fatal("non-localhost domain should not ignore custom port")
	}
}
