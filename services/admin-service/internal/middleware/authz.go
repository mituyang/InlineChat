package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"inlinechat/services/admin-service/internal/security"
)

type Authz struct {
	secret []byte
	issuer string
}

func NewAuthz(secret string, issuer string) *Authz {
	return &Authz{secret: []byte(secret), issuer: issuer}
}

func (a *Authz) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := parseBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}

		claims, err := security.ParseToken(a.secret, a.issuer, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if claims.Role != "admin" && claims.Role != "super_admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin role required"})
			return
		}

		c.Set("claims", claims)
		c.Next()
	}
}

func parseBearerToken(header string) string {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 {
		return ""
	}
	if !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}
