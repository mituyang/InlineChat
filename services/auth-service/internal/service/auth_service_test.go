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

func (r *fakeAgentRepository) GetByID(_ context.Context, id uint64) (*model.Agent, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	for _, item := range r.items {
		if item.ID != id {
			continue
		}
		copyAgent := *item
		return &copyAgent, nil
	}
	return nil, repository.ErrNotFound
}

type fakeSuperAdminRepository struct {
	items       map[string]*model.SuperAdmin
	createCalls int
	saveCalls   int
	createErr   error
	saveErr     error
	getErr      error
}

func newFakeSuperAdminRepository() *fakeSuperAdminRepository {
	return &fakeSuperAdminRepository{
		items: make(map[string]*model.SuperAdmin),
	}
}

func (r *fakeSuperAdminRepository) Create(_ context.Context, superAdmin *model.SuperAdmin) error {
	if r.createErr != nil {
		return r.createErr
	}
	r.createCalls++

	copyItem := *superAdmin
	r.items[strings.ToLower(copyItem.Email)] = &copyItem
	return nil
}

func (r *fakeSuperAdminRepository) Save(_ context.Context, superAdmin *model.SuperAdmin) error {
	if r.saveErr != nil {
		return r.saveErr
	}
	r.saveCalls++

	copyItem := *superAdmin
	r.items[strings.ToLower(copyItem.Email)] = &copyItem
	return nil
}

func (r *fakeSuperAdminRepository) GetByEmail(_ context.Context, email string) (*model.SuperAdmin, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}

	item, ok := r.items[strings.ToLower(email)]
	if !ok {
		return nil, repository.ErrNotFound
	}
	copyItem := *item
	return &copyItem, nil
}

func (r *fakeSuperAdminRepository) GetByID(_ context.Context, id uint64) (*model.SuperAdmin, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	for _, item := range r.items {
		if item.ID != id {
			continue
		}
		copyItem := *item
		return &copyItem, nil
	}
	return nil, repository.ErrNotFound
}

func newTestAuthService(
	agentRepo repository.AgentRepository,
	superAdminRepo repository.SuperAdminRepository,
	email string,
	password string,
	displayName string,
) *AuthService {
	return New(agentRepo, superAdminRepo, "test-secret", "", "test-issuer", time.Hour, 10, email, password, displayName)
}

func TestEnsureSuperAdminCreateWhenMissing(t *testing.T) {
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "Sup3rAdmin#2026!", "超级管理员")

	if err := svc.EnsureSuperAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureSuperAdmin failed: %v", err)
	}

	if superAdminRepo.createCalls != 1 {
		t.Fatalf("expected createCalls=1, got %d", superAdminRepo.createCalls)
	}
	if superAdminRepo.saveCalls != 0 {
		t.Fatalf("expected saveCalls=0, got %d", superAdminRepo.saveCalls)
	}

	created, ok := superAdminRepo.items["super@example.com"]
	if !ok {
		t.Fatalf("super admin not created")
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
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	oldHash, err := security.HashPassword("OldPassword123!", 10)
	if err != nil {
		t.Fatalf("hash password failed: %v", err)
	}
	superAdminRepo.items["super@example.com"] = &model.SuperAdmin{
		Email:        "super@example.com",
		PasswordHash: oldHash,
		DisplayName:  "旧名字",
		Status:       "inactive",
	}

	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "NewPassword123!", "新名字")
	if err := svc.EnsureSuperAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureSuperAdmin failed: %v", err)
	}

	if superAdminRepo.createCalls != 0 {
		t.Fatalf("expected createCalls=0, got %d", superAdminRepo.createCalls)
	}
	if superAdminRepo.saveCalls != 1 {
		t.Fatalf("expected saveCalls=1, got %d", superAdminRepo.saveCalls)
	}

	updated := superAdminRepo.items["super@example.com"]
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
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	hash, err := security.HashPassword("SamePassword123!", 10)
	if err != nil {
		t.Fatalf("hash password failed: %v", err)
	}
	superAdminRepo.items["super@example.com"] = &model.SuperAdmin{
		Email:        "super@example.com",
		PasswordHash: hash,
		DisplayName:  "超级管理员",
		Status:       "active",
		TokenVersion: 1,
	}

	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "SamePassword123!", "超级管理员")
	if err := svc.EnsureSuperAdmin(context.Background()); err != nil {
		t.Fatalf("EnsureSuperAdmin failed: %v", err)
	}

	if superAdminRepo.createCalls != 0 {
		t.Fatalf("expected createCalls=0, got %d", superAdminRepo.createCalls)
	}
	if superAdminRepo.saveCalls != 0 {
		t.Fatalf("expected saveCalls=0, got %d", superAdminRepo.saveCalls)
	}
}

