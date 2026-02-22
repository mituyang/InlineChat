package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"inlinechat/services/admin-service/internal/model"
	"inlinechat/services/admin-service/internal/repository"
)

type fakeSiteRepository struct {
	items       []model.Site
	createCalls int
	saveCalls   int
	createErr   error
}

func (r *fakeSiteRepository) Create(_ context.Context, site *model.Site) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.createCalls++
	copySite := *site
	r.items = append(r.items, copySite)
	return nil
}

func (r *fakeSiteRepository) List(_ context.Context, _ int, _ int) ([]model.Site, error) {
	out := make([]model.Site, len(r.items))
	copy(out, r.items)
	return out, nil
}

func (r *fakeSiteRepository) Save(_ context.Context, site *model.Site) error {
	r.saveCalls++
	for i := range r.items {
		if r.items[i].SiteID == site.SiteID {
			r.items[i] = *site
			return nil
		}
	}
	r.items = append(r.items, *site)
	return nil
}

func (r *fakeSiteRepository) GetBySiteID(_ context.Context, siteID string) (*model.Site, error) {
	for i := range r.items {
		if r.items[i].SiteID == siteID {
			out := r.items[i]
			return &out, nil
		}
	}
	return nil, repository.ErrNotFound
}

func (r *fakeSiteRepository) GetByDomain(_ context.Context, domain string) (*model.Site, error) {
	for i := range r.items {
		if r.items[i].Domain == domain {
			out := r.items[i]
			return &out, nil
		}
	}
	return nil, repository.ErrNotFound
}

type fakeAgentRepository struct {
	items       []model.Agent
	createCalls int
	saveCalls   int
	createErr   error
}

func (r *fakeAgentRepository) Create(_ context.Context, agent *model.Agent) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.createCalls++
	copyAgent := *agent
	r.items = append(r.items, copyAgent)
	return nil
}

func (r *fakeAgentRepository) List(_ context.Context, _ int, _ int) ([]model.Agent, error) {
	out := make([]model.Agent, len(r.items))
	copy(out, r.items)
	return out, nil
}

func (r *fakeAgentRepository) Save(_ context.Context, agent *model.Agent) error {
	r.saveCalls++
	for i := range r.items {
		if r.items[i].ID == agent.ID {
			r.items[i] = *agent
			return nil
		}
	}
	r.items = append(r.items, *agent)
	return nil
}

func (r *fakeAgentRepository) GetByID(_ context.Context, id uint64) (*model.Agent, error) {
	for i := range r.items {
		if r.items[i].ID == id {
			out := r.items[i]
			return &out, nil
		}
	}
	return nil, repository.ErrNotFound
}

type fakeAuditLogRepository struct {
	items []model.AuditLog
}

func (r *fakeAuditLogRepository) Create(_ context.Context, auditLog *model.AuditLog) error {
	copyLog := *auditLog
	r.items = append(r.items, copyLog)
	return nil
}

func (r *fakeAuditLogRepository) List(_ context.Context, _ repository.AuditLogFilter, _ int, _ int) ([]model.AuditLog, error) {
	out := make([]model.AuditLog, len(r.items))
	copy(out, r.items)
	return out, nil
}

func newTestAdminService(siteRepo repository.SiteRepository, agentRepo repository.AgentRepository) *AdminService {
	return New(siteRepo, agentRepo, &fakeAuditLogRepository{}, 10)
}

func TestCreateAgentDefaultRoleAndHashPassword(t *testing.T) {
	siteRepo := &fakeSiteRepository{}
	agentRepo := &fakeAgentRepository{}
	svc := newTestAdminService(siteRepo, agentRepo)

	agent, err := svc.CreateAgent(context.Background(), CreateAgentInput{
		AgentID:     "0012",
		Email:       "Agent@Example.com",
		Password:    "Agent#Strong2026!",
		DisplayName: "客服A",
	})
	if err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	if agentRepo.createCalls != 1 {
		t.Fatalf("expected createCalls=1, got %d", agentRepo.createCalls)
	}
	if agent.Email != "agent@example.com" {
		t.Fatalf("unexpected normalized email: %s", agent.Email)
	}
	if agent.ID != 12 {
		t.Fatalf("unexpected agent id: %d", agent.ID)
	}
	if agent.Role != "agent" {
		t.Fatalf("unexpected role: %s", agent.Role)
	}
	if agent.Status != "active" {
		t.Fatalf("unexpected status: %s", agent.Status)
	}
	if agent.PasswordHash == "Agent#Strong2026!" {
		t.Fatalf("password hash should not be plain text")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(agent.PasswordHash), []byte("Agent#Strong2026!")); err != nil {
		t.Fatalf("password hash does not match: %v", err)
	}
}

