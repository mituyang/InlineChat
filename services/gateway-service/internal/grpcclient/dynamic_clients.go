package grpcclient

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
	authv1 "inlinechat/services/gateway-service/internal/gen/authv1"
	chatv1 "inlinechat/services/gateway-service/internal/gen/chatv1"
)

type endpointResolver interface {
	Resolve(ctx context.Context, serviceName string, protocol string) (string, error)
}

type serviceConn struct {
	resolver        endpointResolver
	serviceName     string
	protocol        string
	dialTimeout     time.Duration
	resolveTimeout  time.Duration
	refreshInterval time.Duration

	mu             sync.RWMutex
	reconnectMu    sync.Mutex
	conn           *grpc.ClientConn
	target         string
	lastResolvedAt time.Time
}

func NewDynamic(
	resolver endpointResolver,
	chatServiceName string,
	authServiceName string,
	adminServiceName string,
	dialTimeout time.Duration,
) (*Clients, error) {
	if resolver == nil {
		return nil, fmt.Errorf("resolver is required")
	}
	chatServiceName = strings.TrimSpace(chatServiceName)
	authServiceName = strings.TrimSpace(authServiceName)
	adminServiceName = strings.TrimSpace(adminServiceName)
	if chatServiceName == "" || authServiceName == "" || adminServiceName == "" {
		return nil, fmt.Errorf("chat auth admin service names are required")
	}
	if dialTimeout <= 0 {
		dialTimeout = 8 * time.Second
	}

	chatConn, err := newServiceConn(resolver, chatServiceName, "grpc", dialTimeout)
	if err != nil {
		return nil, err
	}
	authConn, err := newServiceConn(resolver, authServiceName, "grpc", dialTimeout)
	if err != nil {
		_ = chatConn.Close()
		return nil, err
	}
	adminConn, err := newServiceConn(resolver, adminServiceName, "grpc", dialTimeout)
	if err != nil {
		_ = chatConn.Close()
		_ = authConn.Close()
		return nil, err
	}

	return &Clients{
		Chat:  &dynamicChatClient{conn: chatConn},
		Auth:  &dynamicAuthClient{conn: authConn},
		Admin: &dynamicAdminClient{conn: adminConn},

		chatConnManager:  chatConn,
		authConnManager:  authConn,
		adminConnManager: adminConn,
	}, nil
}

func newServiceConn(resolver endpointResolver, serviceName string, protocol string, dialTimeout time.Duration) (*serviceConn, error) {
	sc := &serviceConn{
		resolver:        resolver,
		serviceName:     serviceName,
		protocol:        protocol,
		dialTimeout:     dialTimeout,
		resolveTimeout:  2 * time.Second,
		refreshInterval: 5 * time.Second,
	}
	if err := sc.refresh(context.Background(), true); err != nil {
		return nil, err
	}
	return sc, nil
}

func (s *serviceConn) Close() error {
	s.mu.Lock()
	conn := s.conn
	s.conn = nil
	s.target = ""
	s.lastResolvedAt = time.Time{}
	s.mu.Unlock()

	if conn == nil {
		return nil
	}
	return conn.Close()
}

func (s *serviceConn) currentConn() *grpc.ClientConn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.conn
}

func (s *serviceConn) connForCall(ctx context.Context) (*grpc.ClientConn, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	existing := s.currentConn()
	if err := s.refresh(ctx, false); err != nil {
		if existing != nil {
			return existing, nil
		}
		return nil, err
	}

	conn := s.currentConn()
	if conn == nil {
		return nil, fmt.Errorf("%s grpc connection unavailable", s.serviceName)
	}
	return conn, nil
}

