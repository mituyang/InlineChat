package pubsub

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"inlinechat/services/chat-service/internal/model"
)

const channelPrefix = "chat.messages."

type RedisMessagePublisher struct {
	client  *redis.Client
	timeout time.Duration
}

func NewRedisMessagePublisher(client *redis.Client, timeout time.Duration) *RedisMessagePublisher {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &RedisMessagePublisher{
		client:  client,
		timeout: timeout,
	}
}

func (p *RedisMessagePublisher) PublishMessageCreated(_ context.Context, message *model.Message) error {
	if message == nil {
		return nil
	}

	payload := map[string]any{
		"type": "message.new",
		"payload": map[string]any{
			"conversation_id": message.ConversationID,
			"message": map[string]any{
				"id":              message.ID,
				"conversation_id": message.ConversationID,
				"sender_type":     message.SenderType,
				"sender_id":       message.SenderID,
				"content":         message.Content,
				"client_msg_id":   message.ClientMsgID,
				"created_at":      message.CreatedAt.Format(time.RFC3339Nano),
				"updated_at":      message.UpdatedAt.Format(time.RFC3339Nano),
			},
		},
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal message.new payload failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	channel := channelPrefix + strconv.FormatUint(message.ConversationID, 10)
	if err := p.client.Publish(ctx, channel, raw).Err(); err != nil {
		return fmt.Errorf("publish message.new failed: %w", err)
	}

	return nil
}
