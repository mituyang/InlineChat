package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"inlinechat/services/auth-service/internal/model"
)

var ErrNotFound = errors.New("not found")

type AgentRepository interface {
	Create(ctx context.Context, agent *model.Agent) error
	Save(ctx context.Context, agent *model.Agent) error
	GetByEmail(ctx context.Context, email string) (*model.Agent, error)
	GetByID(ctx context.Context, id uint64) (*model.Agent, error)
}

type GormAgentRepository struct {
	// defaultQueryTimeout 用于给未设置 deadline 的调用补超时，防止慢查询拖垮请求链路。
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewAgentRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormAgentRepository {
	timeout := resolveQueryTimeout(defaultQueryTimeout...)
	return &GormAgentRepository{db: db, defaultQueryTimeout: timeout}
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

func (r *GormAgentRepository) GetByEmail(ctx context.Context, email string) (*model.Agent, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var agent model.Agent
	if err := db.Where("email = ?", email).First(&agent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &agent, nil
}

func (r *GormAgentRepository) GetByID(ctx context.Context, id uint64) (*model.Agent, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var agent model.Agent
	if err := db.Where("id = ?", id).First(&agent).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &agent, nil
}

func resolveQueryTimeout(overrides ...time.Duration) time.Duration {
	if len(overrides) > 0 && overrides[0] > 0 {
		return overrides[0]
	}
	return 1500 * time.Millisecond
}

// dbWithContext 统一把 context 超时策略应用到 GORM 查询。
func dbWithContext(db *gorm.DB, ctx context.Context, defaultQueryTimeout time.Duration) (*gorm.DB, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if defaultQueryTimeout > 0 {
		if _, ok := ctx.Deadline(); !ok {
			timeoutCtx, cancel := context.WithTimeout(ctx, defaultQueryTimeout)
			return db.WithContext(timeoutCtx), cancel
		}
	}
	return db.WithContext(ctx), func() {}
}
