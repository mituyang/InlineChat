package security

import (
	"testing"
	"time"

	jwtv5 "github.com/golang-jwt/jwt/v5"
)

func TestParseToken(t *testing.T) {
	secret := []byte("test-secret")
	claims := Claims{
		AgentID: 9,
		Email:   "ops@example.com",
		Role:    "admin",
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

	if parsed.AgentID != claims.AgentID || parsed.Email != claims.Email || parsed.Role != claims.Role {
		t.Fatalf("unexpected claims: %+v", parsed)
	}
}
