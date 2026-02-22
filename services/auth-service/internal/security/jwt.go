package security

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	AgentID      uint64 `json:"agent_id"`
	Email        string `json:"email"`
	Role         string `json:"role"`
	TokenVersion uint64 `json:"token_version,omitempty"`
	jwt.RegisteredClaims
}

func IssueToken(secret []byte, issuer string, expire time.Duration, agentID uint64, email string, role string, tokenVersion uint64) (string, error) {
	now := time.Now()
	claims := Claims{
		AgentID:      agentID,
		Email:        email,
		Role:         role,
		TokenVersion: tokenVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   fmt.Sprintf("agent:%d", agentID),
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(expire)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(secret)
}

func ParseToken(secret []byte, issuer string, token string) (*Claims, error) {
	return ParseTokenAny([][]byte{secret}, issuer, token)
}

func ParseTokenAny(secrets [][]byte, issuer string, token string) (*Claims, error) {
	var lastErr error
	for _, secret := range secrets {
		if len(secret) == 0 {
			continue
		}
		claims, err := parseTokenWithSecret(secret, issuer, token)
		if err == nil {
			return claims, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("invalid token")
}

func parseTokenWithSecret(secret []byte, issuer string, token string) (*Claims, error) {
	parsed, err := jwt.ParseWithClaims(token, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return secret, nil
	}, jwt.WithIssuer(issuer))
	if err != nil {
		return nil, err
	}
	claims, ok := parsed.Claims.(*Claims)
	if !ok || !parsed.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
