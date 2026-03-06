package config

import (
	"strings"
	"testing"
)

func TestLoadSuccessUsesDefaultsAndFallbacks(t *testing.T) {
	setGatewayConfigEnv(t)
	t.Setenv("RATE_LIMIT_REDIS_ADDR", "")
	t.Setenv("REDIS_ADDR", "redis-fallback:6379")
	t.Setenv("RATE_LIMIT_REDIS_PASSWORD", "")
	t.Setenv("REDIS_PASSWORD", "fallback-secret")
	t.Setenv("RATE_LIMIT_REDIS_DB", "")
	t.Setenv("REDIS_DB", "7")
	t.Setenv("ETCD_ENDPOINTS", " etcd-a:2379 , etcd-b:2379 ")
	t.Setenv("GRPC_DIAL_TIMEOUT_SEC", "")
	t.Setenv("GRPC_CALL_TIMEOUT_SEC", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.RateLimitRedisAddr != "redis-fallback:6379" {
		t.Fatalf("expected redis addr fallback, got %q", cfg.RateLimitRedisAddr)
	}
	if cfg.RateLimitRedisPassword != "fallback-secret" {
		t.Fatalf("expected redis password fallback, got %q", cfg.RateLimitRedisPassword)
	}
	if cfg.RateLimitRedisDB != 7 {
		t.Fatalf("expected redis db fallback 7, got %d", cfg.RateLimitRedisDB)
	}
	if len(cfg.ETCDEndpoints) != 2 || cfg.ETCDEndpoints[0] != "etcd-a:2379" || cfg.ETCDEndpoints[1] != "etcd-b:2379" {
		t.Fatalf("unexpected etcd endpoints: %#v", cfg.ETCDEndpoints)
	}
	if cfg.GRPCDialTimeoutSec != 8 {
		t.Fatalf("expected GRPCDialTimeoutSec=8, got %d", cfg.GRPCDialTimeoutSec)
	}
	if cfg.GRPCCallTimeoutSec != 8 {
		t.Fatalf("expected GRPCCallTimeoutSec=8, got %d", cfg.GRPCCallTimeoutSec)
	}
}

func TestLoadValidationFailures(t *testing.T) {
	cases := []struct {
		name       string
		key        string
		value      string
		errMessage string
	}{
		{name: "missing etcd endpoints", key: "ETCD_ENDPOINTS", value: " , ", errMessage: "ETCD_ENDPOINTS is required"},
		{name: "invalid login rate", key: "LOGIN_RATE_LIMIT_PER_MIN", value: "0", errMessage: "LOGIN_RATE_LIMIT_PER_MIN must be greater than 0"},
		{name: "invalid login burst", key: "LOGIN_RATE_LIMIT_BURST", value: "0", errMessage: "LOGIN_RATE_LIMIT_BURST must be greater than 0"},
		{name: "invalid visitor rate", key: "VISITOR_RATE_LIMIT_PER_MIN", value: "0", errMessage: "VISITOR_RATE_LIMIT_PER_MIN must be greater than 0"},
		{name: "invalid visitor burst", key: "VISITOR_RATE_LIMIT_BURST", value: "0", errMessage: "VISITOR_RATE_LIMIT_BURST must be greater than 0"},
		{name: "invalid agent rate", key: "AGENT_RATE_LIMIT_PER_MIN", value: "0", errMessage: "AGENT_RATE_LIMIT_PER_MIN must be greater than 0"},
		{name: "invalid agent burst", key: "AGENT_RATE_LIMIT_BURST", value: "0", errMessage: "AGENT_RATE_LIMIT_BURST must be greater than 0"},
		{name: "invalid admin rate", key: "ADMIN_RATE_LIMIT_PER_MIN", value: "0", errMessage: "ADMIN_RATE_LIMIT_PER_MIN must be greater than 0"},
		{name: "invalid admin burst", key: "ADMIN_RATE_LIMIT_BURST", value: "0", errMessage: "ADMIN_RATE_LIMIT_BURST must be greater than 0"},
		{name: "invalid rate ttl", key: "RATE_LIMIT_KEY_TTL_MINS", value: "0", errMessage: "RATE_LIMIT_KEY_TTL_MINS must be greater than 0"},
		{name: "invalid redis timeout", key: "RATE_LIMIT_REDIS_TIMEOUT_MS", value: "0", errMessage: "RATE_LIMIT_REDIS_TIMEOUT_MS must be greater than 0"},
		{name: "invalid redis fail threshold", key: "RATE_LIMIT_REDIS_FAIL_THRESHOLD", value: "0", errMessage: "RATE_LIMIT_REDIS_FAIL_THRESHOLD must be greater than 0"},
		{name: "invalid redis circuit window", key: "RATE_LIMIT_REDIS_CIRCUIT_OPEN_SEC", value: "0", errMessage: "RATE_LIMIT_REDIS_CIRCUIT_OPEN_SEC must be greater than 0"},
		{name: "invalid etcd timeout", key: "ETCD_DIAL_TIMEOUT_SEC", value: "0", errMessage: "ETCD_DIAL_TIMEOUT_SEC must be greater than 0"},
		{name: "missing service name", key: "CHAT_SERVICE_NAME", value: "   ", errMessage: "CHAT_SERVICE_NAME AUTH_SERVICE_NAME ADMIN_SERVICE_NAME REALTIME_SERVICE_NAME are required"},
		{name: "invalid grpc dial timeout", key: "GRPC_DIAL_TIMEOUT_SEC", value: "0", errMessage: "GRPC_DIAL_TIMEOUT_SEC must be greater than 0"},
		{name: "invalid grpc call timeout", key: "GRPC_CALL_TIMEOUT_SEC", value: "0", errMessage: "GRPC_CALL_TIMEOUT_SEC must be greater than 0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setGatewayConfigEnv(t)
			t.Setenv(tc.key, tc.value)

			_, err := Load()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tc.errMessage) {
				t.Fatalf("expected error containing %q, got %q", tc.errMessage, err.Error())
			}
		})
	}
}

