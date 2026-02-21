package pubsub

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const outboxWakeupChannel = "chat.outbox.wakeup"

type RedisOutboxWakeupBus struct {
	client  *redis.Client
	timeout time.Duration
}

func NewRedisOutboxWakeupBus(client *redis.Client, timeout time.Duration) *RedisOutboxWakeupBus {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &RedisOutboxWakeupBus{
		client:  client,
		timeout: timeout,
	}
}

func (b *RedisOutboxWakeupBus) NotifyOutbox(ctx context.Context) error {
	if b == nil || b.client == nil {
		return nil
	}

	pubCtx := ctx
	cancel := func() {}
	if pubCtx == nil {
		pubCtx, cancel = context.WithTimeout(context.Background(), b.timeout)
	}
	defer cancel()

	if err := b.client.Publish(pubCtx, outboxWakeupChannel, "1").Err(); err != nil {
		return fmt.Errorf("publish outbox wakeup failed: %w", err)
	}
	return nil
}

func (b *RedisOutboxWakeupBus) ConsumeOutboxWakeup(ctx context.Context, onWakeup func()) error {
	if b == nil || b.client == nil {
		return nil
	}
	if onWakeup == nil {
		return nil
	}

	sub := b.client.Subscribe(ctx, outboxWakeupChannel)
	defer sub.Close()

	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-ch:
			if !ok {
				return fmt.Errorf("redis outbox wakeup channel closed")
			}
			onWakeup()
		}
	}
}
