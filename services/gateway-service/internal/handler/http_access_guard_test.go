package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

type chatAccessStub struct {
	chatv1.ChatGatewayServiceClient
	getConversationFn      func(ctx context.Context, in *chatv1.GetConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error)
	listMessagesFn         func(ctx context.Context, in *chatv1.ListMessagesRequest, opts ...grpc.CallOption) (*chatv1.ListMessagesResponse, error)
	createMessageFn        func(ctx context.Context, in *chatv1.CreateMessageRequest, opts ...grpc.CallOption) (*chatv1.Message, error)
	claimConversationFn    func(ctx context.Context, in *chatv1.ClaimConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error)
	transferConversationFn func(ctx context.Context, in *chatv1.TransferConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error)
	markMessagesReadFn     func(ctx context.Context, in *chatv1.MarkMessagesReadRequest, opts ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error)
}

type authAccessStub struct {
	authv1.AuthGatewayServiceClient
	meFn func(ctx context.Context, in *authv1.MeRequest, opts ...grpc.CallOption) (*authv1.MeResponse, error)
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

func (s *chatAccessStub) ClaimConversation(ctx context.Context, in *chatv1.ClaimConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	if s.claimConversationFn != nil {
		return s.claimConversationFn(ctx, in, opts...)
	}
	return &chatv1.Conversation{Id: in.GetConversationId(), Status: "open", AssignedAgentId: in.GetAgentId()}, nil
}

func (s *chatAccessStub) TransferConversation(ctx context.Context, in *chatv1.TransferConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	if s.transferConversationFn != nil {
		return s.transferConversationFn(ctx, in, opts...)
	}
	return &chatv1.Conversation{Id: in.GetConversationId(), Status: "open", AssignedAgentId: in.GetToAgentId()}, nil
}

func (s *chatAccessStub) MarkMessagesRead(ctx context.Context, in *chatv1.MarkMessagesReadRequest, opts ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
	if s.markMessagesReadFn != nil {
		return s.markMessagesReadFn(ctx, in, opts...)
	}
	return &chatv1.MarkMessagesReadResponse{}, nil
}

func (s *authAccessStub) Me(ctx context.Context, in *authv1.MeRequest, opts ...grpc.CallOption) (*authv1.MeResponse, error) {
	if s.meFn != nil {
		return s.meFn(ctx, in, opts...)
	}
	return &authv1.MeResponse{AgentId: 7, Role: "agent", SiteId: "site_demo"}, nil
}

func newActiveSiteAdminStub() *adminClientStub {
	return &adminClientStub{
		getSiteBySiteIDFn: func(_ context.Context, in *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
			return &adminv1.Site{SiteId: in.GetSiteId(), Status: "active"}, nil
		},
	}
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
					SiteId:       "site_demo",
					VisitorToken: "vt_expected",
					Status:       "open",
				}, nil
			},
		},
		Admin: newActiveSiteAdminStub(),
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
		Admin: newActiveSiteAdminStub(),
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

func TestCreateMessageAgentRequiresClaimedConversation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	createCalled := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, _ *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: 1, SiteId: "site_demo", Status: "open", AssignedAgentId: 0}, nil
			},
			createMessageFn: func(_ context.Context, _ *chatv1.CreateMessageRequest, _ ...grpc.CallOption) (*chatv1.Message, error) {
				createCalled = true
				return &chatv1.Message{}, nil
			},
		},
		Auth: &authAccessStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return &authv1.MeResponse{AgentId: 7, Role: "agent", SiteId: "site_demo"}, nil
			},
		},
		Admin: newActiveSiteAdminStub(),
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"sender_type":"agent","content":"hello","client_msg_id":"a1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}
	if createCalled {
		t.Fatal("CreateMessage should not be called when conversation is unclaimed")
	}
}

