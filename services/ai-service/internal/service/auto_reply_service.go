package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"inlinechat/services/ai-service/internal/adminclient"
	"inlinechat/services/ai-service/internal/chatclient"
	"inlinechat/services/ai-service/internal/knowledgebase"
	"inlinechat/services/ai-service/internal/openai"
)

const (
	replyLockTTL      = 10 * time.Minute
	replySenderType   = "ai"
	replySenderID     = "ai-service"
	replyTemperature  = 0.1
	replyMaxTokens    = 256
	maxPromptContext  = 4800
	replyModeAutoOnly = "unassigned_auto_reply"
)

var thinkTagPattern = regexp.MustCompile(`(?s)<think>.*?</think>`)

type AutoReplyService struct {
	redisClient   *redis.Client
	chatClient    *chatclient.DynamicClient
	adminClient   *adminclient.DynamicClient
	kb            *knowledgebase.Manager
	llmClient     *openai.Client
	logger        *zap.Logger
	callTimeout   time.Duration
	retrieveTopK  int
	minSimilarity float64
	unknownReply  string
}

func NewAutoReplyService(
	redisClient *redis.Client,
	chatClient *chatclient.DynamicClient,
	adminClient *adminclient.DynamicClient,
	kb *knowledgebase.Manager,
	llmClient *openai.Client,
	logger *zap.Logger,
	callTimeout time.Duration,
	retrieveTopK int,
	minSimilarity float64,
	unknownReply string,
) *AutoReplyService {
	if logger == nil {
		logger = zap.NewNop()
	}
	if callTimeout <= 0 {
		callTimeout = 8 * time.Second
	}
	return &AutoReplyService{
		redisClient:   redisClient,
		chatClient:    chatClient,
		adminClient:   adminClient,
		kb:            kb,
		llmClient:     llmClient,
		logger:        logger,
		callTimeout:   callTimeout,
		retrieveTopK:  retrieveTopK,
		minSimilarity: minSimilarity,
		unknownReply:  strings.TrimSpace(unknownReply),
	}
}

func (s *AutoReplyService) ReloadKnowledge(ctx context.Context) (knowledgebase.Status, error) {
	return s.kb.Reload(ctx)
}

func (s *AutoReplyService) KnowledgeStatus() knowledgebase.Status {
	return s.kb.Status()
}

func (s *AutoReplyService) HandleEvent(ctx context.Context, payload []byte) error {
	event, err := parseMessageCreatedEvent(payload)
	if err != nil {
		return nil
	}
	if event.Message.SenderType != "visitor" || strings.TrimSpace(event.Message.Content) == "" {
		return nil
	}

	lockKey := buildReplyLockKey(event.ConversationID, event.Message.ID)
	acquired, err := s.redisClient.SetNX(ctx, lockKey, "1", replyLockTTL).Result()
	if err != nil {
		return err
	}
	if !acquired {
		return nil
	}

	keepLock := true
	defer func() {
		if keepLock {
			return
		}
		deleteCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if delErr := s.redisClient.Del(deleteCtx, lockKey).Err(); delErr != nil {
			s.logger.Warn("delete ai reply lock failed", zap.Error(delErr), zap.String("lock_key", lockKey))
		}
	}()

	if err := s.processVisitorMessage(ctx, *event); err != nil {
		keepLock = false
		return err
	}
	return nil
}

func (s *AutoReplyService) processVisitorMessage(ctx context.Context, event messageCreatedEvent) error {
	conversation, err := s.chatClient.GetConversation(ctx, event.ConversationID)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil
		}
		return err
	}
	if conversation.Status != "open" || conversation.AssignedAgentID != 0 {
		return nil
	}

	aiConfig, err := s.adminClient.GetSiteAIConfig(ctx, conversation.SiteID)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil
		}
		return err
	}
	if !aiConfig.Enabled || strings.TrimSpace(aiConfig.ReplyMode) != replyModeAutoOnly {
		return nil
	}

	if !s.isLatestVisitorMessage(ctx, event.ConversationID, event.Message.ID) {
		return nil
	}

	query := normalizeText(event.Message.Content)
	if query == "" {
		return nil
	}

	results, err := s.kb.Search(ctx, query, s.retrieveTopK, s.minSimilarity)
	if err != nil {
		return err
	}
	reply := s.unknownReply
	if len(results) > 0 {
		generatedReply, genErr := s.generateReply(ctx, query, results)
		if genErr != nil {
			s.logger.Warn("generate ai reply failed, fallback to unknown reply",
				zap.Error(genErr),
				zap.Uint64("conversation_id", event.ConversationID),
				zap.Uint64("message_id", event.Message.ID),
			)
		} else {
			reply = generatedReply
		}
	}
	reply = sanitizeModelReply(reply, s.unknownReply)

	conversation, err = s.chatClient.GetConversation(ctx, event.ConversationID)
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			return nil
		}
		return err
	}
	if conversation.Status != "open" || conversation.AssignedAgentID != 0 {
		return nil
	}
	if !s.isLatestVisitorMessage(ctx, event.ConversationID, event.Message.ID) {
		return nil
	}

	_, err = s.chatClient.CreateMessage(ctx, event.ConversationID, chatclient.CreateMessageRequest{
		SenderType:  replySenderType,
		SenderID:    replySenderID,
		Content:     reply,
		ClientMsgID: buildAIClientMsgID(event.ConversationID, event.Message.ID),
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == codes.AlreadyExists {
			return nil
		}
		return err
	}

	s.logger.Info("ai reply created",
		zap.Uint64("conversation_id", event.ConversationID),
		zap.Uint64("message_id", event.Message.ID),
		zap.String("site_id", conversation.SiteID),
	)
	return nil
}

