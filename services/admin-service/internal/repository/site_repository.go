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
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("DomainItems", "Domains").Create(site).Error; err != nil {
			return err
		}
		return syncSiteDomainsTx(tx, site.SiteID, site.Domains)
	})
}

func (r *GormSiteRepository) Save(ctx context.Context, site *model.Site) error {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	if site == nil || site.Domains == nil {
		return db.Omit("DomainItems", "Domains").Save(site).Error
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Omit("DomainItems", "Domains").Save(site).Error; err != nil {
			return err
		}
		return syncSiteDomainsTx(tx, site.SiteID, site.Domains)
	})
}

func (r *GormSiteRepository) List(ctx context.Context, limit int, offset int) ([]model.Site, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out []model.Site
	err := db.
		Preload("DomainItems", func(tx *gorm.DB) *gorm.DB {
			return tx.Order("domain ASC")
		}).
		Order("id DESC").
		Limit(limit).
		Offset(offset).
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	populateSiteDomains(out)
	return out, nil
}

func (r *GormSiteRepository) GetBySiteID(ctx context.Context, siteID string) (*model.Site, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out model.Site
	if err := db.
		Preload("DomainItems", func(tx *gorm.DB) *gorm.DB {
			return tx.Order("domain ASC")
		}).
		Where("site_id = ?", siteID).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	populateSingleSiteDomains(&out)
	return &out, nil
}

func (r *GormSiteRepository) GetByDomain(ctx context.Context, domain string) (*model.Site, error) {
	db, cancel := dbWithContext(r.db, ctx, r.defaultQueryTimeout)
	defer cancel()
	var out model.Site
	if err := db.
		Joins("JOIN site_domains ON site_domains.site_id = sites.site_id").
		Preload("DomainItems", func(tx *gorm.DB) *gorm.DB {
			return tx.Order("domain ASC")
		}).
		Where("site_domains.domain = ?", domain).
		First(&out).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	populateSingleSiteDomains(&out)
	return &out, nil
}

func buildSiteDomainModels(siteID string, domains []string) []model.SiteDomain {
	items := make([]model.SiteDomain, 0, len(domains))
	for _, domain := range domains {
		items = append(items, model.SiteDomain{
			SiteID: siteID,
			Domain: domain,
		})
	}
	return items
}

func syncSiteDomainsTx(tx *gorm.DB, siteID string, domains []string) error {
	if err := tx.Where("site_id = ?", siteID).Delete(&model.SiteDomain{}).Error; err != nil {
		return err
	}
	domainItems := buildSiteDomainModels(siteID, domains)
	if len(domainItems) == 0 {
		return nil
	}
	return tx.Create(&domainItems).Error
}

func populateSiteDomains(items []model.Site) {
	for i := range items {
		populateSingleSiteDomains(&items[i])
	}
}

func populateSingleSiteDomains(site *model.Site) {
	if site == nil {
		return
	}
	site.Domains = make([]string, 0, len(site.DomainItems))
	for i := range site.DomainItems {
		site.Domains = append(site.Domains, site.DomainItems[i].Domain)
	}
}
