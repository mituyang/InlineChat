package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLimiterAllow(t *testing.T) {
	limiter := New(60, 2, time.Minute, 100)
	key := "visitor:demo"

	if !limiter.Allow(key) {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow(key) {
		t.Fatal("second request should be allowed within burst")
	}
	if limiter.Allow(key) {
		t.Fatal("third request should be blocked by burst limit")
	}
}

func TestLimiterEmptyKeyAlwaysAllow(t *testing.T) {
	limiter := New(1, 1, time.Minute, 1)
	if !limiter.Allow("") {
		t.Fatal("empty key should be allowed")
	}
}

type fakeDistributedCounter struct {
	calls []string
	count int64
	err   error
}

func (f *fakeDistributedCounter) IncrWithTTL(_ context.Context, key string, _ time.Duration) (int64, error) {
	f.calls = append(f.calls, key)
	if f.err != nil {
		return 0, f.err
	}
	f.count++
	return f.count, nil
}

func TestLimiterAllowWithDistributedCounter(t *testing.T) {
	limiter := New(1, 1, time.Minute, 100)
	counter := &fakeDistributedCounter{}
	limiter.EnableDistributedCounter(counter, "gateway:ratelimit:test", time.Minute, 50*time.Millisecond)

	key := "visitor:test"
	if !limiter.Allow(key) {
		t.Fatal("first request should be allowed")
	}
	if !limiter.Allow(key) {
		t.Fatal("second request should be allowed")
	}
	if limiter.Allow(key) {
		t.Fatal("third request should be blocked by distributed limit")
	}
	if len(counter.calls) == 0 {
		t.Fatal("expected distributed counter to be called")
	}
}

func TestLimiterDistributedCounterFallbackToLocal(t *testing.T) {
	limiter := New(60, 2, time.Minute, 100)
	counter := &fakeDistributedCounter{err: errors.New("redis unavailable")}
	limiter.EnableDistributedCounter(counter, "gateway:ratelimit:test", time.Minute, 50*time.Millisecond)

	key := "visitor:fallback"
	if !limiter.Allow(key) {
		t.Fatal("first request should be allowed by local fallback")
	}
	if !limiter.Allow(key) {
		t.Fatal("second request should be allowed by local fallback")
	}
	if limiter.Allow(key) {
		t.Fatal("third request should be blocked by local fallback")
	}
}
