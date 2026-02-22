package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
	auditRepo  repository.AuditLogRepository
	bcryptCost int
}

type CreateSiteInput struct {
	SiteID string
	Name   string
	Domain string
}

type CreateAgentInput struct {
	AgentID     string
	Email       string
	Password    string
	DisplayName string
	Role        string
}

type ActorContext struct {
	AgentID   uint64
	Email     string
	Role      string
	IP        string
	UserAgent string
}

type UpdateSiteStatusInput struct {
	SiteID string
	Status string
}

type RotateSiteWidgetKeyInput struct {
	SiteID string
}

type UpdateAgentStatusInput struct {
	AgentID uint64
	Status  string
}

type ResetAgentPasswordInput struct {
	AgentID     uint64
	NewPassword string
}

type ForceAgentLogoutInput struct {
	AgentID uint64
}

type ListAuditLogsInput struct {
	Limit        int
	Offset       int
	ActorAgentID uint64
	Action       string
	ResourceType string
}

func New(siteRepo repository.SiteRepository, agentRepo repository.AgentRepository, auditRepo repository.AuditLogRepository, bcryptCost int) *AdminService {
	return &AdminService{siteRepo: siteRepo, agentRepo: agentRepo, auditRepo: auditRepo, bcryptCost: bcryptCost}
}

func (s *AdminService) CreateSite(ctx context.Context, in CreateSiteInput) (*model.Site, error) {
	return s.createSite(ctx, in, ActorContext{})
}

func (s *AdminService) CreateSiteWithActor(ctx context.Context, in CreateSiteInput, actor ActorContext) (*model.Site, error) {
	return s.createSite(ctx, in, actor)
}

func (s *AdminService) createSite(ctx context.Context, in CreateSiteInput, actor ActorContext) (*model.Site, error) {
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
	_ = s.writeAuditLog(ctx, actor, "site.create", "site", site.SiteID, fmt.Sprintf("创建站点 %s", site.SiteID))
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

func (s *AdminService) UpdateSiteStatus(ctx context.Context, in UpdateSiteStatusInput, actor ActorContext) (*model.Site, error) {
	siteID := strings.TrimSpace(in.SiteID)
	if siteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}
	status, err := normalizeSiteStatus(in.Status)
	if err != nil {
		return nil, err
	}

	site, err := s.siteRepo.GetBySiteID(ctx, siteID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	oldStatus := site.Status
	site.Status = status
	if err := s.siteRepo.Save(ctx, site); err != nil {
		return nil, err
	}
	_ = s.writeAuditLog(ctx, actor, "site.update_status", "site", site.SiteID, fmt.Sprintf("站点状态 %s -> %s", oldStatus, status))
	return site, nil
}

func (s *AdminService) RotateSiteWidgetKey(ctx context.Context, in RotateSiteWidgetKeyInput, actor ActorContext) (*model.Site, error) {
	siteID := strings.TrimSpace(in.SiteID)
	if siteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}

	site, err := s.siteRepo.GetBySiteID(ctx, siteID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	widgetKey, err := randomHex("wk", 16)
	if err != nil {
		return nil, err
	}

	site.WidgetKey = widgetKey
	if err := s.siteRepo.Save(ctx, site); err != nil {
		return nil, err
	}
	_ = s.writeAuditLog(ctx, actor, "site.rotate_widget_key", "site", site.SiteID, "轮换站点 widget_key")
	return site, nil
}

func (s *AdminService) CreateAgent(ctx context.Context, in CreateAgentInput) (*model.Agent, error) {
	return s.createAgent(ctx, in, ActorContext{})
}

func (s *AdminService) CreateAgentWithActor(ctx context.Context, in CreateAgentInput, actor ActorContext) (*model.Agent, error) {
	return s.createAgent(ctx, in, actor)
}

func (s *AdminService) createAgent(ctx context.Context, in CreateAgentInput, actor ActorContext) (*model.Agent, error) {
	agentID, err := normalizeAgentID(in.AgentID)
	if err != nil {
		return nil, err
	}

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
		ID:           agentID,
		Email:        email,
		PasswordHash: hash,
		DisplayName:  displayName,
		Role:         role,
		Status:       "active",
		TokenVersion: 1,
	}
	if err := s.agentRepo.Create(ctx, agent); err != nil {
		if isDuplicateError(err) {
			return nil, ErrConflict
		}
		return nil, err
	}
	_ = s.writeAuditLog(ctx, actor, "agent.create", "agent", formatAgentID(agent.ID), fmt.Sprintf("创建坐席 %s", formatAgentID(agent.ID)))
	return agent, nil
}

func (s *AdminService) ListAgents(ctx context.Context, limit int, offset int) ([]model.Agent, error) {
	return s.agentRepo.List(ctx, limit, offset)
}

