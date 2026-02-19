package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"inlinechat/services/auth-service/internal/model"
	"inlinechat/services/auth-service/internal/repository"
	"inlinechat/services/auth-service/internal/security"
)

type fakeAgentRepository struct {
	items       map[string]*model.Agent
	createCalls int
	saveCalls   int
	createErr   error
	saveErr     error
	getErr      error
}

func newFakeAgentRepository() *fakeAgentRepository {
	return &fakeAgentRepository{
		items: make(map[string]*model.Agent),
	}
}

func (r *fakeAgentRepository) Create(_ context.Context, agent *model.Agent) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.createCalls++

	copyAgent := *agent
	r.items[strings.ToLower(copyAgent.Email)] = &copyAgent
	return nil
}

func (r *fakeAgentRepository) Save(_ context.Context, agent *model.Agent) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saveCalls++

	copyAgent := *agent
	r.items[strings.ToLower(copyAgent.Email)] = &copyAgent
	return nil
}

func (r *fakeAgentRepository) GetByEmail(_ context.Context, email string) (*model.Agent, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}

	item, ok := r.items[strings.ToLower(email)]
	if !ok {
		return nil, repository.ErrNotFound
	}
	copyAgent := *item
	return &copyAgent, nil
}

func newTestAuthService(repo repository.AgentRepository, email string, password string, displayName string) *AuthService {
	return New(repo, "test-secret", "test-issuer", time.Hour, 10, email, password, displayName)
}

func TestEnsureSuperAdminCreateWhenMissing(t *testing.T) {
	repo := newFakeAgentRepository()
	svc := newTestAuthService(repo, "super@example.com", "Sup3rAdmin#2026!", "超级管理员")

	if err := svc.EnsureSuperAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureSuperAdmin failed: %v", err)
	}

	if repo.createCalls != 1 {
		t.Fatalf("expected createCalls=1, got %d", repo.createCalls)
	}
	if repo.saveCalls != 0 {
		t.Fatalf("expected saveCalls=0, got %d", repo.saveCalls)
	}

	created, ok := repo.items["super@example.com"]
	if !ok {
		t.Fatalf("super admin not created")
	}
	if created.Role != "super_admin" {
		t.Fatalf("unexpected role: %s", created.Role)
	}
	if created.Status != "active" {
		t.Fatalf("unexpected status: %s", created.Status)
	}
	if created.DisplayName != "超级管理员" {
		t.Fatalf("unexpected display_name: %s", created.DisplayName)
	}
	if created.PasswordHash == "Sup3rAdmin#2026!" {
		t.Fatalf("password hash should not be plain text")
	}
	if err := security.ComparePassword(created.PasswordHash, "Sup3rAdmin#2026!"); err != nil {
		t.Fatalf("password hash does not match: %v", err)
	}
}

func TestEnsureSuperAdminUpdateWhenDrifted(t *testing.T) {
	repo := newFakeAgentRepository()
	oldHash, err := security.HashPassword("OldPassword123!", 10)
	if err != nil {
		t.Fatalf("hash password failed: %v", err)
	}
	repo.items["super@example.com"] = &model.Agent{
		Email:        "super@example.com",
		PasswordHash: oldHash,
		DisplayName:  "旧名字",
		Role:         "agent",
		Status:       "inactive",
	}

	svc := newTestAuthService(repo, "super@example.com", "NewPassword123!", "新名字")
	if err := svc.EnsureSuperAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureSuperAdmin failed: %v", err)
	}

	if repo.createCalls != 0 {
		t.Fatalf("expected createCalls=0, got %d", repo.createCalls)
	}
	if repo.saveCalls != 1 {
		t.Fatalf("expected saveCalls=1, got %d", repo.saveCalls)
	}

	updated := repo.items["super@example.com"]
	if updated.Role != "super_admin" {
		t.Fatalf("unexpected role: %s", updated.Role)
	}
	if updated.Status != "active" {
		t.Fatalf("unexpected status: %s", updated.Status)
	}
	if updated.DisplayName != "新名字" {
		t.Fatalf("unexpected display_name: %s", updated.DisplayName)
	}
	if err := security.ComparePassword(updated.PasswordHash, "NewPassword123!"); err != nil {
		t.Fatalf("password not updated: %v", err)
	}
}

func TestEnsureSuperAdminNoopWhenAlreadyAligned(t *testing.T) {
	repo := newFakeAgentRepository()
	hash, err := security.HashPassword("SamePassword123!", 10)
	if err != nil {
		t.Fatalf("hash password failed: %v", err)
	}
	repo.items["super@example.com"] = &model.Agent{
		Email:        "super@example.com",
		PasswordHash: hash,
		DisplayName:  "超级管理员",
		Role:         "super_admin",
		Status:       "active",
	}

	svc := newTestAuthService(repo, "super@example.com", "SamePassword123!", "超级管理员")
	if err := svc.EnsureSuperAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureSuperAdmin failed: %v", err)
	}

	if repo.createCalls != 0 {
		t.Fatalf("expected createCalls=0, got %d", repo.createCalls)
	}
	if repo.saveCalls != 0 {
		t.Fatalf("expected saveCalls=0, got %d", repo.saveCalls)
	}
}

func TestEnsureSuperAdminPropagatesRepositoryError(t *testing.T) {
	repo := newFakeAgentRepository()
	repo.getErr = errors.New("db unavailable")

	svc := newTestAuthService(repo, "super@example.com", "SamePassword123!", "超级管理员")
	err := svc.EnsureSuperAdmin(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "db unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureSuperAdminRejectWeakPassword(t *testing.T) {
	repo := newFakeAgentRepository()
	svc := newTestAuthService(repo, "super@example.com", "password12345!", "超级管理员")

	err := svc.EnsureSuperAdmin(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid SUPER_ADMIN_PASSWORD") {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.createCalls != 0 {
		t.Fatalf("expected createCalls=0, got %d", repo.createCalls)
	}
	if repo.saveCalls != 0 {
		t.Fatalf("expected saveCalls=0, got %d", repo.saveCalls)
	}
}
