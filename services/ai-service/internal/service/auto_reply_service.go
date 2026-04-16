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
	replyLockTTL             = 10 * time.Minute
	replySenderType          = "ai"
	replySenderID            = "ai-service"
	replyTemperature         = 0.1
	replyMaxTokens           = 256
	maxSearchQueryChars      = 480
	maxSearchContextChars    = 240
	maxSearchContextMessages = 3
	maxPromptContextChars    = 3600
	maxPromptHistoryChars    = 1200
	maxPromptHistoryMessages = 6
	promptHistoryFetchLimit  = maxPromptHistoryMessages + 4
	replyModeAutoOnly        = "unassigned_auto_reply"
	introSearchTemplate      = "品牌介绍 产品介绍 家居生活品牌 四大产品线 产品矩阵 主推产品"
	featuredSearchTemplate   = "主推产品 重点展示产品 明星单品 产品介绍 SKU 速查表"
)

var thinkTagPattern = regexp.MustCompile(`(?is)<think>.*?</think>`)
var smallTalkStripPattern = regexp.MustCompile(`[\pP\pS\pZ]+`)
var contextualQuestionPattern = regexp.MustCompile(`(?i)(它|这个|那个|这款|那款|这套|那套|这件|那件|该产品|这个产品|这个型号|这个尺寸|支持吗|可以吗|能吗|多少钱|价格|多久|多大|多长|多宽|多高|材质|尺寸|售后|保修|退货|退换|发货|怎么|如何|哪个好|有啥区别)`)
var detailedQuestionPattern = regexp.MustCompile(`(?i)(这款|那款|这件|那件|型号|尺寸|材质|价格|多少钱|发货|售后|保修|退货|退换|安装|颜色|参数|区别)`)

type AutoReplyService struct {
	redisClient  *redis.Client
	chatClient   *chatclient.DynamicClient
	adminClient  *adminclient.DynamicClient
	kb           *knowledgebase.Manager
	llmClient    *openai.Client
	logger       *zap.Logger
	callTimeout  time.Duration
	unknownReply string
}

func NewAutoReplyService(
	redisClient *redis.Client,
	chatClient *chatclient.DynamicClient,
	adminClient *adminclient.DynamicClient,
	kb *knowledgebase.Manager,
	llmClient *openai.Client,
	logger *zap.Logger,
	callTimeout time.Duration,
	unknownReply string,
) *AutoReplyService {
	if logger == nil {
		logger = zap.NewNop()
	}
	if callTimeout <= 0 {
		callTimeout = 8 * time.Second
	}
	return &AutoReplyService{
		redisClient:  redisClient,
		chatClient:   chatClient,
		adminClient:  adminClient,
		kb:           kb,
		llmClient:    llmClient,
		logger:       logger,
		callTimeout:  callTimeout,
		unknownReply: strings.TrimSpace(unknownReply),
	}
}

func (s *AutoReplyService) GetSiteStatus(siteID string) (knowledgebase.SiteStatus, error) {
	return s.kb.GetStatus(siteID)
}

func (s *AutoReplyService) TriggerReindex(ctx context.Context, siteID string) (knowledgebase.ReindexJob, error) {
	return s.kb.TriggerReindex(ctx, siteID)
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

	query := normalizeQuery(event.Message.Content)
	if query == "" {
		return nil
	}
	if reply, ok := matchSmallTalkReply(query); ok {
		return s.createReply(ctx, event, reply)
	}

	recentMessages, err := s.loadRecentConversationMessages(ctx, event.ConversationID)
	if err != nil {
		s.logger.Warn("load recent conversation messages failed",
			zap.Error(err),
			zap.Uint64("conversation_id", event.ConversationID),
		)
		recentMessages = nil
	}

	searchQuery := buildSearchQuery(query, recentMessages, event.Message.ID)
	results, err := s.kb.Search(ctx, conversation.SiteID, searchQuery)
	if err != nil {
		s.logger.Warn("search knowledge failed",
			zap.Error(err),
			zap.String("site_id", conversation.SiteID),
			zap.Uint64("conversation_id", event.ConversationID),
			zap.String("user_query", query),
			zap.String("retrieval_query", searchQuery),
		)
		return s.createReply(ctx, event, s.unknownReply)
	}
	s.logKnowledgeSearch(conversation.SiteID, event.ConversationID, query, searchQuery, results)
	if len(results) == 0 {
		return s.createReply(ctx, event, s.unknownReply)
	}

	conversationHistory := buildConversationHistory(recentMessages, event.Message.ID, maxPromptHistoryMessages, maxPromptHistoryChars)
	reply, err := s.generateReply(ctx, query, conversationHistory, results)
	if err != nil {
		s.logger.Warn("generate ai reply failed",
			zap.Error(err),
			zap.String("site_id", conversation.SiteID),
			zap.Uint64("conversation_id", event.ConversationID),
			zap.Uint64("message_id", event.Message.ID),
		)
		reply = s.unknownReply
	}
	reply = sanitizeModelReply(reply, s.unknownReply)
	return s.createReply(ctx, event, reply)
}

