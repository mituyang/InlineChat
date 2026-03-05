package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"inlinechat/services/auth-service/internal/model"
)

type SuperAdminRepository interface {
	Create(ctx context.Context, superAdmin *model.SuperAdmin) error
	Save(ctx context.Context, superAdmin *model.SuperAdmin) error
	GetByEmail(ctx context.Context, email string) (*model.SuperAdmin, error)
	GetByID(ctx context.Context, id uint64) (*model.SuperAdmin, error)
}

type GormSuperAdminRepository struct {
	// 与 agent repository 保持同一超时策略，便于统一容量治理。
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewSuperAdminRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormSuperAdminRepository {
	timeout := resolveQueryTimeout(defaultQueryTimeout...)
	return &GormSuperAdminRepository{db: db, defaultQueryTimeout: timeout}
}

func (r *GormSuperAdminRepository) Create(ctx context.Context, superAdmin *model.SuperAdmin) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Create(superAdmin).Error
}

func (r *GormSuperAdminRepository) Save(ctx context.Context, superAdmin *model.SuperAdmin) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Save(superAdmin).Error
}

func (r *GormSuperAdminRepository) GetByEmail(ctx context.Context, email string) (*model.SuperAdmin, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var superAdmin model.SuperAdmin
	if err := db.Where("email = ?", email).First(&superAdmin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &superAdmin, nil
}

func (r *GormSuperAdminRepository) GetByID(ctx context.Context, id uint64) (*model.SuperAdmin, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var superAdmin model.SuperAdmin
	if err := db.Where("id = ?", id).First(&superAdmin).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &superAdmin, nil
}
