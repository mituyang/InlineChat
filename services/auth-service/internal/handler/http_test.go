package handler

import "testing"

func TestParseBearerToken(t *testing.T) {
	if got := parseBearerToken("Bearer abc.def"); got != "abc.def" {
		t.Fatalf("unexpected token: %q", got)
	}
	if got := parseBearerToken("bearer xyz"); got != "xyz" {
		t.Fatalf("unexpected token: %q", got)
	}
	if got := parseBearerToken("Basic abc"); got != "" {
		t.Fatalf("expected empty token, got: %q", got)
	}
}
