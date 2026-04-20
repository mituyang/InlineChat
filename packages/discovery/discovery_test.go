package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
)

func TestNormalizeEndpoints(t *testing.T) {
	got := normalizeEndpoints([]string{" 127.0.0.1:2379 ", "", "etcd:2379"})
	if len(got) != 2 {
		t.Fatalf("expected 2 endpoints, got %d (%v)", len(got), got)
	}
	if got[0] != "127.0.0.1:2379" || got[1] != "etcd:2379" {
		t.Fatalf("unexpected endpoints: %v", got)
	}
}

func TestNormalizePrefix(t *testing.T) {
	if got := normalizePrefix(" "); got != "/inlinechat/services" {
		t.Fatalf("unexpected default prefix: %s", got)
	}
	if got := normalizePrefix("inlinechat/services/"); got != "/inlinechat/services" {
		t.Fatalf("unexpected normalized prefix: %s", got)
	}
}

func TestNewResolverRejectInvalidInputs(t *testing.T) {
	if _, err := NewResolver(nil, 5*time.Second, "/inlinechat/services"); err == nil {
		t.Fatal("expected error for empty endpoints")
	}
	if _, err := NewResolver([]string{"127.0.0.1:2379"}, 0, "/inlinechat/services"); err == nil {
		t.Fatal("expected error for invalid timeout")
	}
}

func TestResolverResolveRejectInvalidArguments(t *testing.T) {
	resolver, err := NewResolver([]string{"127.0.0.1:2379"}, 100*time.Millisecond, "/inlinechat/services")
	if err != nil {
		t.Fatalf("NewResolver failed: %v", err)
	}
	defer func() { _ = resolver.Close() }()

	if _, err := resolver.Resolve(context.Background(), "", "grpc"); err == nil {
		t.Fatal("expected error for empty service name")
	}
	if _, err := resolver.Resolve(context.Background(), "chat-service", ""); err == nil {
		t.Fatal("expected error for empty protocol")
	}
}

func TestResolverResolveFromCacheRoundRobin(t *testing.T) {
	resolver := &Resolver{
		cacheTTL: 2 * time.Second,
		cache: map[string]cacheEntry{
			"chat-service/grpc": {
				endpoints: []string{"chat-a:8202", "chat-b:8202"},
				expiresAt: time.Now().Add(time.Second),
			},
		},
		rrState: make(map[string]int),
	}

	first, ok, err := resolver.resolveFromCache("chat-service/grpc")
	if !ok || err != nil {
		t.Fatalf("expected cache hit, ok=%v err=%v", ok, err)
	}
	second, ok, err := resolver.resolveFromCache("chat-service/grpc")
	if !ok || err != nil {
		t.Fatalf("expected cache hit, ok=%v err=%v", ok, err)
	}
	if first != "chat-a:8202" || second != "chat-b:8202" {
		t.Fatalf("unexpected round robin sequence: first=%s second=%s", first, second)
	}
}

func TestResolverResolveFromCacheEvictsExpiredEntry(t *testing.T) {
	resolver := &Resolver{
		cacheTTL: 2 * time.Second,
		cache: map[string]cacheEntry{
			"chat-service/grpc": {
				endpoints: []string{"chat-a:8202"},
				expiresAt: time.Now().Add(-time.Second),
			},
		},
		rrState: make(map[string]int),
	}

	_, ok, err := resolver.resolveFromCache("chat-service/grpc")
	if ok || err != nil {
		t.Fatalf("expected expired cache miss, ok=%v err=%v", ok, err)
	}
	if _, exists := resolver.cache["chat-service/grpc"]; exists {
		t.Fatal("expected expired cache entry removed")
	}
}

func TestRegisterRejectInvalidInputs(t *testing.T) {
	if _, err := Register(context.Background(), RegisterRequest{
		TTLSeconds:   0,
		DialTimeout:  5 * time.Second,
		ETCDEndpoint: []string{"127.0.0.1:2379"},
	}); err == nil {
		t.Fatal("expected error for invalid ttl")
	}

	if _, err := Register(context.Background(), RegisterRequest{
		TTLSeconds:   10,
		DialTimeout:  0,
		ETCDEndpoint: []string{"127.0.0.1:2379"},
	}); err == nil {
		t.Fatal("expected error for invalid dial timeout")
	}
}

type fakeResolver struct {
	calls       int
	failTimes   int
	resolveErr  error
	resolveResp string
}

func (r *fakeResolver) Resolve(_ context.Context, _ string, _ string) (string, error) {
	r.calls++
	if r.calls <= r.failTimes {
		if r.resolveErr != nil {
			return "", r.resolveErr
		}
		return "", errors.New("temporary unavailable")
	}
	return r.resolveResp, nil
}

func TestResolveWithRetrySuccessAfterRetry(t *testing.T) {
	resolver := &fakeResolver{
		failTimes:   2,
		resolveResp: "chat-service:8212",
	}

	got, err := resolveWithRetryWithPolicy(
		resolver,
		"chat-service",
		"grpc",
		200*time.Millisecond,
		10*time.Millisecond,
		5*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected success, got err=%v", err)
	}
	if got != "chat-service:8212" {
		t.Fatalf("unexpected target: %s", got)
	}
	if resolver.calls < 3 {
		t.Fatalf("expected at least 3 calls, got %d", resolver.calls)
	}
}

func TestResolveWithRetryTimeout(t *testing.T) {
	resolver := &fakeResolver{
		failTimes:  100,
		resolveErr: errors.New("upstream unavailable"),
	}

	_, err := resolveWithRetryWithPolicy(
		resolver,
		"chat-service",
		"grpc",
		40*time.Millisecond,
		10*time.Millisecond,
		5*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if resolver.calls == 0 {
		t.Fatal("expected resolver called at least once")
	}
}

func TestIsLeaseNotFound(t *testing.T) {
	if !isLeaseNotFound(rpctypes.ErrLeaseNotFound) {
		t.Fatal("expected lease not found recognized")
	}
	if isLeaseNotFound(errors.New("other error")) {
		t.Fatal("expected non-lease error ignored")
	}
}
