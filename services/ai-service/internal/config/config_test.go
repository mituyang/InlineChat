package config

import "testing"

func TestLoadSupportsLegacyLLMEnv(t *testing.T) {
	t.Setenv("REDIS_ADDR", "127.0.0.1:6379")
	t.Setenv("ETCD_ENDPOINTS", "127.0.0.1:2379")
	t.Setenv("SERVICE_ADVERTISE_HTTP_ENDPOINT", "http://ai-service:8205")
	t.Setenv("AI_LLM_BASE_URL", "http://localhost:8642/v1")
	t.Setenv("AI_LLM_MODEL", "hermes-agent")
	t.Setenv("AI_EMBEDDING_BASE_URL", "http://localhost:8298/v1")
	t.Setenv("AI_EMBEDDING_MODEL", "Qwen3-Embedding-0.6B")
	t.Setenv("AI_RERANKER_BASE_URL", "http://localhost:8299")
	t.Setenv("AI_QDRANT_URL", "http://localhost:6333")
	t.Setenv("AI_KB_ROOT_DIR", "/tmp/knowledgebases")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.AIChatBaseURL != "http://localhost:8642/v1" {
		t.Fatalf("AIChatBaseURL = %q", cfg.AIChatBaseURL)
	}
	if cfg.AIChatModel != "hermes-agent" {
		t.Fatalf("AIChatModel = %q", cfg.AIChatModel)
	}
	if cfg.AIQdrantCollection != defaultQdrantCollection {
		t.Fatalf("AIQdrantCollection = %q", cfg.AIQdrantCollection)
	}
	if cfg.AIIndexEmbedBatchSize != 4 {
		t.Fatalf("AIIndexEmbedBatchSize = %d", cfg.AIIndexEmbedBatchSize)
	}
}
