package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisCounter struct {
	client redis.UniversalClient
}

func NewRedisCounter(client redis.UniversalClient) *RedisCounter {
	if client == nil {
		return nil
	}
	return &RedisCounter{client: client}
}

func (r *RedisCounter) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if r == nil || r.client == nil {
		return 0, fmt.Errorf("redis counter is not initialized")
	}
	if ttl <= 0 {
		ttl = time.Minute
	}

	pipe := r.client.TxPipeline()
	countCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return countCmd.Val(), nil
}