func (s *AdminService) UpdateAgentStatus(ctx context.Context, in UpdateAgentStatusInput, actor ActorContext) (*model.Agent, error) {
	if in.AgentID == 0 {
		return nil, fmt.Errorf("agent_id is required")
	}
	status, err := normalizeAgentStatus(in.Status)
	if err != nil {
		return nil, err
	}

	agent, err := s.agentRepo.GetByID(ctx, in.AgentID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if agent.Role == "super_admin" && status != "active" {
		return nil, fmt.Errorf("invalid status transition for super_admin")
	}

	oldStatus := agent.Status
	agent.Status = status
	if status != "active" {
		agent.TokenVersion = nextTokenVersion(agent.TokenVersion)
	}
	if err := s.agentRepo.Save(ctx, agent); err != nil {
		return nil, err
	}
	_ = s.writeAuditLog(ctx, actor, "agent.update_status", "agent", formatAgentID(agent.ID), fmt.Sprintf("坐席状态 %s -> %s", oldStatus, status))
	return agent, nil
}

func (s *AdminService) ResetAgentPassword(ctx context.Context, in ResetAgentPasswordInput, actor ActorContext) (*model.Agent, error) {
	if in.AgentID == 0 {
		return nil, fmt.Errorf("agent_id is required")
	}
	if err := security.ValidatePasswordPolicy(in.NewPassword); err != nil {
		return nil, err
	}

	agent, err := s.agentRepo.GetByID(ctx, in.AgentID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	hash, err := security.HashPassword(in.NewPassword, s.bcryptCost)
	if err != nil {
		return nil, err
	}
	agent.PasswordHash = hash
	agent.TokenVersion = nextTokenVersion(agent.TokenVersion)
	if err := s.agentRepo.Save(ctx, agent); err != nil {
		return nil, err
	}
	_ = s.writeAuditLog(ctx, actor, "agent.reset_password", "agent", formatAgentID(agent.ID), "重置坐席密码并强制下线")
	return agent, nil
}

func (s *AdminService) ForceAgentLogout(ctx context.Context, in ForceAgentLogoutInput, actor ActorContext) (*model.Agent, error) {
	if in.AgentID == 0 {
		return nil, fmt.Errorf("agent_id is required")
	}

	agent, err := s.agentRepo.GetByID(ctx, in.AgentID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	agent.TokenVersion = nextTokenVersion(agent.TokenVersion)
	if err := s.agentRepo.Save(ctx, agent); err != nil {
		return nil, err
	}
	_ = s.writeAuditLog(ctx, actor, "agent.force_logout", "agent", formatAgentID(agent.ID), "强制坐席下线")
	return agent, nil
}

func (s *AdminService) ListAuditLogs(ctx context.Context, in ListAuditLogsInput) ([]model.AuditLog, error) {
	if s.auditRepo == nil {
		return []model.AuditLog{}, nil
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return nil, fmt.Errorf("invalid limit")
	}
	if in.Offset < 0 {
		return nil, fmt.Errorf("invalid offset")
	}
	filter := repository.AuditLogFilter{
		ActorAgentID: in.ActorAgentID,
		Action:       strings.TrimSpace(in.Action),
		ResourceType: strings.TrimSpace(in.ResourceType),
	}
	return s.auditRepo.List(ctx, filter, limit, in.Offset)
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

func normalizeAgentID(raw string) (uint64, error) {
	agentID := strings.TrimSpace(raw)
	if agentID == "" {
		return 0, fmt.Errorf("agent_id is required")
	}
	matched, err := regexp.MatchString(`^\d{4}$`, agentID)
	if err != nil {
		return 0, err
	}
	if !matched {
		return 0, fmt.Errorf("agent_id must be exactly 4 digits")
	}
	value, err := strconv.ParseUint(agentID, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid agent_id")
	}
	if value == 0 {
		return 0, fmt.Errorf("agent_id 0000 is not allowed")
	}
	return value, nil
}

func normalizeSiteStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status != "active" && status != "disabled" {
		return "", fmt.Errorf("invalid site status")
	}
	return status, nil
}

func normalizeAgentStatus(raw string) (string, error) {
	status := strings.ToLower(strings.TrimSpace(raw))
	if status != "active" && status != "inactive" {
		return "", fmt.Errorf("invalid agent status")
	}
	return status, nil
}

func nextTokenVersion(current uint64) uint64 {
	if current == 0 {
		return 2
	}
	return current + 1
}

func formatAgentID(id uint64) string {
	return fmt.Sprintf("%04d", id)
}

func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique")
}

func (s *AdminService) writeAuditLog(ctx context.Context, actor ActorContext, action string, resourceType string, resourceID string, summary string) error {
	if s.auditRepo == nil || strings.TrimSpace(action) == "" {
		return nil
	}
	if actor.AgentID == 0 {
		return nil
	}
	auditLog := &model.AuditLog{
		ActorAgentID: actor.AgentID,
		ActorEmail:   strings.TrimSpace(actor.Email),
		ActorRole:    strings.TrimSpace(actor.Role),
		Action:       strings.TrimSpace(action),
		ResourceType: strings.TrimSpace(resourceType),
		ResourceID:   strings.TrimSpace(resourceID),
		Summary:      strings.TrimSpace(summary),
		IP:           strings.TrimSpace(actor.IP),
		UserAgent:    strings.TrimSpace(actor.UserAgent),
	}
	if auditLog.ActorEmail == "" {
		auditLog.ActorEmail = "-"
	}
	if auditLog.ActorRole == "" {
		auditLog.ActorRole = "-"
	}
	return s.auditRepo.Create(ctx, auditLog)
}
