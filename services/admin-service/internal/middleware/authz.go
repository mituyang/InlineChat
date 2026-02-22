package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"inlinechat/services/admin-service/internal/repository"
	"inlinechat/services/admin-service/internal/security"
)

type Authz struct {
	secrets   [][]byte
	issuer    string
	agentRepo repository.AgentRepository
}

func NewAuthz(secret string, previousSecret string, issuer string, agentRepo repository.AgentRepository) *Authz {
	return &Authz{
		secrets:   buildJWTSecrets(secret, previousSecret),
		issuer:    issuer,
		agentRepo: agentRepo,
	}
}

func (a *Authz) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := parseBearerToken(c.GetHeader("Authorization"))
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}

		claims, err := security.ParseTokenAny(a.secrets, a.issuer, token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if claims.Role != "admin" && claims.Role != "super_admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin role required"})
			return
		}
		if a.agentRepo != nil {
			agent, findErr := a.agentRepo.GetByID(c.Request.Context(), claims.AgentID)
			if findErr != nil ||
				strings.ToLower(strings.TrimSpace(agent.Status)) != "active" ||
				normalizeTokenVersion(agent.TokenVersion) != normalizeTokenVersion(claims.TokenVersion) {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
				return
			}
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

func buildJWTSecrets(primary string, previous string) [][]byte {
	out := make([][]byte, 0, 2)
	primaryText := strings.TrimSpace(primary)
	if primaryText != "" {
		out = append(out, []byte(primaryText))
	}
	previousText := strings.TrimSpace(previous)
	if previousText != "" && previousText != primaryText {
		out = append(out, []byte(previousText))
	}
	return out
}

func normalizeTokenVersion(v uint64) uint64 {
	if v == 0 {
		return 1
	}
	return v
}
