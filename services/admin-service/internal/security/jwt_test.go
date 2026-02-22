package security

import (
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

func TestParseToken(t *testing.T) {
	secret := []byte("test-secret")
	claims := Claims{
		AgentID:      9,
		Email:        "ops@example.com",
		Role:         "admin",
		TokenVersion: 3,
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   "agent:9",
			IssuedAt:  jwtv5.NewNumericDate(time.Now()),
			NotBefore: jwtv5.NewNumericDate(time.Now()),
			ExpiresAt: jwtv5.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims).SignedString(secret)
	if err != nil {
		t.Fatalf("sign token failed: %v", err)
	}

	parsed, err := ParseToken(secret, "test-issuer", token)
	if err != nil {
		t.Fatalf("parse token failed: %v", err)
	}

	if parsed.AgentID != claims.AgentID || parsed.Email != claims.Email || parsed.Role != claims.Role || parsed.TokenVersion != claims.TokenVersion {
		t.Fatalf("unexpected claims: %+v", parsed)
	}
}

func TestParseTokenAnyWithPreviousSecret(t *testing.T) {
	currentSecret := []byte("current-secret")
	previousSecret := []byte("previous-secret")
	claims := Claims{
		AgentID:      10,
		Email:        "agent@example.com",
		Role:         "admin",
		TokenVersion: 2,
		RegisteredClaims: jwtv5.RegisteredClaims{
			Issuer:    "test-issuer",
			Subject:   "agent:10",
			IssuedAt:  jwtv5.NewNumericDate(time.Now()),
			NotBefore: jwtv5.NewNumericDate(time.Now()),
			ExpiresAt: jwtv5.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	token, err := jwtv5.NewWithClaims(jwtv5.SigningMethodHS256, claims).SignedString(previousSecret)
	if err != nil {
		t.Fatalf("sign token failed: %v", err)
	}

	parsed, err := ParseTokenAny([][]byte{currentSecret, previousSecret}, "test-issuer", token)
	if err != nil {
		t.Fatalf("parse token with previous secret failed: %v", err)
	}
	if parsed.AgentID != claims.AgentID {
		t.Fatalf("unexpected claims: %+v", parsed)
	}
}
