package handler

import (
	"testing"

	"inlinechat/services/gateway-service/internal/aiclient"
	adminv1 "inlinechat/services/gateway-service/internal/gen/adminv1"
)

func TestMergeSiteAIConfig(t *testing.T) {
	payload := mergeSiteAIConfig(&adminv1.SiteAIConfig{
		SiteId:    "site_demo",
		Enabled:   true,
		ReplyMode: "unassigned_auto_reply",
		UpdatedAt: "2026-04-15T00:00:00Z",
	}, &aiclient.SiteStatus{
		SiteID:         "site_demo",
		KnowledgeDir:   "/app/data/knowledgebases/site_demo",
		IndexStatus:    "ready",
		IndexedChunks:  12,
		LastIndexedAt:  "2026-04-15T00:00:01Z",
		LastIndexError: "",
		ActiveJobID:    "",
	})

	if payload["site_id"] != "site_demo" {
		t.Fatalf("site_id = %v", payload["site_id"])
	}
	if payload["index_status"] != "ready" {
		t.Fatalf("index_status = %v", payload["index_status"])
	}
	if payload["indexed_chunks"] != 12 {
		t.Fatalf("indexed_chunks = %v", payload["indexed_chunks"])
	}
}