func TestCreateMessageRejectsTooLongContent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	createCalled := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: in.GetId(), VisitorToken: "vt_1", Status: "open"}, nil
			},
			createMessageFn: func(_ context.Context, _ *chatv1.CreateMessageRequest, _ ...grpc.CallOption) (*chatv1.Message, error) {
				createCalled = true
				return &chatv1.Message{}, nil
			},
		},
		Auth:  &authAccessStub{},
		Admin: newActiveSiteAdminStub(),
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	longContent := strings.Repeat("a", maxMessageContentChars+1)
	body := []byte(`{"sender_type":"visitor","content":"` + longContent + `","client_msg_id":"c-too-long","visitor_token":"vt_1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusBadRequest, rr.Code, rr.Body.String())
	}
	if createCalled {
		t.Fatal("CreateMessage should not be called when content is too long")
	}
}

func TestClaimConversationTriggersMarkRead(t *testing.T) {
	gin.SetMode(gin.TestMode)

	markCalled := false
	var markReq *chatv1.MarkMessagesReadRequest
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: in.GetId(), SiteId: "site_demo", Status: "open"}, nil
			},
			claimConversationFn: func(_ context.Context, in *chatv1.ClaimConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: in.GetConversationId(), SiteId: "site_demo", Status: "open", AssignedAgentId: in.GetAgentId()}, nil
			},
			listMessagesFn: func(_ context.Context, _ *chatv1.ListMessagesRequest, _ ...grpc.CallOption) (*chatv1.ListMessagesResponse, error) {
				return &chatv1.ListMessagesResponse{
					Items: []*chatv1.Message{
						{Id: 12, ConversationId: 1, SenderType: "visitor", Status: "sent"},
					},
				}, nil
			},
			markMessagesReadFn: func(_ context.Context, in *chatv1.MarkMessagesReadRequest, _ ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
				markCalled = true
				markReq = in
				return &chatv1.MarkMessagesReadResponse{UpdatedCount: 1}, nil
			},
		},
		Auth: &authAccessStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return &authv1.MeResponse{AgentId: 7, Role: "agent", SiteId: "site_demo"}, nil
			},
		},
		Admin: newActiveSiteAdminStub(),
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/1/claim", nil)
	req.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusOK, rr.Code, rr.Body.String())
	}
	if !markCalled {
		t.Fatal("MarkMessagesRead should be called after claim")
	}
	if markReq.GetConversationId() != 1 || markReq.GetLastReadMessageId() != 12 || markReq.GetActorAgentId() != 7 || markReq.GetActorType() != "agent" {
		t.Fatalf("unexpected MarkMessagesRead request: %+v", markReq)
	}
}

func TestGetConversationRejectsUnavailableSite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, _ *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{
					Id:           1,
					SiteId:       "site_missing",
					VisitorToken: "vt_expected",
					Status:       "open",
				}, nil
			},
		},
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, _ *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return nil, status.Error(codes.NotFound, "site not found")
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations/1?visitor_token=vt_expected", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}
}

func TestGetConversationAgentRejectsDifferentSite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, _ *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{
					Id:              1,
					SiteId:          "site_other",
					Status:          "open",
					AssignedAgentId: 7,
				}, nil
			},
		},
		Auth: &authAccessStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return &authv1.MeResponse{AgentId: 7, Role: "agent", SiteId: "site_demo"}, nil
			},
		},
		Admin: newActiveSiteAdminStub(),
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/api/chat/v1/conversations/1", nil)
	req.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, rr.Code, rr.Body.String())
	}
}

func TestTransferConversationRejectsTargetAgentFromDifferentSite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	transferCalled := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, in *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{
					Id:              in.GetId(),
					SiteId:          "site_demo",
					Status:          "open",
					AssignedAgentId: 7,
				}, nil
			},
			transferConversationFn: func(_ context.Context, _ *chatv1.TransferConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				transferCalled = true
				return &chatv1.Conversation{}, nil
			},
		},
		Auth: &authAccessStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return &authv1.MeResponse{AgentId: 7, Role: "agent", SiteId: "site_demo"}, nil
			},
		},
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, in *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return &adminv1.Site{SiteId: in.GetSiteId(), Status: "active"}, nil
			},
			getAgentByIDFn: func(_ context.Context, in *adminv1.GetAgentByIDRequest, _ ...grpc.CallOption) (*adminv1.Agent, error) {
				return &adminv1.Agent{Id: in.GetAgentId(), SiteId: "site_other", Status: "active"}, nil
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"to_agent_id":8}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/1/transfer", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer token")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusForbidden, rr.Code, rr.Body.String())
	}
	if transferCalled {
		t.Fatal("TransferConversation should not be called when target agent is outside current site")
	}
}

func TestCreateMessageAgentRejectsInactiveSite(t *testing.T) {
	gin.SetMode(gin.TestMode)

	createCalled := false
	h := NewHTTPHandler(&grpcclient.Clients{
		Chat: &chatAccessStub{
			getConversationFn: func(_ context.Context, _ *chatv1.GetConversationRequest, _ ...grpc.CallOption) (*chatv1.Conversation, error) {
				return &chatv1.Conversation{Id: 1, SiteId: "site_demo", Status: "open", AssignedAgentId: 7}, nil
			},
			createMessageFn: func(_ context.Context, _ *chatv1.CreateMessageRequest, _ ...grpc.CallOption) (*chatv1.Message, error) {
				createCalled = true
				return &chatv1.Message{}, nil
			},
		},
		Auth: &authAccessStub{
			meFn: func(_ context.Context, _ *authv1.MeRequest, _ ...grpc.CallOption) (*authv1.MeResponse, error) {
				return &authv1.MeResponse{AgentId: 7, Role: "agent", SiteId: "site_demo"}, nil
			},
		},
		Admin: &adminClientStub{
			getSiteBySiteIDFn: func(_ context.Context, in *adminv1.GetSiteBySiteIDRequest, _ ...grpc.CallOption) (*adminv1.Site, error) {
				return &adminv1.Site{SiteId: in.GetSiteId(), Status: "disabled"}, nil
			},
		},
	}, time.Second)
	r := gin.New()
	h.RegisterRoutes(r)

	body := []byte(`{"sender_type":"agent","content":"hello","client_msg_id":"a1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/v1/conversations/1/messages", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d body=%s", http.StatusConflict, rr.Code, rr.Body.String())
	}
	if createCalled {
		t.Fatal("CreateMessage should not be called when site is inactive")
	}
}