func TestCreateAgentRejectInvalidRole(t *testing.T) {
	svc := newTestAdminService(&fakeSiteRepository{}, &fakeAgentRepository{})

	_, err := svc.CreateAgent(context.Background(), CreateAgentInput{
		AgentID:     "1001",
		Email:       "agent@example.com",
		Password:    "Agent#Strong2026!",
		DisplayName: "客服A",
		Role:        "super_admin",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid role") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateAgentMapDuplicateToConflict(t *testing.T) {
	agentRepo := &fakeAgentRepository{
		createErr: errors.New("Duplicate entry"),
	}
	svc := newTestAdminService(&fakeSiteRepository{}, agentRepo)

	_, err := svc.CreateAgent(context.Background(), CreateAgentInput{
		AgentID:     "1001",
		Email:       "agent@example.com",
		Password:    "Agent#Strong2026!",
		DisplayName: "客服A",
		Role:        "agent",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestCreateAgentRejectWeakPassword(t *testing.T) {
	svc := newTestAdminService(&fakeSiteRepository{}, &fakeAgentRepository{})

	_, err := svc.CreateAgent(context.Background(), CreateAgentInput{
		AgentID:     "1001",
		Email:       "agent@example.com",
		Password:    "password12345!",
		DisplayName: "客服A",
		Role:        "agent",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCreateAgentRejectInvalidAgentID(t *testing.T) {
	svc := newTestAdminService(&fakeSiteRepository{}, &fakeAgentRepository{})

	cases := []string{"", "12", "abc1", "10000", "0000"}
	for _, agentID := range cases {
		_, err := svc.CreateAgent(context.Background(), CreateAgentInput{
			AgentID:     agentID,
			Email:       "agent@example.com",
			Password:    "Agent#Strong2026!",
			DisplayName: "客服A",
			Role:        "agent",
		})
		if err == nil {
			t.Fatalf("expected error for agent_id=%q, got nil", agentID)
		}
	}
}

func TestCreateSiteNormalizeDomainAndGenerateKeys(t *testing.T) {
	siteRepo := &fakeSiteRepository{}
	svc := newTestAdminService(siteRepo, &fakeAgentRepository{})

	site, err := svc.CreateSite(context.Background(), CreateSiteInput{
		SiteID: "Shop_Main",
		Name:   " 商城站点 ",
		Domain: " Shop.Example.COM ",
	})
	if err != nil {
		t.Fatalf("CreateSite failed: %v", err)
	}

	if siteRepo.createCalls != 1 {
		t.Fatalf("expected createCalls=1, got %d", siteRepo.createCalls)
	}
	if site.Name != "商城站点" {
		t.Fatalf("unexpected normalized name: %q", site.Name)
	}
	if site.Domain != "shop.example.com" {
		t.Fatalf("unexpected normalized domain: %q", site.Domain)
	}
	if site.SiteID != "shop_main" {
		t.Fatalf("unexpected site_id: %s", site.SiteID)
	}
	if !strings.HasPrefix(site.WidgetKey, "wk_") {
		t.Fatalf("unexpected widget_key: %s", site.WidgetKey)
	}
}

func TestCreateSiteRequireSiteID(t *testing.T) {
	siteRepo := &fakeSiteRepository{}
	svc := newTestAdminService(siteRepo, &fakeAgentRepository{})

	_, err := svc.CreateSite(context.Background(), CreateSiteInput{
		SiteID: "",
		Name:   "商城站点",
		Domain: "shop.example.com",
	})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "site_id is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetSiteBySiteID(t *testing.T) {
	siteRepo := &fakeSiteRepository{
		items: []model.Site{
			{SiteID: "site_abc", Name: "测试站点"},
		},
	}
	svc := newTestAdminService(siteRepo, &fakeAgentRepository{})

	site, err := svc.GetSiteBySiteID(context.Background(), " site_abc ")
	if err != nil {
		t.Fatalf("GetSiteBySiteID failed: %v", err)
	}
	if site.SiteID != "site_abc" {
		t.Fatalf("unexpected site_id: %s", site.SiteID)
	}
}

func TestGetSiteBySiteIDNotFound(t *testing.T) {
	svc := newTestAdminService(&fakeSiteRepository{}, &fakeAgentRepository{})

	_, err := svc.GetSiteBySiteID(context.Background(), "site_missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestGetSiteByDomain(t *testing.T) {
	siteRepo := &fakeSiteRepository{
		items: []model.Site{
			{SiteID: "site_abc", Domain: "shop.example.com", Name: "测试站点"},
		},
	}
	svc := newTestAdminService(siteRepo, &fakeAgentRepository{})

	site, err := svc.GetSiteByDomain(context.Background(), " Shop.Example.com ")
	if err != nil {
		t.Fatalf("GetSiteByDomain failed: %v", err)
	}
	if site.Domain != "shop.example.com" {
		t.Fatalf("unexpected domain: %s", site.Domain)
	}
}

func TestGetSiteByDomainNotFound(t *testing.T) {
	svc := newTestAdminService(&fakeSiteRepository{}, &fakeAgentRepository{})

	_, err := svc.GetSiteByDomain(context.Background(), "missing.example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestForceAgentLogoutIncrementsTokenVersion(t *testing.T) {
	agentRepo := &fakeAgentRepository{
		items: []model.Agent{
			{ID: 12, Email: "a@example.com", Role: "agent", Status: "active", TokenVersion: 1},
		},
	}
	svc := newTestAdminService(&fakeSiteRepository{}, agentRepo)

	updated, err := svc.ForceAgentLogout(context.Background(), ForceAgentLogoutInput{AgentID: 12}, ActorContext{AgentID: 9001})
	if err != nil {
		t.Fatalf("ForceAgentLogout failed: %v", err)
	}
	if updated.TokenVersion != 2 {
		t.Fatalf("unexpected token_version: %d", updated.TokenVersion)
	}
}

func TestUpdateSiteStatus(t *testing.T) {
	siteRepo := &fakeSiteRepository{
		items: []model.Site{
			{SiteID: "site_demo", Status: "active"},
		},
	}
	svc := newTestAdminService(siteRepo, &fakeAgentRepository{})

	updated, err := svc.UpdateSiteStatus(context.Background(), UpdateSiteStatusInput{
		SiteID: "site_demo",
		Status: "disabled",
	}, ActorContext{AgentID: 9001})
	if err != nil {
		t.Fatalf("UpdateSiteStatus failed: %v", err)
	}
	if updated.Status != "disabled" {
		t.Fatalf("unexpected status: %s", updated.Status)
	}
	if siteRepo.saveCalls != 1 {
		t.Fatalf("expected saveCalls=1, got %d", siteRepo.saveCalls)
	}
}
