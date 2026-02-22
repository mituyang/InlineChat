package repository

import (
	"context"
	"strings"

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
	db *gorm.DB
}

func NewAuditLogRepository(db *gorm.DB) *GormAuditLogRepository {
	return &GormAuditLogRepository{db: db}
}

func (r *GormAuditLogRepository) Create(ctx context.Context, auditLog *model.AuditLog) error {
	return r.db.WithContext(ctx).Create(auditLog).Error
}

func (r *GormAuditLogRepository) List(ctx context.Context, filter AuditLogFilter, limit int, offset int) ([]model.AuditLog, error) {
	query := r.db.WithContext(ctx).Model(&model.AuditLog{})
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
