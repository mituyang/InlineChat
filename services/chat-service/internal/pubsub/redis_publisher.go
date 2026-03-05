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

// PublishMessageCreated 推送新消息事件，供 realtime-service 广播到 WS 客户端。
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
				"status":          message.Status,
				"created_at":      message.CreatedAt.Format(time.RFC3339Nano),
				"updated_at":      message.UpdatedAt.Format(time.RFC3339Nano),
			},
		},
	}

	if err := p.publishByConversationID(message.ConversationID, payload); err != nil {
		return fmt.Errorf("publish message.new failed: %w", err)
	}
	return nil
}

// PublishMessageStatus 推送单条消息状态变更（sent -> delivered/read）。
func (p *RedisMessagePublisher) PublishMessageStatus(_ context.Context, conversationID uint64, messageID uint64, status string) error {
	if conversationID == 0 || messageID == 0 {
		return nil
	}

	payload := map[string]any{
		"type": "message.status",
		"payload": map[string]any{
			"conversation_id": conversationID,
			"message_id":      messageID,
			"status":          status,
		},
	}

	if err := p.publishByConversationID(conversationID, payload); err != nil {
		return fmt.Errorf("publish message.status(single) failed: %w", err)
	}
	return nil
}

// PublishMessageStatusRange 推送“截至某条消息”的批量状态推进事件。
func (p *RedisMessagePublisher) PublishMessageStatusRange(_ context.Context, conversationID uint64, senderType string, upToMessageID uint64, status string) error {
	if conversationID == 0 || upToMessageID == 0 {
		return nil
	}

	payload := map[string]any{
		"type": "message.status",
		"payload": map[string]any{
			"conversation_id":  conversationID,
			"sender_type":      senderType,
			"up_to_message_id": upToMessageID,
			"status":           status,
		},
	}

	if err := p.publishByConversationID(conversationID, payload); err != nil {
		return fmt.Errorf("publish message.status(range) failed: %w", err)
	}
	return nil
}

func (p *RedisMessagePublisher) PublishConversationClosed(_ context.Context, conversationID uint64) error {
	if conversationID == 0 {
		return nil
	}

	payload := map[string]any{
		"type": "conversation.closed",
		"payload": map[string]any{
			"conversation_id": conversationID,
			"status":          "closed",
		},
	}

	if err := p.publishByConversationID(conversationID, payload); err != nil {
		return fmt.Errorf("publish conversation.closed failed: %w", err)
	}
	return nil
}

// publishByConversationID 统一封装 JSON 序列化与按会话分频道发布。
func (p *RedisMessagePublisher) publishByConversationID(conversationID uint64, payload map[string]any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event payload failed: %w", err)
	}
	return p.PublishConversationEvent(context.Background(), conversationID, raw)
}

// PublishConversationEvent 是底层发布原语，按 conversation_id 路由频道。
func (p *RedisMessagePublisher) PublishConversationEvent(_ context.Context, conversationID uint64, payload []byte) error {
	if conversationID == 0 || len(payload) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), p.timeout)
	defer cancel()

	channel := channelPrefix + strconv.FormatUint(conversationID, 10)
	if err := p.client.Publish(ctx, channel, payload).Err(); err != nil {
		return fmt.Errorf("publish event failed: %w", err)
	}
	return nil
}
