package middleware

import "testing"

func TestParseBearerToken(t *testing.T) {
	if got := parseBearerToken("Bearer aaa.bbb"); got != "aaa.bbb" {
		t.Fatalf("unexpected token: %q", got)
	}
	if got := parseBearerToken("bad token"); got != "" {
		t.Fatalf("expected empty token, got: %q", got)
	}
}
