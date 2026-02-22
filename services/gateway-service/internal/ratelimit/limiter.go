package ratelimit

import (
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type Limiter struct {
	mu          sync.Mutex
	entries     map[string]*entry
	ratePerSec  rate.Limit
	burst       int
	ttl         time.Duration
	maxEntries  int
	lastCleanup time.Time
}

type entry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func New(perMinute int, burst int, ttl time.Duration, maxEntries int) *Limiter {
	if perMinute <= 0 {
		perMinute = 60
	}
	if burst <= 0 {
		burst = 20
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	if maxEntries <= 0 {
		maxEntries = 100000
	}
	return &Limiter{
		entries:    make(map[string]*entry),
		ratePerSec: rate.Limit(float64(perMinute) / 60.0),
		burst:      burst,
		ttl:        ttl,
		maxEntries: maxEntries,
	}
}

func (l *Limiter) Allow(key string) bool {
	normalized := strings.TrimSpace(strings.ToLower(key))
	if normalized == "" {
		return true
	}
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	l.cleanupIfNeeded(now)
	item := l.entries[normalized]
	if item == nil {
		if len(l.entries) >= l.maxEntries {
			// 避免被恶意 key 撑爆内存，达到上限时按“放行”退化。
			return true
		}
		item = &entry{
			limiter:  rate.NewLimiter(l.ratePerSec, l.burst),
			lastSeen: now,
		}
		l.entries[normalized] = item
	}
	item.lastSeen = now
	return item.limiter.Allow()
}

func (l *Limiter) cleanupIfNeeded(now time.Time) {
	if !l.lastCleanup.IsZero() && now.Sub(l.lastCleanup) < time.Minute {
		return
	}
	expireBefore := now.Add(-l.ttl)
	for key, item := range l.entries {
		if item.lastSeen.Before(expireBefore) {
			delete(l.entries, key)
		}
	}
	l.lastCleanup = now
}
