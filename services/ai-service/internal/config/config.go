package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const defaultQdrantCollection = "site_knowledge_chunks"

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
	AIChatBaseURL                string
	AIChatModel                  string
	AIChatAPIKey                 string
	AIEmbeddingBaseURL           string
	AIEmbeddingModel             string
	AIEmbeddingAPIKey            string
	AIRerankerBaseURL            string
	AIQdrantURL                  string
	AIQdrantAPIKey               string
	AIQdrantCollection           string
	AIKBRootDir                  string
	AIIndexEmbedBatchSize        int
	AIRetrievalCandidateK        int
	AIRerankTopK                 int
	AIRerankMinScore             float64
	AIUnknownReply               string
	AIHTTPTimeoutMS              int
	AIDisableExternalReadiness   bool
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
		AIChatBaseURL:                strings.TrimRight(strings.TrimSpace(firstNonEmptyEnv("AI_CHAT_BASE_URL", "AI_LLM_BASE_URL")), "/"),
		AIChatModel:                  strings.TrimSpace(firstNonEmptyEnv("AI_CHAT_MODEL", "AI_LLM_MODEL")),
		AIChatAPIKey:                 strings.TrimSpace(firstNonEmptyEnv("AI_CHAT_API_KEY", "AI_LLM_API_KEY")),
		AIEmbeddingBaseURL:           strings.TrimRight(strings.TrimSpace(os.Getenv("AI_EMBEDDING_BASE_URL")), "/"),
		AIEmbeddingModel:             strings.TrimSpace(os.Getenv("AI_EMBEDDING_MODEL")),
		AIEmbeddingAPIKey:            strings.TrimSpace(os.Getenv("AI_EMBEDDING_API_KEY")),
		AIRerankerBaseURL:            strings.TrimRight(strings.TrimSpace(os.Getenv("AI_RERANKER_BASE_URL")), "/"),
		AIQdrantURL:                  strings.TrimRight(strings.TrimSpace(os.Getenv("AI_QDRANT_URL")), "/"),
		AIQdrantAPIKey:               strings.TrimSpace(os.Getenv("AI_QDRANT_API_KEY")),
		AIQdrantCollection:           strings.TrimSpace(getEnv("AI_QDRANT_COLLECTION", defaultQdrantCollection)),
		AIKBRootDir:                  strings.TrimSpace(getEnv("AI_KB_ROOT_DIR", "/app/data/knowledgebases")),
		AIIndexEmbedBatchSize:        getIntEnv("AI_INDEX_EMBED_BATCH_SIZE", 4),
		AIRetrievalCandidateK:        getIntEnv("AI_RETRIEVAL_CANDIDATE_K", 12),
		AIRerankTopK:                 getIntEnv("AI_RERANK_TOP_K", 4),
		AIRerankMinScore:             getFloatEnv("AI_RERANK_MIN_SCORE", 0.15),
		AIUnknownReply:               getEnv("AI_UNKNOWN_REPLY", "当前资料未提及，我暂时无法确认，请联系人工客服。"),
		AIHTTPTimeoutMS:              getIntEnv("AI_HTTP_TIMEOUT_MS", 15000),
		AIDisableExternalReadiness:   getBoolEnv("AI_DISABLE_EXTERNAL_READINESS", false),
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
	if cfg.AIChatBaseURL == "" || cfg.AIChatModel == "" {
		return Config{}, fmt.Errorf("AI_CHAT_BASE_URL and AI_CHAT_MODEL are required")
	}
	if cfg.AIEmbeddingBaseURL == "" || cfg.AIEmbeddingModel == "" {
		return Config{}, fmt.Errorf("AI_EMBEDDING_BASE_URL and AI_EMBEDDING_MODEL are required")
	}
	if cfg.AIRerankerBaseURL == "" {
		return Config{}, fmt.Errorf("AI_RERANKER_BASE_URL is required")
	}
	if cfg.AIQdrantURL == "" {
		return Config{}, fmt.Errorf("AI_QDRANT_URL is required")
	}
	if cfg.AIQdrantCollection == "" {
		return Config{}, fmt.Errorf("AI_QDRANT_COLLECTION is required")
	}
	if cfg.AIKBRootDir == "" {
		return Config{}, fmt.Errorf("AI_KB_ROOT_DIR is required")
	}
	if cfg.AIIndexEmbedBatchSize <= 0 {
		return Config{}, fmt.Errorf("AI_INDEX_EMBED_BATCH_SIZE must be greater than 0")
	}
	if cfg.AIRetrievalCandidateK <= 0 {
		return Config{}, fmt.Errorf("AI_RETRIEVAL_CANDIDATE_K must be greater than 0")
	}
	if cfg.AIRerankTopK <= 0 {
		return Config{}, fmt.Errorf("AI_RERANK_TOP_K must be greater than 0")
	}
	if cfg.AIRerankTopK > cfg.AIRetrievalCandidateK {
		return Config{}, fmt.Errorf("AI_RERANK_TOP_K must be less than or equal to AI_RETRIEVAL_CANDIDATE_K")
	}
	if cfg.AIRerankMinScore < -1 || cfg.AIRerankMinScore > 1 {
		return Config{}, fmt.Errorf("AI_RERANK_MIN_SCORE must be between -1 and 1")
	}
	if strings.TrimSpace(cfg.AIUnknownReply) == "" {
		return Config{}, fmt.Errorf("AI_UNKNOWN_REPLY is required")
	}
	if cfg.AIHTTPTimeoutMS <= 0 {
		return Config{}, fmt.Errorf("AI_HTTP_TIMEOUT_MS must be greater than 0")
	}

	return cfg, nil
}

func firstNonEmptyEnv(keys ...string) string {
	for _, key := range keys {
		value := strings.TrimSpace(os.Getenv(key))
		if value != "" {
			return value
		}
	}
	return ""
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

func getBoolEnv(key string, fallback bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return fallback
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
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
