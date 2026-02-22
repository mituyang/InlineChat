package config

import "testing"

func TestLoadSuccessWithDefaultTimeout(t *testing.T) {
	t.Setenv("HTTP_PORT", "8203")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("JWT_SECRET", "test_secret")
	t.Setenv("JWT_ISSUER", "inlinechat-auth")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("WS_ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("CHAT_GRPC_DIAL_TIMEOUT_SEC", "")
	t.Setenv("CHAT_GRPC_CALL_TIMEOUT_SEC", "")
	t.Setenv("ETCD_ENDPOINTS", "etcd:2379")
	t.Setenv("ETCD_DIAL_TIMEOUT_SEC", "")
	t.Setenv("ETCD_REGISTER_TTL_SEC", "")
	t.Setenv("CHAT_SERVICE_NAME", "chat-service")
	t.Setenv("AUTH_SERVICE_NAME", "auth-service")
	t.Setenv("SERVICE_NAME", "realtime-service")
	t.Setenv("SERVICE_ADVERTISE_HTTP_ENDPOINT", "http://realtime-service:8203")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}

	if cfg.ChatGRPCDialTimeout != 8 {
		t.Fatalf("expected ChatGRPCDialTimeout=8, got %d", cfg.ChatGRPCDialTimeout)
	}
	if cfg.ChatGRPCCallTimeout != 8 {
		t.Fatalf("expected ChatGRPCCallTimeout=8, got %d", cfg.ChatGRPCCallTimeout)
	}
}

func TestLoadRejectsInvalidTimeout(t *testing.T) {
	t.Setenv("HTTP_PORT", "8203")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("JWT_SECRET", "test_secret")
	t.Setenv("JWT_ISSUER", "inlinechat-auth")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("WS_ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("CHAT_GRPC_DIAL_TIMEOUT_SEC", "0")
	t.Setenv("CHAT_GRPC_CALL_TIMEOUT_SEC", "8")
	t.Setenv("ETCD_ENDPOINTS", "etcd:2379")
	t.Setenv("ETCD_DIAL_TIMEOUT_SEC", "5")
	t.Setenv("ETCD_REGISTER_TTL_SEC", "15")
	t.Setenv("CHAT_SERVICE_NAME", "chat-service")
	t.Setenv("AUTH_SERVICE_NAME", "auth-service")
	t.Setenv("SERVICE_NAME", "realtime-service")
	t.Setenv("SERVICE_ADVERTISE_HTTP_ENDPOINT", "http://realtime-service:8203")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestLoadRejectsMissingAllowedOrigins(t *testing.T) {
	t.Setenv("HTTP_PORT", "8203")
	t.Setenv("REDIS_ADDR", "redis:6379")
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("JWT_SECRET", "test_secret")
	t.Setenv("JWT_ISSUER", "inlinechat-auth")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("WS_ALLOWED_ORIGINS", "")
	t.Setenv("CHAT_GRPC_DIAL_TIMEOUT_SEC", "8")
	t.Setenv("CHAT_GRPC_CALL_TIMEOUT_SEC", "8")
	t.Setenv("ETCD_ENDPOINTS", "etcd:2379")
	t.Setenv("ETCD_DIAL_TIMEOUT_SEC", "5")
	t.Setenv("ETCD_REGISTER_TTL_SEC", "15")
	t.Setenv("CHAT_SERVICE_NAME", "chat-service")
	t.Setenv("AUTH_SERVICE_NAME", "auth-service")
	t.Setenv("SERVICE_NAME", "realtime-service")
	t.Setenv("SERVICE_ADVERTISE_HTTP_ENDPOINT", "http://realtime-service:8203")

	_, err := Load()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