func (s *serviceConn) refresh(ctx context.Context, force bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.reconnectMu.Lock()
	defer s.reconnectMu.Unlock()

	if !force {
		s.mu.RLock()
		existingConn := s.conn
		lastResolvedAt := s.lastResolvedAt
		s.mu.RUnlock()
		if existingConn != nil && time.Since(lastResolvedAt) < s.refreshInterval {
			return nil
		}
	}

	resolveCtx := ctx
	cancel := func() {}
	if s.resolveTimeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, s.resolveTimeout)
	}
	target, err := s.resolver.Resolve(resolveCtx, s.serviceName, s.protocol)
	cancel()
	if err != nil {
		return err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("resolved empty target for %s/%s", s.serviceName, s.protocol)
	}

	s.mu.RLock()
	currentTarget := s.target
	currentConn := s.conn
	s.mu.RUnlock()

	if !force && currentConn != nil && currentTarget == target {
		s.mu.Lock()
		s.lastResolvedAt = time.Now()
		s.mu.Unlock()
		return nil
	}

	nextConn, err := dial(target, s.dialTimeout)
	if err != nil {
		return err
	}

	s.mu.Lock()
	prevConn := s.conn
	s.conn = nextConn
	s.target = target
	s.lastResolvedAt = time.Now()
	s.mu.Unlock()

	if prevConn != nil {
		_ = prevConn.Close()
	}
	return nil
}

func invokeWithRetry[C any, R any](
	ctx context.Context,
	connManager *serviceConn,
	buildClient func(conn grpc.ClientConnInterface) C,
	invoke func(client C) (R, error),
) (R, error) {
	var zero R

	conn, err := connManager.connForCall(ctx)
	if err != nil {
		return zero, err
	}

	client := buildClient(conn)
	resp, err := invoke(client)
	if err == nil {
		return resp, nil
	}
	if !isRetryableRPCError(err) {
		return zero, err
	}
	if refreshErr := connManager.refresh(ctx, true); refreshErr != nil {
		return zero, err
	}

	conn = connManager.currentConn()
	if conn == nil {
		return zero, err
	}
	client = buildClient(conn)
	return invoke(client)
}

func isRetryableRPCError(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code() == codes.Unavailable || st.Code() == codes.DeadlineExceeded
}

type dynamicChatClient struct {
	conn *serviceConn
}

