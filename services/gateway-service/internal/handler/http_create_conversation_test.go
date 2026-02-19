package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	authv1 "inlinechat/services/gateway-service/internal/gen/authv1"
	chatv1 "inlinechat/services/gateway-service/internal/gen/chatv1"
	"inlinechat/services/gateway-service/internal/grpcclient"
)

type adminClientStub struct {
	adminv1.AdminGatewayServiceClient
	getSiteBySiteIDFn func(ctx context.Context, in *adminv1.GetSiteBySiteIDRequest, opts ...grpc.CallOption) (*adminv1.Site, error)
}

func (s *adminClientStub) GetSiteBySiteID(ctx context.Context, in *adminv1.GetSiteBySiteIDRequest, opts ...grpc.CallOption) (*adminv1.Site, error) {
	if s.getSiteBySiteIDFn != nil {
		return s.getSiteBySiteIDFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

type chatClientStub struct {
	chatv1.ChatGatewayServiceClient
	createConversationFn func(ctx context.Context, in *chatv1.CreateConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error)
}

func (s *chatClientStub) CreateConversation(ctx context.Context, in *chatv1.CreateConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	if s.createConversationFn != nil {
		return s.createConversationFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestCreateConversationSuccessReturnPublicPayload(t *testing.T) {
	gin.SetMode(gin.TestMode)

	adminCalls := 0
	chatCalls := 0

	h := NewHTTPHandler(&grpcclient.Clients{
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, in *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				adminCalls++
				if in.GetSiteId() != "site_demo" {
					t.Fatalf("unexpected site_id: %s", in.GetSiteId())
				}
				return &adminv1.Site{SiteId: in.GetSiteId(), Status: "active"}, nil
			},
		},
		Chat: &chatClientStub{
			createConversationFn: func(_ context.Context, in *chatv1.CreateConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				chatCalls++
				if in.GetVisitorToken() != "visitor_xxx" {
					t.Fatalf("unexpected visitor_token: %s", in.GetVisitorToken())
				}
				return &chatv1.Conversation{
					Id:           101,
					SiteId:       "site_demo",
					VisitorToken: "visitor_xxx",
					Status:       "open",
					CreatedAt:    "2026-02-19T10:00:00Z",
					UpdatedAt:    "2026-02-19T10:00:00Z",
				}, nil
			},
		},
		Auth: authv1.NewAuthGatewayServiceClient(nil),
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"site_id":"site_demo","visitor_token":"visitor_xxx"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusCreated {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusCreated, rr.Code, rr.Body.String())
	}
	if adminCalls != 1 || chatCalls != 1 {
		t.Fatalf("unexpected upstream calls admin=%d chat=%d", adminCalls, chatCalls)
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}

	if _, ok := resp["site_id"]; ok {
		t.Fatalf("public payload should not expose site_id: %v", resp)
	}
	if _, ok := resp["visitor_token"]; ok {
		t.Fatalf("public payload should not expose visitor_token: %v", resp)
	}
	if resp["id"] == nil || resp["status"] == nil {
		t.Fatalf("public payload missing required fields: %v", resp)
	}
}

func TestCreateConversationInvalidSiteID(t *testing.T) {
	gin.SetMode(gin.TestMode)

	chatCalls := 0
	h := NewHTTPHandler(&grpcclient.Clients{
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, _ *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return nil, status.Error(codes.NotFound, "site not found")
			},
		},
		Chat: &chatClientStub{
			createConversationFn: func(_ context.Context, _ *chatv1.CreateConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				chatCalls++
				return &chatv1.Conversation{}, nil
			},
		},
		Auth: authv1.NewAuthGatewayServiceClient(nil),
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"site_id":"missing","visitor_token":"visitor_xxx"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
	if chatCalls != 0 {
		t.Fatalf("chat create should not be called, got %d", chatCalls)
	}
}

func TestCreateConversationInactiveSite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	chatCalls := 0
	h := NewHTTPHandler(&grpcclient.Clients{
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, _ *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return &adminv1.Site{SiteId: "site_demo", Status: "disabled"}, nil
			},
		},
		Chat: &chatClientStub{
			createConversationFn: func(_ context.Context, _ *chatv1.CreateConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				chatCalls++
				return &chatv1.Conversation{}, nil
			},
		},
		Auth: authv1.NewAuthGatewayServiceClient(nil),
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"site_id":"site_demo","visitor_token":"visitor_xxx"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}
	if chatCalls != 0 {
		t.Fatalf("chat create should not be called, got %d", chatCalls)
	}
}
