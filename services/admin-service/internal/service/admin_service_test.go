package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
	"inlinechat/services/admin-service/internal/model"
)

type fakeSiteRepository struct {
	items       []model.Site
	createCalls int
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

type fakeAgentRepository struct {
	items       []model.Agent
	createCalls int
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

func TestCreateAgentDefaultRoleAndHashPassword(t *testing.T) {
	siteRepo := &fakeSiteRepository{}
	agentRepo := &fakeAgentRepository{}
	svc := New(siteRepo, agentRepo, 10)

	agent, err := svc.CreateAgent(context.Background(), CreateAgentInput{
		Email:       "Agent@Example.com",
		Password:    "Agent12345!",
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
	if agent.Role != "agent" {
		t.Fatalf("unexpected role: %s", agent.Role)
	}
	if agent.Status != "active" {
		t.Fatalf("unexpected status: %s", agent.Status)
	}
	if agent.PasswordHash == "Agent12345!" {
		t.Fatalf("password hash should not be plain text")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(agent.PasswordHash), []byte("Agent12345!")); err != nil {
		t.Fatalf("password hash does not match: %v", err)
	}
}

func TestCreateAgentRejectInvalidRole(t *testing.T) {
	svc := New(&fakeSiteRepository{}, &fakeAgentRepository{}, 10)

	_, err := svc.CreateAgent(context.Background(), CreateAgentInput{
		Email:       "agent@example.com",
		Password:    "Agent12345!",
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
	svc := New(&fakeSiteRepository{}, agentRepo, 10)

	_, err := svc.CreateAgent(context.Background(), CreateAgentInput{
		Email:       "agent@example.com",
		Password:    "Agent12345!",
		DisplayName: "客服A",
		Role:        "agent",
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got %v", err)
	}
}

func TestCreateSiteNormalizeDomainAndGenerateKeys(t *testing.T) {
	siteRepo := &fakeSiteRepository{}
	svc := New(siteRepo, &fakeAgentRepository{}, 10)

	site, err := svc.CreateSite(context.Background(), CreateSiteInput{
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
	if !strings.HasPrefix(site.SiteID, "site_") {
		t.Fatalf("unexpected site_id: %s", site.SiteID)
	}
	if !strings.HasPrefix(site.WidgetKey, "wk_") {
		t.Fatalf("unexpected widget_key: %s", site.WidgetKey)
	}
}
