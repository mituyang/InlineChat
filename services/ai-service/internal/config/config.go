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
	RedisAddr                    string
	RedisPassword                string
	RedisDB                      int
	ETCDEndpoints                []string
	ETCDDialTimeoutSec           int
	ETCDRegisterTTLSec           int
	DiscoveryPrefix              string
	ChatServiceName              string
	AdminServiceName             string
	ServiceName                  string
	ServiceInstanceID            string
	ServiceAdvertiseHTTPEndpoint string
	GRPCDialTimeoutSec           int
	GRPCCallTimeoutSec           int
	AILLMBaseURL                 string
	AILLMModel                   string
	AILLMAPIKey                  string
	AIEmbeddingBaseURL           string
	AIEmbeddingModel             string
	AIEmbeddingAPIKey            string
	AIKBPath                     string
	AIRetrieveTopK               int
	AIMinSimilarity              float64
	AIUnknownReply               string
	AIHTTPTimeoutMS              int
}

func Load() (Config, error) {
	cfg := Config{
		HTTPPort:                     getEnv("HTTP_PORT", "8205"),
		LogLevel:                     getEnv("LOG_LEVEL", "info"),
		RedisAddr:                    strings.TrimSpace(os.Getenv("REDIS_ADDR")),
		RedisPassword:                os.Getenv("REDIS_PASSWORD"),
		RedisDB:                      getIntEnv("REDIS_DB", 0),
		ETCDEndpoints:                splitAndTrim(os.Getenv("ETCD_ENDPOINTS")),
		ETCDDialTimeoutSec:           getIntEnv("ETCD_DIAL_TIMEOUT_SEC", 5),
		ETCDRegisterTTLSec:           getIntEnv("ETCD_REGISTER_TTL_SEC", 15),
		DiscoveryPrefix:              getEnv("DISCOVERY_PREFIX", "/inlinechat/services"),
		ChatServiceName:              getEnv("CHAT_SERVICE_NAME", "chat-service"),
		AdminServiceName:             getEnv("ADMIN_SERVICE_NAME", "admin-service"),
		ServiceName:                  getEnv("SERVICE_NAME", "ai-service"),
		ServiceInstanceID:            strings.TrimSpace(os.Getenv("SERVICE_INSTANCE_ID")),
		ServiceAdvertiseHTTPEndpoint: strings.TrimSpace(os.Getenv("SERVICE_ADVERTISE_HTTP_ENDPOINT")),
		GRPCDialTimeoutSec:           getIntEnv("AI_GRPC_DIAL_TIMEOUT_SEC", 8),
		GRPCCallTimeoutSec:           getIntEnv("AI_GRPC_CALL_TIMEOUT_SEC", 8),
		AILLMBaseURL:                 strings.TrimRight(strings.TrimSpace(os.Getenv("AI_LLM_BASE_URL")), "/"),
		AILLMModel:                   strings.TrimSpace(os.Getenv("AI_LLM_MODEL")),
		AILLMAPIKey:                  strings.TrimSpace(os.Getenv("AI_LLM_API_KEY")),
		AIEmbeddingBaseURL:           strings.TrimRight(strings.TrimSpace(os.Getenv("AI_EMBEDDING_BASE_URL")), "/"),
		AIEmbeddingModel:             strings.TrimSpace(os.Getenv("AI_EMBEDDING_MODEL")),
		AIEmbeddingAPIKey:            strings.TrimSpace(os.Getenv("AI_EMBEDDING_API_KEY")),
		AIKBPath:                     getEnv("AI_KB_PATH", "docs/qinghe-home-customer-knowledge-base.md"),
		AIRetrieveTopK:               getIntEnv("AI_RETRIEVE_TOP_K", 5),
		AIMinSimilarity:              getFloatEnv("AI_MIN_SIMILARITY", 0.35),
		AIUnknownReply:               getEnv("AI_UNKNOWN_REPLY", "当前资料未提及，我暂时无法确认，请联系人工客服。"),
		AIHTTPTimeoutMS:              getIntEnv("AI_HTTP_TIMEOUT_MS", 15000),
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
	if cfg.ChatServiceName == "" || cfg.AdminServiceName == "" {
		return Config{}, fmt.Errorf("CHAT_SERVICE_NAME and ADMIN_SERVICE_NAME are required")
	}
	if cfg.ServiceName == "" {
		return Config{}, fmt.Errorf("SERVICE_NAME is required")
	}
	if cfg.ServiceAdvertiseHTTPEndpoint == "" {
		return Config{}, fmt.Errorf("SERVICE_ADVERTISE_HTTP_ENDPOINT is required")
	}
	if cfg.GRPCDialTimeoutSec <= 0 || cfg.GRPCCallTimeoutSec <= 0 {
		return Config{}, fmt.Errorf("AI_GRPC_DIAL_TIMEOUT_SEC and AI_GRPC_CALL_TIMEOUT_SEC must be greater than 0")
	}
	if cfg.AILLMBaseURL == "" || cfg.AILLMModel == "" {
		return Config{}, fmt.Errorf("AI_LLM_BASE_URL and AI_LLM_MODEL are required")
	}
	if cfg.AIEmbeddingBaseURL == "" || cfg.AIEmbeddingModel == "" {
		return Config{}, fmt.Errorf("AI_EMBEDDING_BASE_URL and AI_EMBEDDING_MODEL are required")
	}
	if cfg.AIKBPath == "" {
		return Config{}, fmt.Errorf("AI_KB_PATH is required")
	}
	if cfg.AIRetrieveTopK <= 0 {
		return Config{}, fmt.Errorf("AI_RETRIEVE_TOP_K must be greater than 0")
	}
	if cfg.AIMinSimilarity < 0 || cfg.AIMinSimilarity > 1 {
		return Config{}, fmt.Errorf("AI_MIN_SIMILARITY must be between 0 and 1")
	}
	if strings.TrimSpace(cfg.AIUnknownReply) == "" {
		return Config{}, fmt.Errorf("AI_UNKNOWN_REPLY is required")
	}
	if cfg.AIHTTPTimeoutMS <= 0 {
		return Config{}, fmt.Errorf("AI_HTTP_TIMEOUT_MS must be greater than 0")
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

func getFloatEnv(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
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
