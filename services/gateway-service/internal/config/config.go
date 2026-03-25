package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPPort                     string
	LogLevel                     string
	RequestIDHeader              string
	LoginRateLimitPerMin         int
	LoginRateLimitBurst          int
	VisitorRateLimitPerMin       int
	VisitorRateLimitBurst        int
	AgentRateLimitPerMin         int
	AgentRateLimitBurst          int
	AdminRateLimitPerMin         int
	AdminRateLimitBurst          int
	RateLimitKeyTTLMins          int
	RateLimitRedisAddr           string
	RateLimitRedisPassword       string
	RateLimitRedisDB             int
	RateLimitRedisPrefix         string
	RateLimitRedisTimeout        int
	RateLimitRedisFailThreshold  int
	RateLimitRedisCircuitOpenSec int
	ETCDEndpoints                []string
	ETCDDialTimeoutSec           int
	DiscoveryPrefix              string
	ChatServiceName              string
	AuthServiceName              string
	AdminServiceName             string
	RealtimeServiceName          string
	AIServiceName                string
	GRPCDialTimeoutSec           int
	GRPCCallTimeoutSec           int
}

func Load() (Config, error) {
	cfg := Config{
		HTTPPort:                     getEnv("HTTP_PORT", "8200"),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		RequestIDHeader:              getEnv("REQUEST_ID_HEADER", "X-Request-ID"),
		LoginRateLimitPerMin:         getIntEnv("LOGIN_RATE_LIMIT_PER_MIN", 60),
		LoginRateLimitBurst:          getIntEnv("LOGIN_RATE_LIMIT_BURST", 20),
		VisitorRateLimitPerMin:       getIntEnv("VISITOR_RATE_LIMIT_PER_MIN", 180),
		VisitorRateLimitBurst:        getIntEnv("VISITOR_RATE_LIMIT_BURST", 60),
		AgentRateLimitPerMin:         getIntEnv("AGENT_RATE_LIMIT_PER_MIN", 240),
		AgentRateLimitBurst:          getIntEnv("AGENT_RATE_LIMIT_BURST", 80),
		AdminRateLimitPerMin:         getIntEnv("ADMIN_RATE_LIMIT_PER_MIN", 120),
		AdminRateLimitBurst:          getIntEnv("ADMIN_RATE_LIMIT_BURST", 40),
		RateLimitKeyTTLMins:          getIntEnv("RATE_LIMIT_KEY_TTL_MINS", 30),
		RateLimitRedisAddr:           strings.TrimSpace(getEnv("RATE_LIMIT_REDIS_ADDR", os.Getenv("REDIS_ADDR"))),
		RateLimitRedisPassword:       getEnv("RATE_LIMIT_REDIS_PASSWORD", os.Getenv("REDIS_PASSWORD")),
		RateLimitRedisDB:             getIntEnv("RATE_LIMIT_REDIS_DB", getIntEnv("REDIS_DB", 0)),
		RateLimitRedisPrefix:         getEnv("RATE_LIMIT_REDIS_PREFIX", "gateway:ratelimit"),
		RateLimitRedisTimeout:        getIntEnv("RATE_LIMIT_REDIS_TIMEOUT_MS", 120),
		RateLimitRedisFailThreshold:  getIntEnv("RATE_LIMIT_REDIS_FAIL_THRESHOLD", 3),
		RateLimitRedisCircuitOpenSec: getIntEnv("RATE_LIMIT_REDIS_CIRCUIT_OPEN_SEC", 30),
		ETCDEndpoints:                splitAndTrim(os.Getenv("ETCD_ENDPOINTS")),
		ETCDDialTimeoutSec:           getIntEnv("ETCD_DIAL_TIMEOUT_SEC", 5),
		DiscoveryPrefix:              strings.TrimSpace(getEnv("DISCOVERY_PREFIX", "/inlinechat/services")),
		ChatServiceName:              strings.TrimSpace(getEnv("CHAT_SERVICE_NAME", "chat-service")),
		AuthServiceName:              strings.TrimSpace(getEnv("AUTH_SERVICE_NAME", "auth-service")),
		AdminServiceName:             strings.TrimSpace(getEnv("ADMIN_SERVICE_NAME", "admin-service")),
		RealtimeServiceName:          strings.TrimSpace(getEnv("REALTIME_SERVICE_NAME", "realtime-service")),
		AIServiceName:                strings.TrimSpace(getEnv("AI_SERVICE_NAME", "ai-service")),
		GRPCDialTimeoutSec:           getIntEnv("GRPC_DIAL_TIMEOUT_SEC", 8),
		GRPCCallTimeoutSec:           getIntEnv("GRPC_CALL_TIMEOUT_SEC", 8),
	}

	if len(cfg.ETCDEndpoints) == 0 {
		return Config{}, fmt.Errorf("ETCD_ENDPOINTS is required")
	}
	if cfg.LoginRateLimitPerMin <= 0 {
		return Config{}, fmt.Errorf("LOGIN_RATE_LIMIT_PER_MIN must be greater than 0")
	}
	if cfg.LoginRateLimitBurst <= 0 {
		return Config{}, fmt.Errorf("LOGIN_RATE_LIMIT_BURST must be greater than 0")
	}
	if cfg.VisitorRateLimitPerMin <= 0 {
		return Config{}, fmt.Errorf("VISITOR_RATE_LIMIT_PER_MIN must be greater than 0")
	}
	if cfg.VisitorRateLimitBurst <= 0 {
		return Config{}, fmt.Errorf("VISITOR_RATE_LIMIT_BURST must be greater than 0")
	}
	if cfg.AgentRateLimitPerMin <= 0 {
		return Config{}, fmt.Errorf("AGENT_RATE_LIMIT_PER_MIN must be greater than 0")
	}
	if cfg.AgentRateLimitBurst <= 0 {
		return Config{}, fmt.Errorf("AGENT_RATE_LIMIT_BURST must be greater than 0")
	}
	if cfg.AdminRateLimitPerMin <= 0 {
		return Config{}, fmt.Errorf("ADMIN_RATE_LIMIT_PER_MIN must be greater than 0")
	}
	if cfg.AdminRateLimitBurst <= 0 {
		return Config{}, fmt.Errorf("ADMIN_RATE_LIMIT_BURST must be greater than 0")
	}
	if cfg.RateLimitKeyTTLMins <= 0 {
		return Config{}, fmt.Errorf("RATE_LIMIT_KEY_TTL_MINS must be greater than 0")
	}
	if cfg.RateLimitRedisTimeout <= 0 {
		return Config{}, fmt.Errorf("RATE_LIMIT_REDIS_TIMEOUT_MS must be greater than 0")
	}
	if cfg.RateLimitRedisFailThreshold <= 0 {
		return Config{}, fmt.Errorf("RATE_LIMIT_REDIS_FAIL_THRESHOLD must be greater than 0")
	}
	if cfg.RateLimitRedisCircuitOpenSec <= 0 {
		return Config{}, fmt.Errorf("RATE_LIMIT_REDIS_CIRCUIT_OPEN_SEC must be greater than 0")
	}
	if cfg.ETCDDialTimeoutSec <= 0 {
		return Config{}, fmt.Errorf("ETCD_DIAL_TIMEOUT_SEC must be greater than 0")
	}
	if cfg.ChatServiceName == "" || cfg.AuthServiceName == "" || cfg.AdminServiceName == "" || cfg.RealtimeServiceName == "" || cfg.AIServiceName == "" {
		return Config{}, fmt.Errorf("CHAT_SERVICE_NAME AUTH_SERVICE_NAME ADMIN_SERVICE_NAME REALTIME_SERVICE_NAME AI_SERVICE_NAME are required")
	}
	if cfg.GRPCDialTimeoutSec <= 0 {
		return Config{}, fmt.Errorf("GRPC_DIAL_TIMEOUT_SEC must be greater than 0")
	}
	if cfg.GRPCCallTimeoutSec <= 0 {
		return Config{}, fmt.Errorf("GRPC_CALL_TIMEOUT_SEC must be greater than 0")
	}

	return cfg, nil
}

func getEnv(key string, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}

func getIntEnv(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func splitAndTrim(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
