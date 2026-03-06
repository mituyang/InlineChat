package chatclient

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type endpointResolver interface {
	Resolve(ctx context.Context, serviceName string, protocol string) (string, error)
}

type DynamicClient struct {
	resolver        endpointResolver
	serviceName     string
	protocol        string
	dialTimeout     time.Duration
	resolveTimeout  time.Duration
	refreshInterval time.Duration

	mu             sync.RWMutex
	reconnectMu    sync.Mutex
	client         *Client
	target         string
	lastResolvedAt time.Time
}

// NewDynamic 构建带服务发现能力的 chat gRPC 客户端。
func NewDynamic(resolver endpointResolver, serviceName string, protocol string, dialTimeout time.Duration) (*DynamicClient, error) {
	if resolver == nil {
		return nil, fmt.Errorf("resolver is required")
	}
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil, fmt.Errorf("service_name is required")
	}
	protocol = strings.TrimSpace(protocol)
	if protocol == "" {
		protocol = "grpc"
	}
	if dialTimeout <= 0 {
		dialTimeout = 8 * time.Second
	}

	d := &DynamicClient{
		resolver:        resolver,
		serviceName:     serviceName,
		protocol:        protocol,
		dialTimeout:     dialTimeout,
		resolveTimeout:  2 * time.Second,
		refreshInterval: 5 * time.Second,
	}
	if err := d.refresh(context.Background(), true); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *DynamicClient) Target() string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.target
}

func (d *DynamicClient) Close() error {
	d.mu.Lock()
	current := d.client
	d.client = nil
	d.target = ""
	d.lastResolvedAt = time.Time{}
	d.mu.Unlock()

	if current == nil {
		return nil
	}
	return current.Close()
}

// CreateMessage 请求失败且可重试时，会刷新 endpoint 后重试一次。
func (d *DynamicClient) CreateMessage(ctx context.Context, conversationID uint64, reqBody CreateMessageRequest) (*Message, error) {
	client, err := d.clientForCall(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := client.CreateMessage(ctx, conversationID, reqBody)
	if err == nil {
		return resp, nil
	}
	if !isRetryableRPCError(err) {
		return nil, err
	}
	if reconnectErr := d.refresh(ctx, true); reconnectErr != nil {
		return nil, err
	}

	client = d.currentClient()
	if client == nil {
		return nil, err
	}
	return client.CreateMessage(ctx, conversationID, reqBody)
}

func (d *DynamicClient) GetConversation(ctx context.Context, conversationID uint64) (*Conversation, error) {
	client, err := d.clientForCall(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := client.GetConversation(ctx, conversationID)
	if err == nil {
		return resp, nil
	}
	if !isRetryableRPCError(err) {
		return nil, err
	}
	if reconnectErr := d.refresh(ctx, true); reconnectErr != nil {
		return nil, err
	}

	client = d.currentClient()
	if client == nil {
		return nil, err
	}
	return client.GetConversation(ctx, conversationID)
}

func (d *DynamicClient) ListMessages(ctx context.Context, conversationID uint64, in ListMessagesInput) ([]*Message, error) {
	client, err := d.clientForCall(ctx)
	if err != nil {
		return nil, err
	}

	resp, err := client.ListMessages(ctx, conversationID, in)
	if err == nil {
		return resp, nil
	}
	if !isRetryableRPCError(err) {
		return nil, err
	}
	if reconnectErr := d.refresh(ctx, true); reconnectErr != nil {
		return nil, err
	}

	client = d.currentClient()
	if client == nil {
		return nil, err
	}
	return client.ListMessages(ctx, conversationID, in)
}

func (d *DynamicClient) clientForCall(ctx context.Context) (*Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	existing := d.currentClient()
	if err := d.refresh(ctx, false); err != nil {
		if existing != nil {
			return existing, nil
		}
		return nil, err
	}

	client := d.currentClient()
	if client == nil {
		return nil, fmt.Errorf("chat grpc client is unavailable")
	}
	return client, nil
}

func (d *DynamicClient) currentClient() *Client {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.client
}

// refresh 解析最新 endpoint 并在变化时重建连接。
func (d *DynamicClient) refresh(ctx context.Context, force bool) error {
	if ctx == nil {
		ctx = context.Background()
	}

	d.reconnectMu.Lock()
	defer d.reconnectMu.Unlock()

	if !force {
		d.mu.RLock()
		current := d.client
		lastResolvedAt := d.lastResolvedAt
		d.mu.RUnlock()
		if current != nil && time.Since(lastResolvedAt) < d.refreshInterval {
			return nil
		}
	}

	resolveCtx := ctx
	cancel := func() {}
	if d.resolveTimeout > 0 {
		resolveCtx, cancel = context.WithTimeout(ctx, d.resolveTimeout)
	}
	target, err := d.resolver.Resolve(resolveCtx, d.serviceName, d.protocol)
	cancel()
	if err != nil {
		return err
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("resolved empty target for %s/%s", d.serviceName, d.protocol)
	}

	d.mu.RLock()
	currentTarget := d.target
	currentClient := d.client
	d.mu.RUnlock()
	if currentClient != nil && currentTarget == target {
		d.mu.Lock()
		d.lastResolvedAt = time.Now()
		d.mu.Unlock()
		return nil
	}

	nextClient, err := New(target, d.dialTimeout)
	if err != nil {
		return err
	}

	d.mu.Lock()
	prevClient := d.client
	d.client = nextClient
	d.target = target
	d.lastResolvedAt = time.Now()
	d.mu.Unlock()

	if prevClient != nil {
		_ = prevClient.Close()
	}
	return nil
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
