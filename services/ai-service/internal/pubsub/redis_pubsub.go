package pubsub

import (
	"context"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
)

const channelPrefix = "chat.messages."

type RedisPubSub struct {
	client *redis.Client
}

func NewRedisPubSub(client *redis.Client) *RedisPubSub {
	return &RedisPubSub{client: client}
}

func (r *RedisPubSub) Consume(ctx context.Context, fn func(conversationID string, payload []byte)) error {
	ps := r.client.PSubscribe(ctx, channelPrefix+"*")
	defer ps.Close()

	ch := ps.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return fmt.Errorf("redis pubsub channel closed")
			}
			conversationID := strings.TrimPrefix(msg.Channel, channelPrefix)
			fn(conversationID, []byte(msg.Payload))
		}
	}
}

func (r *RedisPubSub) Ping(ctx context.Context) error {
	if err := r.client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	return nil
}
