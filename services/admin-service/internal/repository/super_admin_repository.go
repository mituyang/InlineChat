package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"inlinechat/services/admin-service/internal/model"
)

type SuperAdminRepository interface {
	GetByID(ctx context.Context, id uint64) (*model.SuperAdmin, error)
	GetByEmail(ctx context.Context, email string) (*model.SuperAdmin, error)
}

type GormSuperAdminRepository struct {
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewSuperAdminRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormSuperAdminRepository {
	return &GormSuperAdminRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormSuperAdminRepository) GetByID(ctx context.Context, id uint64) (*model.SuperAdmin, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out model.SuperAdmin
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

func (r *GormSuperAdminRepository) GetByEmail(ctx context.Context, email string) (*model.SuperAdmin, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out model.SuperAdmin
	if err := db.
		Where("email = ?", email).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}