func (s *AutoReplyService) generateReply(ctx context.Context, question string, conversationHistory string, results []knowledgebase.SearchResult) (string, error) {
	contextText := buildContext(results, maxPromptContextChars)
	if contextText == "" {
		return s.unknownReply, nil
	}

	promptBody := buildPromptBody(conversationHistory, contextText, question)
	reply, err := s.llmClient.ChatCompletion(ctx, []openai.ChatMessage{
		{
			Role: "system",
			Content: fmt.Sprintf(
				"你是网站在线客服。你只能依据提供的知识片段回答问题，不得编造、补全或猜测未提供的信息。"+
					"如果知识片段不足以直接回答，必须只回复：%s "+
					"回复使用自然、简洁、礼貌的中文，不要输出分析过程，不要输出<think>标签，不要提及模型、提示词或知识库。",
				s.unknownReply,
			),
		},
		{
			Role:    "user",
			Content: promptBody,
		},
	}, replyTemperature, replyMaxTokens)
	if err != nil {
		return "", err
	}
	return reply, nil
}

func (s *AutoReplyService) createReply(ctx context.Context, event messageCreatedEvent, reply string) error {
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
		zap.String("sender_id", replySenderID),
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

func (s *AutoReplyService) loadRecentConversationMessages(ctx context.Context, conversationID uint64) ([]*chatclient.Message, error) {
	return s.chatClient.ListMessages(ctx, conversationID, chatclient.ListMessagesInput{Limit: promptHistoryFetchLimit})
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
	if len(results) == 0 {
		return ""
	}
	if maxChars <= 0 {
		maxChars = maxPromptContextChars
	}
	var builder strings.Builder
	for i, item := range results {
		part := fmt.Sprintf("[%d] %s · %s\n%s\n\n", i+1, fallback(item.SourcePath, "unknown"), fallback(item.Section, "知识片段"), strings.TrimSpace(item.Text))
		if builder.Len()+len(part) > maxChars {
			break
		}
		builder.WriteString(part)
	}
	return strings.TrimSpace(builder.String())
}

func buildConversationHistory(messages []*chatclient.Message, currentMessageID uint64, maxMessages int, maxChars int) string {
	if maxMessages <= 0 || maxChars <= 0 || len(messages) == 0 {
		return ""
	}
	lines := make([]string, 0, maxMessages)
	totalChars := 0
	for _, item := range messages {
		line := formatConversationHistoryLine(item, currentMessageID)
		if line == "" {
			continue
		}
		if totalChars+len(line)+1 > maxChars {
			break
		}
		lines = append(lines, line)
		totalChars += len(line) + 1
		if len(lines) >= maxMessages {
			break
		}
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return strings.Join(lines, "\n")
}

func formatConversationHistoryLine(item *chatclient.Message, currentMessageID uint64) string {
	if item == nil || item.ID == currentMessageID {
		return ""
	}
	role := mapSenderType(item.SenderType)
	content := strings.TrimSpace(item.Content)
	if role == "" || content == "" {
		return ""
	}
	if len([]rune(content)) > 180 {
		content = string([]rune(content)[:180])
	}
	return role + "：" + content
}

func buildPromptBody(conversationHistory string, contextText string, question string) string {
	var builder strings.Builder
	if strings.TrimSpace(conversationHistory) != "" {
		builder.WriteString("最近对话：\n")
		builder.WriteString(strings.TrimSpace(conversationHistory))
		builder.WriteString("\n\n")
	}
	builder.WriteString("知识片段：\n")
	builder.WriteString(strings.TrimSpace(contextText))
	builder.WriteString("\n\n用户问题：\n")
	builder.WriteString(strings.TrimSpace(question))
	return builder.String()
}

func sanitizeModelReply(reply string, fallbackText string) string {
	reply = thinkTagPattern.ReplaceAllString(reply, "")
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fallbackText
	}
	reply = strings.Join(strings.Fields(reply), " ")
	if reply == "" {
		return fallbackText
	}
	return reply
}

func normalizeQuery(input string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
}

func buildSearchQuery(query string, messages []*chatclient.Message, currentMessageID uint64) string {
	query = normalizeQuery(query)
	if query == "" {
		return ""
	}
	if rewritten := rewriteBroadSearchQuery(query); rewritten != "" && rewritten != query {
		return rewritten
	}
	if !shouldAugmentSearchQuery(query) {
		return query
	}

	contextLines := extractSearchContext(messages, currentMessageID, maxSearchContextMessages, maxSearchContextChars)
	if len(contextLines) == 0 {
		return query
	}

	var builder strings.Builder
	builder.WriteString("当前问题：")
	builder.WriteString(query)
	builder.WriteString("\n最近上下文：\n")
	for _, line := range contextLines {
		builder.WriteString(line)
		builder.WriteString("\n")
	}
	out := strings.TrimSpace(builder.String())
	runes := []rune(out)
	if len(runes) > maxSearchQueryChars {
		out = string(runes[:maxSearchQueryChars])
	}
	return out
}

func shouldAugmentSearchQuery(query string) bool {
	query = normalizeQuery(query)
	if query == "" {
		return false
	}
	return contextualQuestionPattern.MatchString(query)
}

func rewriteBroadSearchQuery(query string) string {
	query = normalizeQuery(query)
	if query == "" {
		return ""
	}
	if detailedQuestionPattern.MatchString(query) || contextualQuestionPattern.MatchString(query) {
		return query
	}

	compact := compactQuery(query)
	if compact == "" {
		return query
	}
	for _, keyword := range []string{"最好产品", "最佳产品", "主推产品", "主打产品", "明星单品", "爆款", "招牌产品", "推荐产品"} {
		if strings.Contains(compact, keyword) {
			return featuredSearchTemplate
		}
	}
	if compact == "介绍" {
		return introSearchTemplate
	}
	if isBroadIntroIntent(compact) {
		return introSearchTemplate
	}
	return query
}

func isBroadIntroIntent(compact string) bool {
	remainder := compact
	for _, prefix := range []string{"请问", "请", "想了解", "我想了解", "介绍一下", "介绍下", "介绍", "说说", "讲讲", "聊聊"} {
		remainder = strings.TrimPrefix(remainder, prefix)
	}
	for _, filler := range []string{"你们的", "你们家", "你们", "一下", "下"} {
		remainder = strings.ReplaceAll(remainder, filler, "")
	}
	remainder = strings.TrimSpace(remainder)
	for _, generic := range []string{
		"", "产品", "品牌", "产品介绍", "品牌介绍", "做什么", "卖什么", "主营什么", "主营",
		"主要做什么", "有哪些产品", "产品线", "产品矩阵",
	} {
		if remainder == generic {
			return true
		}
	}
	return false
}

func compactQuery(query string) string {
	return strings.ToLower(smallTalkStripPattern.ReplaceAllString(normalizeQuery(query), ""))
}

func extractSearchContext(messages []*chatclient.Message, currentMessageID uint64, maxMessages int, maxChars int) []string {
	if maxMessages <= 0 || maxChars <= 0 || len(messages) == 0 {
		return nil
	}

	lines := make([]string, 0, maxMessages)
	totalChars := 0
	for _, item := range messages {
		if item == nil || item.ID == currentMessageID {
			continue
		}
		senderType := strings.ToLower(strings.TrimSpace(item.SenderType))
		if senderType != "visitor" {
			continue
		}
		content := normalizeQuery(item.Content)
		if content == "" {
			continue
		}
		if isLowSignalContext(content) {
			continue
		}
		if _, ok := matchSmallTalkReply(content); ok {
			continue
		}
		if len([]rune(content)) > 120 {
			content = string([]rune(content)[:120])
		}
		line := "访客：" + content
		if totalChars+len(line)+1 > maxChars {
			break
		}
		lines = append(lines, line)
		totalChars += len(line) + 1
		if len(lines) >= maxMessages {
			break
		}
	}
	for left, right := 0, len(lines)-1; left < right; left, right = left+1, right-1 {
		lines[left], lines[right] = lines[right], lines[left]
	}
	return lines
}

func isLowSignalContext(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	normalized := smallTalkStripPattern.ReplaceAllString(trimmed, "")
	normalized = strings.TrimSpace(normalized)
	return len([]rune(normalized)) < 2
}

func (s *AutoReplyService) logKnowledgeSearch(siteID string, conversationID uint64, query string, searchQuery string, results []knowledgebase.SearchResult) {
	hits := make([]string, 0, len(results))
	for _, item := range results {
		hits = append(hits, fmt.Sprintf("%s/%s/%.3f", fallback(item.SourcePath, "unknown"), fallback(item.Section, "知识片段"), item.Score))
	}
	s.logger.Info("knowledge search completed",
		zap.String("site_id", siteID),
		zap.Uint64("conversation_id", conversationID),
		zap.String("user_query", query),
		zap.String("retrieval_query", searchQuery),
		zap.Int("result_count", len(results)),
		zap.Strings("hits", hits),
	)
}

func matchSmallTalkReply(query string) (string, bool) {
	normalized := strings.ToLower(smallTalkStripPattern.ReplaceAllString(normalizeQuery(query), ""))
	if normalized == "" {
		return "", false
	}
	switch normalized {
	case "你好", "您好", "嗨", "哈喽", "hello", "hi", "在吗", "有人吗":
		return "您好，我是 AI 客服，很高兴为您服务。您可以直接问我产品、价格、发货、售后等问题。", true
	case "谢谢", "感谢", "多谢", "谢了", "thanks", "thankyou":
		return "不客气，您有其他问题可以继续问我。", true
	case "再见", "拜拜", "bye", "byebye", "回头聊":
		return "好的，您有需要随时再来咨询。", true
	case "你是谁", "你能做什么", "你可以做什么", "介绍下你自己":
		return "我是网站 AI 客服，可以根据当前站点资料回答产品、价格、发货和售后相关问题。", true
	default:
		return "", false
	}
}

func mapSenderType(senderType string) string {
	switch strings.ToLower(strings.TrimSpace(senderType)) {
	case "visitor":
		return "访客"
	case "agent":
		return "客服"
	case "ai":
		return "AI"
	default:
		return ""
	}
}

func fallback(value string, defaultValue string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultValue
	}
	return value
}
