package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	HTTPPort                     string
	RedisAddr                    string
	RedisPassword                string
	RedisDB                      int
	ChatGRPCDialTimeout          int
	ChatGRPCCallTimeout          int
	LogLevel                     string
	AllowedOrigins               []string
	ETCDEndpoints                []string
	ETCDDialTimeoutSec           int
	ETCDRegisterTTLSec           int
	DiscoveryPrefix              string
	ChatServiceName              string
	ServiceName                  string
	ServiceInstanceID            string
	ServiceAdvertiseHTTPEndpoint string
}

func Load() (Config, error) {
	cfg := Config{
		HTTPPort:                     getEnv("HTTP_PORT", "8203"),
		RedisAddr:                    os.Getenv("REDIS_ADDR"),
		RedisPassword:                os.Getenv("REDIS_PASSWORD"),
		RedisDB:                      getIntEnv("REDIS_DB", 0),
		ChatGRPCDialTimeout:          getIntEnv("CHAT_GRPC_DIAL_TIMEOUT_SEC", 8),
		ChatGRPCCallTimeout:          getIntEnv("CHAT_GRPC_CALL_TIMEOUT_SEC", 8),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		AllowedOrigins:               splitAndTrim(getEnv("WS_ALLOWED_ORIGINS", "*")),
		ETCDEndpoints:                splitAndTrim(os.Getenv("ETCD_ENDPOINTS")),
		ETCDDialTimeoutSec:           getIntEnv("ETCD_DIAL_TIMEOUT_SEC", 5),
		ETCDRegisterTTLSec:           getIntEnv("ETCD_REGISTER_TTL_SEC", 15),
		DiscoveryPrefix:              getEnv("DISCOVERY_PREFIX", "/inlinechat/services"),
		ChatServiceName:              getEnv("CHAT_SERVICE_NAME", "chat-service"),
		ServiceName:                  getEnv("SERVICE_NAME", "realtime-service"),
		ServiceInstanceID:            strings.TrimSpace(os.Getenv("SERVICE_INSTANCE_ID")),
		ServiceAdvertiseHTTPEndpoint: os.Getenv("SERVICE_ADVERTISE_HTTP_ENDPOINT"),
	}

	if cfg.RedisAddr == "" {
		return Config{}, fmt.Errorf("REDIS_ADDR is required")
	}
	if len(cfg.ETCDEndpoints) == 0 {
		return Config{}, fmt.Errorf("ETCD_ENDPOINTS is required")
	}
	if cfg.ETCDDialTimeoutSec <= 0 {
		return Config{}, fmt.Errorf("ETCD_DIAL_TIMEOUT_SEC must be greater than 0")
	}
	if cfg.ETCDRegisterTTLSec <= 0 {
		return Config{}, fmt.Errorf("ETCD_REGISTER_TTL_SEC must be greater than 0")
	}
	if cfg.ChatServiceName == "" {
		return Config{}, fmt.Errorf("CHAT_SERVICE_NAME is required")
	}
	if cfg.ServiceName == "" {
		return Config{}, fmt.Errorf("SERVICE_NAME is required")
	}
	if strings.TrimSpace(cfg.ServiceAdvertiseHTTPEndpoint) == "" {
		return Config{}, fmt.Errorf("SERVICE_ADVERTISE_HTTP_ENDPOINT is required")
	}
	if cfg.ChatGRPCDialTimeout <= 0 {
		return Config{}, fmt.Errorf("CHAT_GRPC_DIAL_TIMEOUT_SEC must be greater than 0")
	}
	if cfg.ChatGRPCCallTimeout <= 0 {
		return Config{}, fmt.Errorf("CHAT_GRPC_CALL_TIMEOUT_SEC must be greater than 0")
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
	if len(out) == 0 {
		return []string{"*"}
	}
	return out
}
