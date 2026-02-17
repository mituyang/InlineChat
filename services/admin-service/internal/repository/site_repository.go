package repository

import (
	"context"

	"gorm.io/gorm"

	"inlinechat/services/admin-service/internal/model"
)

type SiteRepository interface {
	Create(ctx context.Context, site *model.Site) error
	List(ctx context.Context, limit int, offset int) ([]model.Site, error)
}

type GormSiteRepository struct {
	db *gorm.DB
}

func NewSiteRepository(db *gorm.DB) *GormSiteRepository {
	return &GormSiteRepository{db: db}
}

func (r *GormSiteRepository) Create(ctx context.Context, site *model.Site) error {
	return r.db.WithContext(ctx).Create(site).Error
}

func (r *GormSiteRepository) List(ctx context.Context, limit int, offset int) ([]model.Site, error) {
	var out []model.Site
	err := r.db.WithContext(ctx).
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	return out, err
}
