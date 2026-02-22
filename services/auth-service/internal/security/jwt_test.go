package security

import (
	"testing"
	"time"
)

func TestIssueAndParseToken(t *testing.T) {
	secret := []byte("test-secret")
	token, err := IssueToken(secret, "test-issuer", time.Hour, 1001, "admin@example.com", "admin")
	if err != nil {
		t.Fatalf("issue token failed: %v", err)
	}

	claims, err := ParseToken(secret, "test-issuer", token)
	if err != nil {
		t.Fatalf("parse token failed: %v", err)
	}

	if claims.AgentID != 1001 {
		t.Fatalf("unexpected agent_id: %d", claims.AgentID)
	}
	if claims.Email != "admin@example.com" {
		t.Fatalf("unexpected email: %s", claims.Email)
	}
	if claims.Role != "admin" {
		t.Fatalf("unexpected role: %s", claims.Role)
	}
}

func TestParseTokenAnyWithPreviousSecret(t *testing.T) {
	currentSecret := []byte("current-secret")
	previousSecret := []byte("previous-secret")
	token, err := IssueToken(previousSecret, "test-issuer", time.Hour, 1002, "agent@example.com", "agent")
	if err != nil {
		t.Fatalf("issue token failed: %v", err)
	}

	claims, err := ParseTokenAny([][]byte{currentSecret, previousSecret}, "test-issuer", token)
	if err != nil {
		t.Fatalf("parse token with previous secret failed: %v", err)
	}
	if claims.AgentID != 1002 {
		t.Fatalf("unexpected agent_id: %d", claims.AgentID)
	}
}
