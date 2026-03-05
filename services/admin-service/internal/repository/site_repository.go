package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"inlinechat/services/admin-service/internal/model"
)

var ErrNotFound = errors.New("not found")

type SiteRepository interface {
	Create(ctx context.Context, site *model.Site) error
	Save(ctx context.Context, site *model.Site) error
	List(ctx context.Context, limit int, offset int) ([]model.Site, error)
	GetBySiteID(ctx context.Context, siteID string) (*model.Site, error)
	GetByDomain(ctx context.Context, domain string) (*model.Site, error)
}

type GormSiteRepository struct {
	// defaultQueryTimeout 用于兜底查询超时，避免无期限占用连接池。
	db                  *gorm.DB
	defaultQueryTimeout time.Duration
}

func NewSiteRepository(db *gorm.DB, defaultQueryTimeout ...time.Duration) *GormSiteRepository {
	return &GormSiteRepository{
		db:                  db,
		defaultQueryTimeout: resolveQueryTimeout(defaultQueryTimeout...),
	}
}

func (r *GormSiteRepository) Create(ctx context.Context, site *model.Site) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Create(site).Error
}

func (r *GormSiteRepository) Save(ctx context.Context, site *model.Site) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	return db.Save(site).Error
}

func (r *GormSiteRepository) List(ctx context.Context, limit int, offset int) ([]model.Site, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out []model.Site
	err := db.
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	return out, err
}

func (r *GormSiteRepository) GetBySiteID(ctx context.Context, siteID string) (*model.Site, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out model.Site
	if err := db.
		Where("site_id = ?", siteID).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}

func (r *GormSiteRepository) GetByDomain(ctx context.Context, domain string) (*model.Site, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out model.Site
	if err := db.
		Where("domain = ?", domain).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &out, nil
}
