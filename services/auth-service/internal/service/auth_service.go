package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"inlinechat/services/auth-service/internal/model"
	"inlinechat/services/auth-service/internal/repository"
	"inlinechat/services/auth-service/internal/security"
)

var (
	ErrUnauthorized      = errors.New("unauthorized")
	ErrForbidden         = errors.New("forbidden")
	ErrConflict          = errors.New("conflict")
	ErrInvalidCredential = errors.New("invalid credentials")
)

type AuthService struct {
	// agent/super_admin 走不同表，登录与校验时按角色分流。
	agentRepo             repository.AgentRepository
	superAdminRepo        repository.SuperAdminRepository
	jwtSecret             []byte
	jwtVerifySecrets      [][]byte
	jwtIssuer             string
	jwtExpire             time.Duration
	bcryptCost            int
	superAdminEmail       string
	superAdminPassword    string
	superAdminDisplayName string
}

type LoginInput struct {
	Email    string
	Password string
}

type AuthResult struct {
	Token string      `json:"token"`
	Agent model.Agent `json:"agent"`
}

// New 构建认证服务，并预处理密钥轮转与超级管理员配置。
func New(
	agentRepo repository.AgentRepository,
	superAdminRepo repository.SuperAdminRepository,
	jwtSecret string,
	jwtPreviousSecret string,
	jwtIssuer string,
	jwtExpire time.Duration,
	bcryptCost int,
	superAdminEmail string,
	superAdminPassword string,
	superAdminDisplayName string,
) *AuthService {
	return &AuthService{
		agentRepo:             agentRepo,
		superAdminRepo:        superAdminRepo,
		jwtSecret:             []byte(jwtSecret),
		jwtVerifySecrets:      buildJWTVerifySecrets(jwtSecret, jwtPreviousSecret),
		jwtIssuer:             jwtIssuer,
		jwtExpire:             jwtExpire,
		bcryptCost:            bcryptCost,
		superAdminEmail:       strings.ToLower(strings.TrimSpace(superAdminEmail)),
		superAdminPassword:    strings.TrimSpace(superAdminPassword),
		superAdminDisplayName: strings.TrimSpace(superAdminDisplayName),
	}
}

// Login 先尝试 super_admin，再回退普通 agent，确保同邮箱优先超级管理员身份。
func (s *AuthService) Login(ctx context.Context, in LoginInput) (*AuthResult, error) {
	email := strings.ToLower(strings.TrimSpace(in.Email))
	if email == "" || strings.TrimSpace(in.Password) == "" {
		return nil, ErrInvalidCredential
	}

	superAdmin, superErr := s.superAdminRepo.GetByEmail(ctx, email)
	if superErr == nil {
		if !isActiveStatus(superAdmin.Status) {
			return nil, ErrUnauthorized
		}
		if err := security.ComparePassword(superAdmin.PasswordHash, in.Password); err != nil {
			return nil, ErrInvalidCredential
		}

		tokenVersion := normalizeTokenVersion(superAdmin.TokenVersion)
		token, err := security.IssueToken(
			s.jwtSecret,
			s.jwtIssuer,
			s.jwtExpire,
			superAdmin.ID,
			superAdmin.Email,
			"super_admin",
			tokenVersion,
		)
		if err != nil {
			return nil, err
		}

		return &AuthResult{
			Token: token,
			Agent: model.Agent{
				ID:           superAdmin.ID,
				Email:        superAdmin.Email,
				DisplayName:  superAdmin.DisplayName,
				Role:         "super_admin",
				Status:       superAdmin.Status,
				TokenVersion: tokenVersion,
				CreatedAt:    superAdmin.CreatedAt,
				UpdatedAt:    superAdmin.UpdatedAt,
			},
		}, nil
	}
	if !errors.Is(superErr, repository.ErrNotFound) {
		return nil, superErr
	}

	agent, err := s.agentRepo.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrInvalidCredential
		}
		return nil, err
	}
	if !isActiveStatus(agent.Status) {
		return nil, ErrUnauthorized
	}
	if err := security.ComparePassword(agent.PasswordHash, in.Password); err != nil {
		return nil, ErrInvalidCredential
	}

	tokenVersion := normalizeTokenVersion(agent.TokenVersion)
	token, err := security.IssueToken(s.jwtSecret, s.jwtIssuer, s.jwtExpire, agent.ID, agent.Email, agent.Role, tokenVersion)
	if err != nil {
		return nil, err
	}

	return &AuthResult{Token: token, Agent: *agent}, nil
}

