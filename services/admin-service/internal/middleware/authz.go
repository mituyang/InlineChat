package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"inlinechat/services/admin-service/internal/repository"
	"inlinechat/services/admin-service/internal/security"
)

type Authz struct {
	// secrets 支持 JWT 密钥轮转期间并行验签。
	secrets        [][]byte
	issuer         string
	agentRepo      repository.AgentRepository
	superAdminRepo repository.SuperAdminRepository
}

func NewAuthz(
	secret string,
	previousSecret string,
	issuer string,
	agentRepo repository.AgentRepository,
	superAdminRepo repository.SuperAdminRepository,
) *Authz {
	return &Authz{
		secrets:        buildJWTSecrets(secret, previousSecret),
		issuer:         issuer,
		agentRepo:      agentRepo,
		superAdminRepo: superAdminRepo,
	}
}

func (a *Authz) RequireAdmin() gin.HandlerFunc {
	// 中间件职责：身份认证 + 角色准入 + 会话状态校验。
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
		if !a.validateSessionByRole(c, claims) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
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

func (a *Authz) validateSessionByRole(c *gin.Context, claims *security.Claims) bool {
	// 通过数据库状态兜底，防止已禁用账号继续使用旧 token。
	if claims == nil {
		return false
	}

	role := strings.ToLower(strings.TrimSpace(claims.Role))
	switch role {
	case "super_admin":
		if a.superAdminRepo == nil {
			return false
		}
		superAdmin, err := a.superAdminRepo.GetByID(c.Request.Context(), claims.AgentID)
		if err != nil {
			return false
		}
		if strings.ToLower(strings.TrimSpace(superAdmin.Status)) != "active" {
			return false
		}
		return normalizeTokenVersion(superAdmin.TokenVersion) == normalizeTokenVersion(claims.TokenVersion)
	case "admin", "agent":
		if a.agentRepo == nil {
			return false
		}
		agent, err := a.agentRepo.GetByID(c.Request.Context(), claims.AgentID)
		if err != nil {
			return false
		}
		if strings.ToLower(strings.TrimSpace(agent.Status)) != "active" {
			return false
		}
		return normalizeTokenVersion(agent.TokenVersion) == normalizeTokenVersion(claims.TokenVersion)
	default:
		return false
	}
}
