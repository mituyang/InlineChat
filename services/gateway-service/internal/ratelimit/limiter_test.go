package ratelimit

import (
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