func (s *AutoReplyService) isLatestVisitorMessage(ctx context.Context, conversationID uint64, messageID uint64) bool {
	items, err := s.chatClient.ListMessages(ctx, conversationID, chatclient.ListMessagesInput{Limit: 1})
	if err != nil || len(items) == 0 {
		return false
	}
	latest := items[0]
	return latest != nil && latest.ID == messageID && latest.SenderType == "visitor"
}

func (s *AutoReplyService) generateReply(ctx context.Context, question string, results []knowledgebase.SearchResult) (string, error) {
	if len(results) == 0 {
		return s.unknownReply, nil
	}

	contextText := buildContext(results, maxPromptContext)
	reply, err := s.llmClient.ChatCompletion(ctx, []openai.ChatMessage{
		{
			Role: "system",
			Content: fmt.Sprintf(
				"你是青禾家居的AI客服。你只能根据提供的知识片段回答，不得补全、推断、猜测或编造任何信息。"+
					"如果知识片段无法直接支持答案，你必须只回复：%s "+
					"禁止输出分析过程、禁止输出<think>标签、禁止提及模型或训练数据。",
				s.unknownReply,
			),
		},
		{
			Role:    "user",
			Content: "/no_think\n知识片段如下：\n" + contextText + "\n\n用户问题：" + question + "\n\n请直接用中文回答。",
		},
	}, replyTemperature, replyMaxTokens)
	if err != nil {
		return "", err
	}
	return reply, nil
}

type messageCreatedEvent struct {
	Type           string
	ConversationID uint64
	Message        struct {
		ID         uint64
		SenderType string
		Content    string
	}
}

func parseMessageCreatedEvent(payload []byte) (*messageCreatedEvent, error) {
	var raw struct {
		Type    string `json:"type"`
		Payload struct {
			ConversationID uint64 `json:"conversation_id"`
			Message        struct {
				ID         uint64 `json:"id"`
				SenderType string `json:"sender_type"`
				Content    string `json:"content"`
			} `json:"message"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	if raw.Type != "message.new" || raw.Payload.ConversationID == 0 || raw.Payload.Message.ID == 0 {
		return nil, fmt.Errorf("ignore event")
	}

	out := &messageCreatedEvent{
		Type:           raw.Type,
		ConversationID: raw.Payload.ConversationID,
	}
	out.Message.ID = raw.Payload.Message.ID
	out.Message.SenderType = strings.ToLower(strings.TrimSpace(raw.Payload.Message.SenderType))
	out.Message.Content = raw.Payload.Message.Content
	return out, nil
}

func buildReplyLockKey(conversationID uint64, messageID uint64) string {
	return fmt.Sprintf("ai:reply:%d:%d", conversationID, messageID)
}

func buildAIClientMsgID(conversationID uint64, messageID uint64) string {
	return fmt.Sprintf("ai_%d_%d", conversationID, messageID)
}

func buildContext(results []knowledgebase.SearchResult, maxChars int) string {
	if maxChars <= 0 {
		maxChars = maxPromptContext
	}

	var builder strings.Builder
	for i, item := range results {
		part := fmt.Sprintf("[%d] %s\n%s\n\n", i+1, fallbackSection(item.Section), strings.TrimSpace(item.Text))
		if builder.Len()+len(part) > maxChars {
			break
		}
		builder.WriteString(part)
	}
	return strings.TrimSpace(builder.String())
}

func fallbackSection(section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return "未命名章节"
	}
	return section
}

func sanitizeModelReply(reply string, fallback string) string {
	reply = thinkTagPattern.ReplaceAllString(reply, "")
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fallback
	}
	return reply
}

func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}
