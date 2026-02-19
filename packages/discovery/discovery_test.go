package discovery

import (
	"context"
	"testing"
	"time"
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
