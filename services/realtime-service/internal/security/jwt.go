package security

import (
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

type Claims struct {
	AgentID uint64 `json:"agent_id"`
	Email   string `json:"email"`
	Role    string `json:"role"`
	jwt.RegisteredClaims
}

func ParseToken(secret []byte, issuer string, token string) (*Claims, error) {
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
