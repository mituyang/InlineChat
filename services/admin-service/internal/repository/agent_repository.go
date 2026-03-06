package repository

import (
	"context"
	"errors"
	"time"

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
	// defaultQueryTimeout 用于兜底查询超时，避免无期限占用连接池。
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewAgentRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormAgentRepository {
	return &GormAgentRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormAgentRepository) Create(ctx context.Context, agent *model.Agent) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Create(agent).Error
}

func (r *GormAgentRepository) Save(ctx context.Context, agent *model.Agent) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Save(agent).Error
}

func (r *GormAgentRepository) List(ctx context.Context, limit int, offset int) ([]model.Agent, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out []model.Agent
	err := db.
		Select("id", "email", "display_name", "site_id", "role", "status", "created_at", "updated_at").
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	return out, err
}

func (r *GormAgentRepository) GetByID(ctx context.Context, id uint64) (*model.Agent, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out model.Agent
	if err := db.
		Where("id = ?", id).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}
