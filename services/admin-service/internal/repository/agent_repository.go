package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"inlinechat/services/admin-service/internal/model"
)

type AgentRepository interface {
	Create(ctx context.Context, agent *model.Agent) error
	Save(ctx context.Context, agent *model.Agent) error
	List(ctx context.Context, limit int, offset int) ([]model.Agent, error)
	GetByID(ctx context.Context, id uint64) (*model.Agent, error)
}

type GormAgentRepository struct {
	db *gorm.DB
}

func NewAgentRepository(db *gorm.DB) *GormAgentRepository {
	return &GormAgentRepository{db: db}
}

func (r *GormAgentRepository) Create(ctx context.Context, agent *model.Agent) error {
	return r.db.WithContext(ctx).Create(agent).Error
}

func (r *GormAgentRepository) Save(ctx context.Context, agent *model.Agent) error {
	return r.db.WithContext(ctx).Save(agent).Error
}

func (r *GormAgentRepository) List(ctx context.Context, limit int, offset int) ([]model.Agent, error) {
	var out []model.Agent
	err := r.db.WithContext(ctx).
		Select("id", "email", "display_name", "role", "status", "created_at", "updated_at").
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	return out, err
}

func (r *GormAgentRepository) GetByID(ctx context.Context, id uint64) (*model.Agent, error) {
	var out model.Agent
	if err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}
