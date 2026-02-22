package handler

import "testing"

func TestBuildVisitorRateLimitKeysMultiDimensions(t *testing.T) {
	keys := buildVisitorRateLimitKeys("10.10.0.8", "create_message", "site_demo", 88, "vt_abc123")

	expected := []string{
		"visitor:ip:10.10.0.8",
		"visitor:ip_action:create_message:10.10.0.8",
		"visitor:site:site_demo:10.10.0.8",
		"visitor:site_action:create_message:site_demo:10.10.0.8",
		"visitor:token:vt_abc123",
		"visitor:token_action:create_message:vt_abc123",
		"visitor:token_ip:vt_abc123:10.10.0.8",
		"visitor:conversation:88",
		"visitor:conversation_action:create_message:88",
		"visitor:conversation_token:88:vt_abc123",
	}

	for _, key := range expected {
		if !containsString(keys, key) {
			t.Fatalf("missing rate limit key %q in %v", key, keys)
		}
	}
}

func TestBuildVisitorRateLimitKeysFallbackAndDedupe(t *testing.T) {
	keys := buildVisitorRateLimitKeys("", "", "", 0, "")
	if len(keys) != 2 {
		t.Fatalf("expected 2 fallback keys, got %d (%v)", len(keys), keys)
	}
	if !containsString(keys, "visitor:ip:ip_unknown") {
		t.Fatalf("fallback ip key missing: %v", keys)
	}
	if !containsString(keys, "visitor:ip_action:unknown_action:ip_unknown") {
		t.Fatalf("fallback ip_action key missing: %v", keys)
	}

	deduped := dedupeRateLimitKeys([]string{"k1", "k1", "k2", "", "k2"})
	if len(deduped) != 2 {
		t.Fatalf("expected deduped keys length=2, got %d (%v)", len(deduped), deduped)
	}
}

func containsString(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
