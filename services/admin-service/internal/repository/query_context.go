package repository

import (
	"context"
	"time"

	"gorm.io/gorm"
)

func resolveQueryTimeout(overrides ...time.Duration) time.Duration {
	if len(overrides) > 0 && overrides[0] > 0 {
		return overrides[0]
	}
	return 1500 * time.Millisecond
}

// dbWithContext 为未设置 deadline 的调用补统一超时，防止慢查询拖垮接口。
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
