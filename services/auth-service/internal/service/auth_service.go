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
	ErrBootstrapDisabled = errors.New("bootstrap admin is disabled")
)

type AuthService struct {
	repo                  repository.AgentRepository
	jwtSecret             []byte
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

func New(
	repo repository.AgentRepository,
	jwtSecret string,
	jwtIssuer string,
	jwtExpire time.Duration,
	bcryptCost int,
	superAdminEmail string,
	superAdminPassword string,
	superAdminDisplayName string,
) *AuthService {
	return &AuthService{
		repo:                  repo,
		jwtSecret:             []byte(jwtSecret),
		jwtIssuer:             jwtIssuer,
		jwtExpire:             jwtExpire,
		bcryptCost:            bcryptCost,
		superAdminEmail:       strings.ToLower(strings.TrimSpace(superAdminEmail)),
		superAdminPassword:    strings.TrimSpace(superAdminPassword),
		superAdminDisplayName: strings.TrimSpace(superAdminDisplayName),
	}
}

func (s *AuthService) Login(ctx context.Context, in LoginInput) (*AuthResult, error) {
	agent, err := s.repo.GetByEmail(ctx, strings.ToLower(strings.TrimSpace(in.Email)))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrInvalidCredential
		}
		return nil, err
	}
	if agent.Status != "active" {
		return nil, ErrUnauthorized
	}
	if err := security.ComparePassword(agent.PasswordHash, in.Password); err != nil {
		return nil, ErrInvalidCredential
	}

	token, err := security.IssueToken(s.jwtSecret, s.jwtIssuer, s.jwtExpire, agent.ID, agent.Email, agent.Role)
	if err != nil {
		return nil, err
	}

	return &AuthResult{Token: token, Agent: *agent}, nil
}

func (s *AuthService) ParseToken(token string) (*security.Claims, error) {
	return security.ParseToken(s.jwtSecret, s.jwtIssuer, token)
}

func (s *AuthService) EnsureSuperAdmin(ctx context.Context) error {
	if s.superAdminEmail == "" || s.superAdminPassword == "" || s.superAdminDisplayName == "" {
		return fmt.Errorf("super admin env is required")
	}

	agent, err := s.repo.GetByEmail(ctx, s.superAdminEmail)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			hash, hashErr := security.HashPassword(s.superAdminPassword, s.bcryptCost)
			if hashErr != nil {
				return hashErr
			}

			created := &model.Agent{
				Email:        s.superAdminEmail,
				PasswordHash: hash,
				DisplayName:  s.superAdminDisplayName,
				Role:         "super_admin",
				Status:       "active",
			}
			return s.repo.Create(ctx, created)
		}
		return err
	}

	needSave := false
	if agent.DisplayName != s.superAdminDisplayName {
		agent.DisplayName = s.superAdminDisplayName
		needSave = true
	}
	if agent.Role != "super_admin" {
		agent.Role = "super_admin"
		needSave = true
	}
	if agent.Status != "active" {
		agent.Status = "active"
		needSave = true
	}
	if err := security.ComparePassword(agent.PasswordHash, s.superAdminPassword); err != nil {
		hash, hashErr := security.HashPassword(s.superAdminPassword, s.bcryptCost)
		if hashErr != nil {
			return hashErr
		}
		agent.PasswordHash = hash
		needSave = true
	}

	if !needSave {
		return nil
	}
	return s.repo.Save(ctx, agent)
}
