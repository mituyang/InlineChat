package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"inlinechat/services/admin-service/internal/model"
	"inlinechat/services/admin-service/internal/repository"
	"inlinechat/services/admin-service/internal/security"
)

var ErrConflict = errors.New("conflict")
var ErrNotFound = errors.New("not found")

type AdminService struct {
	siteRepo   repository.SiteRepository
	agentRepo  repository.AgentRepository
	bcryptCost int
}

type CreateSiteInput struct {
	SiteID string
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
	siteID, err := normalizeSiteID(in.SiteID)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(in.Name)
	domain := strings.TrimSpace(strings.ToLower(in.Domain))
	if name == "" || domain == "" {
		return nil, fmt.Errorf("name and domain are required")
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

func (s *AdminService) GetSiteBySiteID(ctx context.Context, siteID string) (*model.Site, error) {
	normalizedSiteID := strings.TrimSpace(siteID)
	if normalizedSiteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}

	site, err := s.siteRepo.GetBySiteID(ctx, normalizedSiteID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return site, nil
}

func (s *AdminService) GetSiteByDomain(ctx context.Context, domain string) (*model.Site, error) {
	normalizedDomain := strings.TrimSpace(strings.ToLower(domain))
	if normalizedDomain == "" {
		return nil, fmt.Errorf("domain is required")
	}

	site, err := s.siteRepo.GetByDomain(ctx, normalizedDomain)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return site, nil
}

func (s *AdminService) CreateAgent(ctx context.Context, in CreateAgentInput) (*model.Agent, error) {
	email := strings.TrimSpace(strings.ToLower(in.Email))
	displayName := strings.TrimSpace(in.DisplayName)
	password := in.Password
	role := strings.TrimSpace(strings.ToLower(in.Role))
	if role == "" {
		role = "agent"
	}
	if role != "agent" {
		return nil, fmt.Errorf("invalid role")
	}
	if email == "" || displayName == "" {
		return nil, fmt.Errorf("email and display_name are required")
	}
	if err := security.ValidatePasswordPolicy(password); err != nil {
		return nil, err
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

func normalizeSiteID(raw string) (string, error) {
	siteID := strings.TrimSpace(strings.ToLower(raw))
	if siteID == "" {
		return "", fmt.Errorf("site_id is required")
	}
	if len(siteID) < 4 || len(siteID) > 64 {
		return "", fmt.Errorf("site_id length must be between 4 and 64")
	}
	// 商业化场景下建议显式、可读、可审计；仅允许小写字母、数字、下划线、连字符。
	matched, err := regexp.MatchString("^[a-z0-9][a-z0-9_-]*$", siteID)
	if err != nil {
		return "", err
	}
	if !matched {
		return "", fmt.Errorf("site_id format is invalid")
	}
	return siteID, nil
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique")
}
