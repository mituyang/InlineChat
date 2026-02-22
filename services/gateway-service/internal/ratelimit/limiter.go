package ratelimit

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

type DistributedCounter interface {
	IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error)
}

type Limiter struct {
	mu          sync.Mutex
	entries     map[string]*entry
	perMinute   int
	ratePerSec  rate.Limit
	burst       int
	ttl         time.Duration
	maxEntries  int
	lastCleanup time.Time

	distributedCounter DistributedCounter
	distributedPrefix  string
	distributedWindow  time.Duration
	distributedTimeout time.Duration
	distributedLimit   int
	circuitMu          sync.Mutex
	circuitOpenWindow  time.Duration
	circuitFailCount   int
	circuitFailures    int
	circuitOpenUntil   time.Time
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
		entries:          make(map[string]*entry),
		perMinute:        perMinute,
		ratePerSec:       rate.Limit(float64(perMinute) / 60.0),
		burst:            burst,
		ttl:              ttl,
		maxEntries:       maxEntries,
		distributedLimit: perMinute + burst,
	}
}

func (l *Limiter) EnableDistributedCounter(counter DistributedCounter, prefix string, window time.Duration, timeout time.Duration) {
	l.EnableDistributedCounterWithCircuit(counter, prefix, window, timeout, 3, 30*time.Second)
}

func (l *Limiter) EnableDistributedCounterWithCircuit(counter DistributedCounter, prefix string, window time.Duration, timeout time.Duration, failCount int, openWindow time.Duration) {
	if l == nil || counter == nil {
		return
	}
	if window <= 0 {
		window = time.Minute
	}
	if timeout <= 0 {
		timeout = 120 * time.Millisecond
	}
	normalizedPrefix := strings.TrimSpace(strings.ToLower(prefix))
	if normalizedPrefix == "" {
		normalizedPrefix = "gateway:ratelimit"
	}
	limit := l.perMinute + l.burst
	if limit <= 0 {
		limit = l.perMinute
	}
	if limit <= 0 {
		limit = 1
	}
	if failCount <= 0 {
		failCount = 3
	}
	if openWindow <= 0 {
		openWindow = 30 * time.Second
	}

	l.distributedCounter = counter
	l.distributedPrefix = normalizedPrefix
	l.distributedWindow = window
	l.distributedTimeout = timeout
	l.distributedLimit = limit
	l.circuitFailCount = failCount
	l.circuitOpenWindow = openWindow
	l.circuitMu.Lock()
	l.circuitFailures = 0
	l.circuitOpenUntil = time.Time{}
	l.circuitMu.Unlock()
}

func (l *Limiter) Allow(key string) bool {
	normalized := strings.TrimSpace(strings.ToLower(key))
	if normalized == "" {
		return true
	}
	if l.distributedCounter != nil {
		if allowed, decided := l.allowDistributed(normalized); decided {
			return allowed
		}
	}
	return l.allowLocal(normalized)
}

func (l *Limiter) allowDistributed(normalizedKey string) (bool, bool) {
	if l == nil || l.distributedCounter == nil {
		return false, false
	}
	now := time.Now()
	if l.isCircuitOpen(now) {
		return false, false
	}
	window := l.distributedWindow
	if window <= 0 {
		window = time.Minute
	}
	ttl := window + time.Minute
	bucket := now.UTC().Truncate(window).Unix()
	distKey := fmt.Sprintf("%s:%d:%s", l.distributedPrefix, bucket, normalizedKey)
	ctx, cancel := context.WithTimeout(context.Background(), l.distributedTimeout)
	defer cancel()

	count, err := l.distributedCounter.IncrWithTTL(ctx, distKey, ttl)
	if err != nil {
		l.onDistributedFailure(now)
		return false, false
	}
	l.onDistributedSuccess()
	return count <= int64(l.distributedLimit), true
}

func (l *Limiter) allowLocal(normalized string) bool {
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

func (l *Limiter) isCircuitOpen(now time.Time) bool {
	l.circuitMu.Lock()
	defer l.circuitMu.Unlock()
	if l.circuitOpenUntil.IsZero() {
		return false
	}
	if now.Before(l.circuitOpenUntil) {
		return true
	}
	l.circuitOpenUntil = time.Time{}
	return false
}

func (l *Limiter) onDistributedFailure(now time.Time) {
	failCount := l.circuitFailCount
	if failCount <= 0 {
		failCount = 3
	}
	openWindow := l.circuitOpenWindow
	if openWindow <= 0 {
		openWindow = 30 * time.Second
	}

	l.circuitMu.Lock()
	defer l.circuitMu.Unlock()
	if !l.circuitOpenUntil.IsZero() && now.Before(l.circuitOpenUntil) {
		return
	}
	l.circuitFailures++
	if l.circuitFailures >= failCount {
		l.circuitOpenUntil = now.Add(openWindow)
		l.circuitFailures = 0
	}
}

func (l *Limiter) onDistributedSuccess() {
	l.circuitMu.Lock()
	defer l.circuitMu.Unlock()
	l.circuitFailures = 0
	l.circuitOpenUntil = time.Time{}
}
