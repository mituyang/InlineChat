package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"inlinechat/services/admin-service/internal/model"
	"inlinechat/services/admin-service/internal/repository"
	"inlinechat/services/admin-service/internal/security"
)

var ErrConflict = errors.New("conflict")

type AdminService struct {
	siteRepo   repository.SiteRepository
	agentRepo  repository.AgentRepository
	bcryptCost int
}

type CreateSiteInput struct {
	Name   string
	Domain string
}

type CreateAgentInput struct {
	Email       string
	Password    string
	DisplayName string
	Role        string
}

func New(siteRepo repository.SiteRepository, agentRepo repository.AgentRepository, bcryptCost int) *AdminService {
	return &AdminService{siteRepo: siteRepo, agentRepo: agentRepo, bcryptCost: bcryptCost}
}

func (s *AdminService) CreateSite(ctx context.Context, in CreateSiteInput) (*model.Site, error) {
	name := strings.TrimSpace(in.Name)
	domain := strings.TrimSpace(strings.ToLower(in.Domain))
	if name == "" || domain == "" {
		return nil, fmt.Errorf("name and domain are required")
	}

	siteID, err := randomHex("site", 8)
	if err != nil {
		return nil, err
	}
	widgetKey, err := randomHex("wk", 16)
	if err != nil {
		return nil, err
	}

	site := &model.Site{
		SiteID:    siteID,
		Name:      name,
		Domain:    domain,
		WidgetKey: widgetKey,
		Status:    "active",
	}
	if err := s.siteRepo.Create(ctx, site); err != nil {
		if isDuplicateError(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return site, nil
}

func (s *AdminService) ListSites(ctx context.Context, limit int, offset int) ([]model.Site, error) {
	return s.siteRepo.List(ctx, limit, offset)
}

func (s *AdminService) CreateAgent(ctx context.Context, in CreateAgentInput) (*model.Agent, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	displayName := strings.TrimSpace(in.DisplayName)
	password := strings.TrimSpace(in.Password)
	role := strings.TrimSpace(strings.ToLower(in.Role))
	if role == "" {
		role = "agent"
	}
	if role != "agent" {
		return nil, fmt.Errorf("invalid role")
	}
	if email == "" || displayName == "" || password == "" {
		return nil, fmt.Errorf("email display_name password are required")
	}

	hash, err := security.HashPassword(password, s.bcryptCost)
	if err != nil {
		return nil, err
	}

	agent := &model.Agent{
		Email:        email,
		PasswordHash: hash,
		DisplayName:  displayName,
		Role:         role,
		Status:       "active",
	}
	if err := s.agentRepo.Create(ctx, agent); err != nil {
		if isDuplicateError(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	return agent, nil
}

func (s *AdminService) ListAgents(ctx context.Context, limit int, offset int) ([]model.Agent, error) {
	return s.agentRepo.List(ctx, limit, offset)
}

func randomHex(prefix string, bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(buf), nil
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique")
}
