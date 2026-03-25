package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"inlinechat/services/admin-service/internal/model"
)

type SiteAIConfigRepository interface {
	GetBySiteID(ctx context.Context, siteID string) (*model.SiteAIConfig, error)
	Upsert(ctx context.Context, config *model.SiteAIConfig) error
}

type GormSiteAIConfigRepository struct {
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewSiteAIConfigRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormSiteAIConfigRepository {
	return &GormSiteAIConfigRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormSiteAIConfigRepository) GetBySiteID(ctx context.Context, siteID string) (*model.SiteAIConfig, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()

	var out model.SiteAIConfig
	if err := db.Where("site_id = ?", siteID).First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *GormSiteAIConfigRepository) Upsert(ctx context.Context, config *model.SiteAIConfig) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()

	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "site_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "reply_mode"}),
	}).Create(config).Error
}
