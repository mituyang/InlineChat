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

type chatReadClientStub struct {
	chatv1.ChatGatewayServiceClient
	getConversationFn  func(ctx context.Context, in *chatv1.GetConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error)
	markMessagesReadFn func(ctx context.Context, in *chatv1.MarkMessagesReadRequest, opts ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error)
}

func (s *chatReadClientStub) GetConversation(ctx context.Context, in *chatv1.GetConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	if s.getConversationFn != nil {
		return s.getConversationFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (s *chatReadClientStub) MarkMessagesRead(ctx context.Context, in *chatv1.MarkMessagesReadRequest, opts ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
	if s.markMessagesReadFn != nil {
		return s.markMessagesReadFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

type authMeClientStub struct {
	authv1.AuthGatewayServiceClient
	meFn func(ctx context.Context, in *authv1.MeRequest, opts ...grpc.CallOption) (*authv1.MeResponse, error)
}

func (s *authMeClientStub) Me(ctx context.Context, in *authv1.MeRequest, opts ...grpc.CallOption) (*authv1.MeResponse, error) {
	if s.meFn != nil {
		return s.meFn(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func TestMarkMessagesReadAgentPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotReq *chatv1.MarkMessagesReadRequest
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatReadClientStub{
			getConversationFn: func(_ context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: in.GetId(), SiteId: "site_demo", AssignedAgentId: 7}, nil
			},
			markMessagesReadFn: func(_ context.Context, in *chatv1.MarkMessagesReadRequest, _ ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
				gotReq = in
				return &chatv1.MarkMessagesReadResponse{UpdatedCount: 2}, nil
			},
		},
		Auth: &authMeClientStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return &authv1.MeResponse{AgentId: 7, Role: "agent"}, nil
			},
		},
		Admin: newActiveSiteAdminStub(),
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"last_read_message_id":88}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/12/read", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if gotReq == nil {
		t.Fatal("MarkMessagesRead not called")
	}
	if gotReq.GetActorType() != "agent" || gotReq.GetActorAgentId() != 7 {
		t.Fatalf("unexpected actor info: %+v", gotReq)
	}
	if gotReq.GetVisitorToken() != "" {
		t.Fatalf("visitor_token should be empty in agent path: %+v", gotReq)
	}
}

func TestMarkMessagesReadVisitorPath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	var gotReq *chatv1.MarkMessagesReadRequest
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatReadClientStub{
			getConversationFn: func(_ context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: in.GetId(), SiteId: "site_demo", VisitorToken: "vt_abc", Status: "open"}, nil
			},
			markMessagesReadFn: func(_ context.Context, in *chatv1.MarkMessagesReadRequest, _ ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
				gotReq = in
				return &chatv1.MarkMessagesReadResponse{UpdatedCount: 1}, nil
			},
		},
		Auth:  &authMeClientStub{},
		Admin: newActiveSiteAdminStub(),
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"last_read_message_id":10,"visitor_token":"vt_abc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/9/read", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if gotReq == nil {
		t.Fatal("MarkMessagesRead not called")
	}
	if gotReq.GetActorType() != "visitor" || gotReq.GetActorAgentId() != 0 {
		t.Fatalf("unexpected actor info: %+v", gotReq)
	}
	if gotReq.GetVisitorToken() != "vt_abc" {
		t.Fatalf("unexpected visitor token: %+v", gotReq)
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if resp["updated_count"] == nil {
		t.Fatalf("missing updated_count: %v", resp)
	}
}

func TestMarkMessagesReadAgentRequiresAssignedConversation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	called := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatReadClientStub{
			getConversationFn: func(_ context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: in.GetId(), SiteId: "site_demo", AssignedAgentId: 9}, nil
			},
			markMessagesReadFn: func(_ context.Context, in *chatv1.MarkMessagesReadRequest, _ ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
				called = true
				return &chatv1.MarkMessagesReadResponse{UpdatedCount: 1}, nil
			},
		},
		Auth: &authMeClientStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return &authv1.MeResponse{AgentId: 7, Role: "agent"}, nil
			},
		},
		Admin: newActiveSiteAdminStub(),
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"last_read_message_id":18}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/12/read", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("MarkMessagesRead should not be called when conversation is not assigned to actor")
	}
}

func TestMarkMessagesReadMissingVisitorToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{Chat: &chatReadClientStub{}, Auth: &authMeClientStub{}}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"last_read_message_id":10}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/9/read", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
}

func TestMarkMessagesReadAgentAuthFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatReadClientStub{},
		Auth: &authMeClientStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return nil, status.Error(codes.Unauthenticated, "invalid token")
			},
		},
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"last_read_message_id":8}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/9/read", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer bad")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusUnauthorized, rr.Code, rr.Body.String())
	}
}

func TestMarkMessagesReadVisitorRejectsUnavailableSite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	called := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatReadClientStub{
			getConversationFn: func(_ context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: in.GetId(), SiteId: "site_missing", VisitorToken: "vt_abc", Status: "open"}, nil
			},
			markMessagesReadFn: func(_ context.Context, _ *chatv1.MarkMessagesReadRequest, _ ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
				called = true
				return &chatv1.MarkMessagesReadResponse{UpdatedCount: 1}, nil
			},
		},
		Auth: &authMeClientStub{},
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, _ *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return nil, status.Error(codes.NotFound, "site not found")
			},
		},
	}, time.Second)

	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"last_read_message_id":10,"visitor_token":"vt_abc"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/9/read", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("MarkMessagesRead should not be called when site is unavailable")
	}
}