// ParseToken 只做签名/issuer 解析，不检查账号状态与 token_version。
func (s *AuthService) ParseToken(token string) (*security.Claims, error) {
	return security.ParseTokenAny(s.jwtVerifySecrets, s.jwtIssuer, token)
}

// ValidateToken 在 ParseToken 基础上补齐会话态校验（状态 + token_version）。
func (s *AuthService) ValidateToken(ctx context.Context, token string) (*security.Claims, error) {
	claims, err := s.ParseToken(token)
	if err != nil {
		return nil, ErrUnauthorized
	}

	switch strings.ToLower(strings.TrimSpace(claims.Role)) {
	case "super_admin":
		superAdmin, getErr := s.superAdminRepo.GetByID(ctx, claims.AgentID)
		if getErr != nil {
			if errors.Is(getErr, repository.ErrNotFound) {
				return nil, ErrUnauthorized
			}
			return nil, getErr
		}
		if !isActiveStatus(superAdmin.Status) {
			return nil, ErrUnauthorized
		}
		if normalizeTokenVersion(claims.TokenVersion) != normalizeTokenVersion(superAdmin.TokenVersion) {
			return nil, ErrUnauthorized
		}
	case "agent", "admin":
		agent, getErr := s.agentRepo.GetByID(ctx, claims.AgentID)
		if getErr != nil {
			if errors.Is(getErr, repository.ErrNotFound) {
				return nil, ErrUnauthorized
			}
			return nil, getErr
		}
		if !isActiveStatus(agent.Status) {
			return nil, ErrUnauthorized
		}
		if normalizeTokenVersion(claims.TokenVersion) != normalizeTokenVersion(agent.TokenVersion) {
			return nil, ErrUnauthorized
		}
	default:
		return nil, ErrUnauthorized
	}

	return claims, nil
}

func (s *AuthService) GetAgentByID(ctx context.Context, agentID uint64) (*model.Agent, error) {
	if agentID == 0 {
		return nil, ErrUnauthorized
	}
	agent, err := s.agentRepo.GetByID(ctx, agentID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	return agent, nil
}

// buildJWTVerifySecrets 支持主密钥+上一个密钥并行验签，便于平滑轮转。
func buildJWTVerifySecrets(primary string, previous string) [][]byte {
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

// EnsureSuperAdmin 幂等地创建/修正超级管理员账号，保证环境变量即期望状态。
func (s *AuthService) EnsureSuperAdmin(ctx context.Context) error {
	if s.superAdminEmail == "" || s.superAdminPassword == "" || s.superAdminDisplayName == "" {
		return fmt.Errorf("super admin env is required")
	}
	if err := security.ValidatePasswordPolicy(s.superAdminPassword); err != nil {
		return fmt.Errorf("invalid SUPER_ADMIN_PASSWORD: %w", err)
	}

	superAdmin, err := s.superAdminRepo.GetByEmail(ctx, s.superAdminEmail)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			if _, findAgentErr := s.agentRepo.GetByEmail(ctx, s.superAdminEmail); findAgentErr == nil {
				return fmt.Errorf("SUPER_ADMIN_EMAIL already exists in agents")
			} else if !errors.Is(findAgentErr, repository.ErrNotFound) {
				return findAgentErr
			}

			hash, hashErr := security.HashPassword(s.superAdminPassword, s.bcryptCost)
			if hashErr != nil {
				return hashErr
			}

			created := &model.SuperAdmin{
				Email:        s.superAdminEmail,
				PasswordHash: hash,
				DisplayName:  s.superAdminDisplayName,
				Status:       "active",
				TokenVersion: 1,
			}
			return s.superAdminRepo.Create(ctx, created)
		}
		return err
	}

	needSave := false
	if superAdmin.DisplayName != s.superAdminDisplayName {
		superAdmin.DisplayName = s.superAdminDisplayName
		needSave = true
	}
	if !isActiveStatus(superAdmin.Status) {
		superAdmin.Status = "active"
		needSave = true
	}
	if superAdmin.TokenVersion == 0 {
		superAdmin.TokenVersion = 1
		needSave = true
	}
	if err := security.ComparePassword(superAdmin.PasswordHash, s.superAdminPassword); err != nil {
		hash, hashErr := security.HashPassword(s.superAdminPassword, s.bcryptCost)
		if hashErr != nil {
			return hashErr
		}
		superAdmin.PasswordHash = hash
		needSave = true
	}

	if !needSave {
		return nil
	}
	return s.superAdminRepo.Save(ctx, superAdmin)
}

func normalizeTokenVersion(v uint64) uint64 {
	if v == 0 {
		return 1
	}
	return v
}

// isActiveStatus 统一状态语义，避免大小写导致行为分叉。
func isActiveStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "active")
}