func (c *dynamicChatClient) CreateConversation(ctx context.Context, in *chatv1.CreateConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.Conversation, error) {
		return client.CreateConversation(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) ListConversations(ctx context.Context, in *chatv1.ListConversationsRequest, opts ...grpc.CallOption) (*chatv1.ListConversationsResponse, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.ListConversationsResponse, error) {
		return client.ListConversations(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) GetConversation(ctx context.Context, in *chatv1.GetConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.Conversation, error) {
		return client.GetConversation(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) CreateMessage(ctx context.Context, in *chatv1.CreateMessageRequest, opts ...grpc.CallOption) (*chatv1.Message, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.Message, error) {
		return client.CreateMessage(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) ListMessages(ctx context.Context, in *chatv1.ListMessagesRequest, opts ...grpc.CallOption) (*chatv1.ListMessagesResponse, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.ListMessagesResponse, error) {
		return client.ListMessages(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) MarkMessagesRead(ctx context.Context, in *chatv1.MarkMessagesReadRequest, opts ...grpc.CallOption) (*chatv1.MarkMessagesReadResponse, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.MarkMessagesReadResponse, error) {
		return client.MarkMessagesRead(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) ClaimConversation(ctx context.Context, in *chatv1.ClaimConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.Conversation, error) {
		return client.ClaimConversation(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) TransferConversation(ctx context.Context, in *chatv1.TransferConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.Conversation, error) {
		return client.TransferConversation(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) ConfirmTransferConversation(ctx context.Context, in *chatv1.ConfirmTransferConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.Conversation, error) {
		return client.ConfirmTransferConversation(ctx, in, opts...)
	})
}

func (c *dynamicChatClient) CloseConversation(ctx context.Context, in *chatv1.CloseConversationRequest, opts ...grpc.CallOption) (*chatv1.Conversation, error) {
	return invokeWithRetry(ctx, c.conn, chatv1.NewChatGatewayServiceClient, func(client chatv1.ChatGatewayServiceClient) (*chatv1.Conversation, error) {
		return client.CloseConversation(ctx, in, opts...)
	})
}

type dynamicAuthClient struct {
	conn *serviceConn
}

func (c *dynamicAuthClient) Login(ctx context.Context, in *authv1.LoginRequest, opts ...grpc.CallOption) (*authv1.AuthResult, error) {
	return invokeWithRetry(ctx, c.conn, authv1.NewAuthGatewayServiceClient, func(client authv1.AuthGatewayServiceClient) (*authv1.AuthResult, error) {
		return client.Login(ctx, in, opts...)
	})
}

func (c *dynamicAuthClient) Me(ctx context.Context, in *authv1.MeRequest, opts ...grpc.CallOption) (*authv1.MeResponse, error) {
	return invokeWithRetry(ctx, c.conn, authv1.NewAuthGatewayServiceClient, func(client authv1.AuthGatewayServiceClient) (*authv1.MeResponse, error) {
		return client.Me(ctx, in, opts...)
	})
}

type dynamicAdminClient struct {
	conn *serviceConn
}

func (c *dynamicAdminClient) CreateSite(ctx context.Context, in *adminv1.CreateSiteRequest, opts ...grpc.CallOption) (*adminv1.Site, error) {
	return invokeWithRetry(ctx, c.conn, adminv1.NewAdminGatewayServiceClient, func(client adminv1.AdminGatewayServiceClient) (*adminv1.Site, error) {
		return client.CreateSite(ctx, in, opts...)
	})
}

func (c *dynamicAdminClient) ListSites(ctx context.Context, in *adminv1.ListSitesRequest, opts ...grpc.CallOption) (*adminv1.ListSitesResponse, error) {
	return invokeWithRetry(ctx, c.conn, adminv1.NewAdminGatewayServiceClient, func(client adminv1.AdminGatewayServiceClient) (*adminv1.ListSitesResponse, error) {
		return client.ListSites(ctx, in, opts...)
	})
}

func (c *dynamicAdminClient) GetSiteBySiteID(ctx context.Context, in *adminv1.GetSiteBySiteIDRequest, opts ...grpc.CallOption) (*adminv1.Site, error) {
	return invokeWithRetry(ctx, c.conn, adminv1.NewAdminGatewayServiceClient, func(client adminv1.AdminGatewayServiceClient) (*adminv1.Site, error) {
		return client.GetSiteBySiteID(ctx, in, opts...)
	})
}

func (c *dynamicAdminClient) GetSiteByDomain(ctx context.Context, in *adminv1.GetSiteByDomainRequest, opts ...grpc.CallOption) (*adminv1.Site, error) {
	return invokeWithRetry(ctx, c.conn, adminv1.NewAdminGatewayServiceClient, func(client adminv1.AdminGatewayServiceClient) (*adminv1.Site, error) {
		return client.GetSiteByDomain(ctx, in, opts...)
	})
}

func (c *dynamicAdminClient) CreateAgent(ctx context.Context, in *adminv1.CreateAgentRequest, opts ...grpc.CallOption) (*adminv1.Agent, error) {
	return invokeWithRetry(ctx, c.conn, adminv1.NewAdminGatewayServiceClient, func(client adminv1.AdminGatewayServiceClient) (*adminv1.Agent, error) {
		return client.CreateAgent(ctx, in, opts...)
	})
}

func (c *dynamicAdminClient) ListAgents(ctx context.Context, in *adminv1.ListAgentsRequest, opts ...grpc.CallOption) (*adminv1.ListAgentsResponse, error) {
	return invokeWithRetry(ctx, c.conn, adminv1.NewAdminGatewayServiceClient, func(client adminv1.AdminGatewayServiceClient) (*adminv1.ListAgentsResponse, error) {
		return client.ListAgents(ctx, in, opts...)
	})
}
