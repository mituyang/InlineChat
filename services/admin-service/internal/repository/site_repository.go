package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"inlinechat/services/admin-service/internal/model"
)

var ErrNotFound = errors.New("not found")

type SiteRepository interface {
	Create(ctx context.Context, site *model.Site) error
	List(ctx context.Context, limit int, offset int) ([]model.Site, error)
	GetBySiteID(ctx context.Context, siteID string) (*model.Site, error)
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

func (r *GormSiteRepository) GetBySiteID(ctx context.Context, siteID string) (*model.Site, error) {
	var out model.Site
	if err := r.db.WithContext(ctx).
		Where("site_id = ?", siteID).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}