func TestHelpers(t *testing.T) {
	t.Setenv("UNIT_TEST_ENV_VALUE", "")
	if got := getEnv("UNIT_TEST_ENV_VALUE", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback value, got %q", got)
	}

	t.Setenv("UNIT_TEST_INT_VALUE", "bad")
	if got := getIntEnv("UNIT_TEST_INT_VALUE", 42); got != 42 {
		t.Fatalf("expected int fallback 42, got %d", got)
	}

	parts := splitAndTrim(" a, ,b ,, c ")
	if len(parts) != 3 || parts[0] != "a" || parts[1] != "b" || parts[2] != "c" {
		t.Fatalf("unexpected splitAndTrim result: %#v", parts)
	}
}

func setGatewayConfigEnv(t *testing.T) {
	t.Helper()

	t.Setenv("HTTP_PORT", "8200")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("REQUEST_ID_HEADER", "X-Request-ID")
	t.Setenv("LOGIN_RATE_LIMIT_PER_MIN", "60")
	t.Setenv("LOGIN_RATE_LIMIT_BURST", "20")
	t.Setenv("VISITOR_RATE_LIMIT_PER_MIN", "180")
	t.Setenv("VISITOR_RATE_LIMIT_BURST", "60")
	t.Setenv("AGENT_RATE_LIMIT_PER_MIN", "240")
	t.Setenv("AGENT_RATE_LIMIT_BURST", "80")
	t.Setenv("ADMIN_RATE_LIMIT_PER_MIN", "120")
	t.Setenv("ADMIN_RATE_LIMIT_BURST", "40")
	t.Setenv("RATE_LIMIT_KEY_TTL_MINS", "30")
	t.Setenv("RATE_LIMIT_REDIS_ADDR", "redis:6379")
	t.Setenv("RATE_LIMIT_REDIS_PASSWORD", "redis-secret")
	t.Setenv("RATE_LIMIT_REDIS_DB", "2")
	t.Setenv("RATE_LIMIT_REDIS_PREFIX", "gateway:ratelimit")
	t.Setenv("RATE_LIMIT_REDIS_TIMEOUT_MS", "120")
	t.Setenv("RATE_LIMIT_REDIS_FAIL_THRESHOLD", "3")
	t.Setenv("RATE_LIMIT_REDIS_CIRCUIT_OPEN_SEC", "30")
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("ETCD_ENDPOINTS", "etcd:2379")
	t.Setenv("ETCD_DIAL_TIMEOUT_SEC", "5")
	t.Setenv("DISCOVERY_PREFIX", "/inlinechat/services")
	t.Setenv("CHAT_SERVICE_NAME", "chat-service")
	t.Setenv("AUTH_SERVICE_NAME", "auth-service")
	t.Setenv("ADMIN_SERVICE_NAME", "admin-service")
	t.Setenv("REALTIME_SERVICE_NAME", "realtime-service")
	t.Setenv("GRPC_DIAL_TIMEOUT_SEC", "8")
	t.Setenv("GRPC_CALL_TIMEOUT_SEC", "8")
}
