package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"inlinechat/services/auth-service/internal/model"
)

var ErrNotFound = errors.New("not found")

type AgentRepository interface {
	Create(ctx context.Context, agent *model.Agent) error
	Save(ctx context.Context, agent *model.Agent) error
	GetByEmail(ctx context.Context, email string) (*model.Agent, error)
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

func (r *GormAgentRepository) GetByEmail(ctx context.Context, email string) (*model.Agent, error) {
	var agent model.Agent
	if err := r.db.WithContext(ctx).Where("email = ?", email).First(&agent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &agent, nil
}