func TestEnsureSuperAdminPropagatesRepositoryError(t *testing.T) {
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	superAdminRepo.getErr = errors.New("db unavailable")

	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "SamePassword123!", "超级管理员")
	err := svc.EnsureSuperAdmin(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "db unavailable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureSuperAdminRejectWeakPassword(t *testing.T) {
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "password12345!", "超级管理员")

	err := svc.EnsureSuperAdmin(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid SUPER_ADMIN_PASSWORD") {
		t.Fatalf("unexpected error: %v", err)
	}
	if superAdminRepo.createCalls != 0 {
		t.Fatalf("expected createCalls=0, got %d", superAdminRepo.createCalls)
	}
	if superAdminRepo.saveCalls != 0 {
		t.Fatalf("expected saveCalls=0, got %d", superAdminRepo.saveCalls)
	}
}

func TestEnsureSuperAdminRejectConflictWithAgentEmail(t *testing.T) {
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	agentRepo.items["super@example.com"] = &model.Agent{
		ID:           1001,
		Email:        "super@example.com",
		PasswordHash: "unused",
		DisplayName:  "客服A",
		Role:         "agent",
		Status:       "active",
		TokenVersion: 1,
	}
	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "Sup3rAdmin#2026!", "超级管理员")

	err := svc.EnsureSuperAdmin(context.Background())
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "SUPER_ADMIN_EMAIL already exists in agents") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTokenChecksTokenVersion(t *testing.T) {
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	agentRepo.items["agent@example.com"] = &model.Agent{
		ID:           1001,
		Email:        "agent@example.com",
		PasswordHash: "unused",
		DisplayName:  "客服A",
		Role:         "agent",
		Status:       "active",
		TokenVersion: 2,
	}
	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "Sup3rAdmin#2026!", "超级管理员")

	token, err := security.IssueToken([]byte("test-secret"), "test-issuer", time.Hour, 1001, "agent@example.com", "agent", 2)
	if err != nil {
		t.Fatalf("IssueToken failed: %v", err)
	}

	claims, err := svc.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}
	if claims.AgentID != 1001 {
		t.Fatalf("unexpected agent_id: %d", claims.AgentID)
	}

	agentRepo.items["agent@example.com"].TokenVersion = 3
	if _, err := svc.ValidateToken(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for token version mismatch, got: %v", err)
	}
}

func TestValidateTokenChecksStatus(t *testing.T) {
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	agentRepo.items["agent@example.com"] = &model.Agent{
		ID:           1002,
		Email:        "agent@example.com",
		PasswordHash: "unused",
		DisplayName:  "客服B",
		Role:         "agent",
		Status:       "inactive",
		TokenVersion: 1,
	}
	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "Sup3rAdmin#2026!", "超级管理员")

	token, err := security.IssueToken([]byte("test-secret"), "test-issuer", time.Hour, 1002, "agent@example.com", "agent", 1)
	if err != nil {
		t.Fatalf("IssueToken failed: %v", err)
	}
	if _, err := svc.ValidateToken(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for inactive agent, got: %v", err)
	}
}

func TestValidateTokenChecksSuperAdminTable(t *testing.T) {
	agentRepo := newFakeAgentRepository()
	superAdminRepo := newFakeSuperAdminRepository()
	superAdminRepo.items["super@example.com"] = &model.SuperAdmin{
		ID:           9001,
		Email:        "super@example.com",
		PasswordHash: "unused",
		DisplayName:  "超级管理员",
		Status:       "active",
		TokenVersion: 2,
	}
	svc := newTestAuthService(agentRepo, superAdminRepo, "super@example.com", "Sup3rAdmin#2026!", "超级管理员")

	token, err := security.IssueToken([]byte("test-secret"), "test-issuer", time.Hour, 9001, "super@example.com", "super_admin", 2)
	if err != nil {
		t.Fatalf("IssueToken failed: %v", err)
	}

	if _, err := svc.ValidateToken(context.Background(), token); err != nil {
		t.Fatalf("ValidateToken failed: %v", err)
	}

	superAdminRepo.items["super@example.com"].TokenVersion = 3
	if _, err := svc.ValidateToken(context.Background(), token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected ErrUnauthorized for stale token version, got: %v", err)
	}
}
