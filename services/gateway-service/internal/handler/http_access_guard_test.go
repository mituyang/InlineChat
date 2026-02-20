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

	chatv1 "inlinechat/services/gateway-service/internal/gen/chatv1"
	"inlinechat/services/gateway-service/internal/grpcclient"
)

type chatAccessStub struct {
	chatv1.ChatGatewayServiceClient
	getConversationFn func(ctx context.Context, in *chatv1.GetConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error)
	listMessagesFn    func(ctx context.Context, in *chatv1.ListMessagesRequest, opts ...grpc.CallOption) (*chatv1.ListMessagesResponse, error)
	createMessageFn   func(ctx context.Context, in *chatv1.CreateMessageRequest, opts ...grpc.CallOption) (*chatv1.Message, error)
}

func (s *chatAccessStub) GetConversation(ctx context.Context, in *chatv1.GetConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	if s.getConversationFn != nil {
		return s.getConversationFn(ctx, in, opts...)
	}
	return &chatv1.Conversation{}, nil
}

func (s *chatAccessStub) ListMessages(ctx context.Context, in *chatv1.ListMessagesRequest, opts ...grpc.CallOption) (*chatv1.ListMessagesResponse, error) {
	if s.listMessagesFn != nil {
		return s.listMessagesFn(ctx, in, opts...)
	}
	return &chatv1.ListMessagesResponse{}, nil
}

func (s *chatAccessStub) CreateMessage(ctx context.Context, in *chatv1.CreateMessageRequest, opts ...grpc.CallOption) (*chatv1.Message, error) {
	if s.createMessageFn != nil {
		return s.createMessageFn(ctx, in, opts...)
	}
	return &chatv1.Message{}, nil
}

func TestGetConversationRequiresVisitorTokenWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	called := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, _ *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				called = true
				return &chatv1.Conversation{}, nil
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations/1", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
	if called {
		t.Fatal("GetConversation should not be called when visitor_token is missing")
	}
}

func TestGetConversationVisitorTokenMismatch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, _ *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{
					Id:           1,
					VisitorToken: "vt_expected",
					Status:       "open",
				}, nil
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations/1?visitor_token=vt_wrong", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, rr.Code, rr.Body.String())
	}
}

func TestGetConversationVisitorResponseNoVisitorToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, _ *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{
					Id:           1,
					SiteId:       "site_demo",
					VisitorToken: "vt_expected",
					Status:       "open",
					CreatedAt:    "2026-02-20T00:00:00Z",
					UpdatedAt:    "2026-02-20T00:00:00Z",
				}, nil
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations/1?visitor_token=vt_expected", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if _, ok := resp["visitor_token"]; ok {
		t.Fatalf("response should not expose visitor_token: %v", resp)
	}
}

func TestListMessagesRequiresVisitorTokenWithoutAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)

	listCalled := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			listMessagesFn: func(_ context.Context, _ *chatv1.ListMessagesRequest, _ ...grpc.CallOption) (*chatv1.ListMessagesResponse, error) {
				listCalled = true
				return &chatv1.ListMessagesResponse{}, nil
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations/1/messages", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
	if listCalled {
		t.Fatal("ListMessages should not be called when visitor_token is missing")
	}
}

func TestCreateMessageVisitorRequiresToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	createCalled := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			createMessageFn: func(_ context.Context, _ *chatv1.CreateMessageRequest, _ ...grpc.CallOption) (*chatv1.Message, error) {
				createCalled = true
				return &chatv1.Message{}, nil
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"sender_type":"visitor","content":"hello","client_msg_id":"c1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
	if createCalled {
		t.Fatal("CreateMessage should not be called when visitor_token is missing")
	}
}
