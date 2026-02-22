package repository

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"inlinechat/services/admin-service/internal/model"
)

type AuditLogFilter struct {
	ActorAgentID uint64
	Action       string
	ResourceType string
}

type AuditLogRepository interface {
	Create(ctx context.Context, auditLog *model.AuditLog) error
	List(ctx context.Context, filter AuditLogFilter, limit int, offset int) ([]model.AuditLog, error)
}

type GormAuditLogRepository struct {
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewAuditLogRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormAuditLogRepository {
	return &GormAuditLogRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormAuditLogRepository) Create(ctx context.Context, auditLog *model.AuditLog) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Create(auditLog).Error
}

func (r *GormAuditLogRepository) List(ctx context.Context, filter AuditLogFilter, limit int, offset int) ([]model.AuditLog, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	query := db.Model(&model.AuditLog{})
	if filter.ActorAgentID > 0 {
		query = query.Where("actor_agent_id = ?", filter.ActorAgentID)
	}
	if action := strings.TrimSpace(filter.Action); action != "" {
		query = query.Where("action = ?", action)
	}
	if resourceType := strings.TrimSpace(filter.ResourceType); resourceType != "" {
		query = query.Where("resource_type = ?", resourceType)
	}

	var out []model.AuditLog
	err := query.
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	return out, err
}
