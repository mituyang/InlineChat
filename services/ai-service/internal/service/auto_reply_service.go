package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

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
	replyLockTTL              = 10 * time.Minute
	replySenderType           = "ai"
	replySenderID             = "ai-service"
	replyTemperature          = 0.1
	replyMaxTokens            = 256
	maxPromptContext          = 4800
	maxPromptHistoryContext   = 1400
	maxPromptHistoryMessages  = 6
	promptHistoryFetchLimit   = maxPromptHistoryMessages + 4
	maxPromptHistoryLineRunes = 180
	replyModeAutoOnly         = "unassigned_auto_reply"
	maxFuzzyRewrites          = 3
)

var thinkTagPattern = regexp.MustCompile(`(?s)<think>.*?</think>`)

var (
	priceThresholdAbovePattern = regexp.MustCompile(`(?:高于|大于|超过|不低于|至少|起码)\s*([0-9]+(?:\.[0-9]+)?)\s*元?`)
	priceThresholdBelowPattern = regexp.MustCompile(`(?:低于|小于|不超过|不高于)\s*([0-9]+(?:\.[0-9]+)?)\s*元?`)
	priceWithinPattern         = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*元?(?:以内|以下)`)
	priceFromPattern           = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*元?(?:以上)`)
	yearMentionPattern         = regexp.MustCompile(`(20[0-9]{2}|[0-9]{2})\s*年`)
	englishWordPattern         = regexp.MustCompile(`\b[A-Za-z]{2,}\b`)
	termTokenPattern           = regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]{1,}\b`)
	emailPattern               = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	domainPattern              = regexp.MustCompile(`(?i)\b(?:[A-Z0-9-]+\.)+[A-Z]{2,}\b`)
	hyphenCodePattern          = regexp.MustCompile(`\b[A-Za-z0-9]+(?:-[A-Za-z0-9]+){1,}\b`)
	hanSpaceHanPattern         = regexp.MustCompile(`([\p{Han}])\s+([\p{Han}])`)
)

var englishReplyReplacements = []struct {
	pattern *regexp.Regexp
	value   string
}{
	{pattern: regexp.MustCompile(`(?i)\bpricing\b`), value: "价格"},
	{pattern: regexp.MustCompile(`(?i)\bprices\b`), value: "价格"},
	{pattern: regexp.MustCompile(`(?i)\bprice\b`), value: "价格"},
	{pattern: regexp.MustCompile(`(?i)\bservice\b`), value: "服务"},
	{pattern: regexp.MustCompile(`(?i)\bsupport\b`), value: "支持"},
	{pattern: regexp.MustCompile(`(?i)\bdelivery\b`), value: "配送"},
	{pattern: regexp.MustCompile(`(?i)\bshipping\b`), value: "配送"},
	{pattern: regexp.MustCompile(`(?i)\bsales\b`), value: "销售"},
	{pattern: regexp.MustCompile(`(?i)\bproduct\b`), value: "产品"},
	{pattern: regexp.MustCompile(`(?i)\bproducts\b`), value: "产品"},
	{pattern: regexp.MustCompile(`(?i)\bbrand\b`), value: "品牌"},
}

var allowedEnglishTokens = map[string]struct{}{
	"ai":  {},
	"AI":  {},
	"sku": {},
	"SKU": {},
	"vip": {},
	"VIP": {},
}

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
	recentMessages, err := s.loadRecentConversationMessages(ctx, event.ConversationID)
	if err != nil {
		s.logger.Warn("load recent conversation messages for ai reply failed",
			zap.Error(err),
			zap.Uint64("conversation_id", event.ConversationID),
			zap.Uint64("message_id", event.Message.ID),
		)
		recentMessages = nil
	}
	effectiveQuery := resolveContextualQuery(query, recentMessages, s.kb.ProductPrices())
	if reply, ok := matchPresetReply(query); ok {
		return s.createReply(ctx, event, reply)
	}
	if reply, ok := buildYearMilestoneReply(effectiveQuery, s.kb.YearMilestones()); ok {
		return s.createReply(ctx, event, reply)
	}
	if reply, ok := buildDeterministicReply(effectiveQuery, s.kb.ProductPrices(), s.kb.Terms()); ok {
		return s.createReply(ctx, event, reply)
	}
	structuredFacts := buildStructuredFacts(effectiveQuery, s.kb.ProductPrices())

	results, err := s.searchKnowledge(ctx, effectiveQuery)
	if err != nil {
		return err
	}
	if reply, ok := buildTermContextReply(effectiveQuery, results); ok {
		return s.createReply(ctx, event, reply)
	}
	reply := s.unknownReply
	if len(results) > 0 || structuredFacts != "" {
		conversationHistory := buildConversationHistory(recentMessages, event.Message.ID, maxPromptHistoryMessages, maxPromptHistoryContext)
		generatedReply, genErr := s.generateReply(ctx, effectiveQuery, conversationHistory, results, structuredFacts)
		if genErr != nil {
			s.logger.Warn("generate ai reply failed, fallback to unknown reply",
				zap.Error(genErr),
				zap.Uint64("conversation_id", event.ConversationID),
				zap.Uint64("message_id", event.Message.ID),
			)
		} else {
			reply = generatedReply
		}
	} else if clarifyReply, ok := matchClarifyReply(query); ok {
		reply = clarifyReply
	}
	reply = sanitizeModelReply(query, reply, s.unknownReply)

	return s.createReply(ctx, event, reply)
}

func (s *AutoReplyService) searchKnowledge(ctx context.Context, query string) ([]knowledgebase.SearchResult, error) {
	candidates := buildSearchQueries(query, s.kb.Terms(), s.kb.YearMilestones())
	mergedByID := make(map[int]knowledgebase.SearchResult, s.retrieveTopK)

	for _, candidate := range candidates {
		results, err := s.kb.Search(ctx, candidate, s.retrieveTopK, s.minSimilarity)
		if err != nil {
			return nil, err
		}
		for _, item := range results {
			existing, ok := mergedByID[item.ID]
			if !ok || item.Score > existing.Score {
				mergedByID[item.ID] = item
			}
		}
	}

	merged := make([]knowledgebase.SearchResult, 0, len(mergedByID))
	for _, item := range mergedByID {
		merged = append(merged, item)
	}
	if len(merged) == 0 {
		return nil, nil
	}

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Score == merged[j].Score {
			return merged[i].ID < merged[j].ID
		}
		return merged[i].Score > merged[j].Score
	})
	if len(merged) > s.retrieveTopK {
		merged = merged[:s.retrieveTopK]
	}
	return merged, nil
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

func (s *AutoReplyService) loadRecentConversationMessages(ctx context.Context, conversationID uint64) ([]*chatclient.Message, error) {
	items, err := s.chatClient.ListMessages(ctx, conversationID, chatclient.ListMessagesInput{Limit: promptHistoryFetchLimit})
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (s *AutoReplyService) generateReply(ctx context.Context, question string, conversationHistory string, results []knowledgebase.SearchResult, structuredFacts string) (string, error) {
	if len(results) == 0 && strings.TrimSpace(structuredFacts) == "" {
		return s.unknownReply, nil
	}

	contextText := buildContext(results, maxPromptContext)
	promptBody := buildPromptBody(conversationHistory, contextText, structuredFacts, question)
	reply, err := s.llmClient.ChatCompletion(ctx, []openai.ChatMessage{
		{
			Role: "system",
			Content: fmt.Sprintf(
				"你不是通用助手，你是青禾家居线上客服。"+
					"你的职责是接待来访用户，介绍青禾家居相关的产品、品牌、配送、售后和服务信息。"+
					"语气要自然、礼貌、简洁，像真实人工客服。"+
					"回复时可以根据语境自然加入1到2个贴合语义的 emoji 表情，提升亲和力，但不要堆砌、不要每句话都加。"+
					"除产品名称、品牌名、SKU、常见专有名词或必要英文缩写外，回复必须使用自然中文，不得夹杂英文单词或英文短语。"+
					"像 pricing、price、service 这类表达一律改成中文。"+
					"你只能根据提供的知识片段回答，不得补全、推断、猜测或编造任何信息。"+
					"如果提供了最近对话，它只能帮助你理解用户指代、省略和上下文延续，不能把历史对话本身当成事实来源。"+
					"知识片段明确提到的信息，可以用自然中文直接转述，不要生硬复读。"+
					"如果提供了结构化事实表，你要先基于该事实表完成比较、筛选、排序或统计，再组织自然回复。"+
					"当结构化事实表已经足够支持答案时，不要再说“资料未提及”或“无法确认”。"+
					"回复尽量控制在1到3句，优先直接回答用户问题。"+
					"如果知识片段无法直接支持答案，你必须只回复：%s "+
					"禁止输出分析过程、禁止输出<think>标签、禁止提及模型或训练数据。",
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

func buildConversationHistory(messages []*chatclient.Message, currentMessageID uint64, maxMessages int, maxChars int) string {
	if maxMessages <= 0 || maxChars <= 0 || len(messages) == 0 {
		return ""
	}

	selected := make([]string, 0, maxMessages)
	totalChars := 0
	for _, item := range messages {
		line := formatConversationHistoryLine(item, currentMessageID)
		if line == "" {
			continue
		}
		if totalChars+len(line)+1 > maxChars {
			break
		}
		selected = append(selected, line)
		totalChars += len(line) + 1
		if len(selected) >= maxMessages {
			break
		}
	}

	if len(selected) == 0 {
		return ""
	}

	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return strings.Join(selected, "\n")
}

func resolveContextualQuery(query string, recentMessages []*chatclient.Message, productPrices []knowledgebase.ProductPrice) string {
	normalized := normalizeText(query)
	if normalized == "" || !isFollowUpQuestion(normalized) {
		return normalized
	}
	for _, name := range sortedProductNames(productPrices) {
		if strings.Contains(normalized, name) {
			return normalized
		}
	}
	if productName, ok := findLatestMentionedProduct(recentMessages, 0, productPrices); ok {
		return rewriteFollowUpQuestion(normalized, productName)
	}
	return normalized
}

func isFollowUpQuestion(query string) bool {
	compact := compactText(query)
	if compact == "" {
		return false
	}
	if containsAny(compact,
		"介绍下", "介绍一下", "介绍", "说说", "讲讲", "展开说说", "展开讲讲", "详细说说", "详细介绍",
		"具体介绍", "展开点", "详细点", "具体点", "多介绍点", "继续说", "继续讲", "接着说",
		"它呢", "这个呢", "这款呢", "这款介绍下", "这个介绍下",
	) {
		return true
	}
	return false
}

func rewriteFollowUpQuestion(query string, subject string) string {
	query = normalizeText(query)
	subject = normalizeText(subject)
	if query == "" || subject == "" {
		return query
	}
	if strings.Contains(query, subject) {
		return query
	}

	compact := compactText(query)
	switch {
	case containsAny(compact, "介绍下", "介绍一下", "介绍", "这款介绍下", "这个介绍下"):
		return normalizeText("介绍一下" + subject)
	case containsAny(compact, "说说", "讲讲", "展开说说", "展开讲讲", "详细说说", "详细介绍", "具体介绍", "展开点", "详细点", "具体点", "多介绍点", "继续说", "继续讲", "它呢", "这个呢", "这款呢"):
		return normalizeText("说说" + subject)
	default:
		return normalizeText(subject + " " + query)
	}
}

func findLatestMentionedProduct(messages []*chatclient.Message, currentMessageID uint64, productPrices []knowledgebase.ProductPrice) (string, bool) {
	if len(messages) == 0 || len(productPrices) == 0 {
		return "", false
	}

	names := sortedProductNames(productPrices)
	for _, item := range messages {
		if item == nil || item.ID == currentMessageID {
			continue
		}
		content := normalizeText(item.Content)
		if content == "" {
			continue
		}
		for _, name := range names {
			if strings.Contains(content, name) {
				return name, true
			}
		}
	}
	return "", false
}

func sortedProductNames(productPrices []knowledgebase.ProductPrice) []string {
	names := make([]string, 0, len(productPrices))
	seen := make(map[string]struct{}, len(productPrices))
	for _, item := range productPrices {
		name := normalizeText(item.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		ri := len([]rune(names[i]))
		rj := len([]rune(names[j]))
		if ri == rj {
			return names[i] < names[j]
		}
		return ri > rj
	})
	return names
}

func formatConversationHistoryLine(message *chatclient.Message, currentMessageID uint64) string {
	if message == nil || message.ID == currentMessageID {
		return ""
	}

	content := normalizeText(message.Content)
	if content == "" {
		return ""
	}

	role := historySenderLabel(message.SenderType)
	if role == "" {
		return ""
	}

	return fmt.Sprintf("%s：%s", role, truncateRunes(content, maxPromptHistoryLineRunes))
}

func historySenderLabel(senderType string) string {
	switch strings.ToLower(strings.TrimSpace(senderType)) {
	case "visitor":
		return "用户"
	case "agent":
		return "人工客服"
	case "ai":
		return "AI客服"
	default:
		return ""
	}
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func fallbackSection(section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return "未命名章节"
	}
	return section
}

func sanitizeModelReply(question string, reply string, fallback string) string {
	reply = thinkTagPattern.ReplaceAllString(reply, "")
	reply = normalizeReplyLanguage(question, reply)
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fallback
	}
	return reply
}

func normalizeReplyLanguage(question string, reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return ""
	}

	reply, protected := protectLiteralTerms(reply)

	for _, item := range englishReplyReplacements {
		reply = item.pattern.ReplaceAllString(reply, item.value)
	}

	if !shouldPreserveEnglishTerms(question) {
		reply = englishWordPattern.ReplaceAllStringFunc(reply, func(token string) string {
			if _, ok := allowedEnglishTokens[token]; ok {
				return token
			}
			return ""
		})
	}

	reply = restoreLiteralTerms(reply, protected)
	reply = regexp.MustCompile(`\s+`).ReplaceAllString(reply, " ")
	for hanSpaceHanPattern.MatchString(reply) {
		reply = hanSpaceHanPattern.ReplaceAllString(reply, `$1$2`)
	}
	reply = strings.ReplaceAll(reply, "（ ", "（")
	reply = strings.ReplaceAll(reply, " ）", "）")
	reply = strings.ReplaceAll(reply, "( ", "(")
	reply = strings.ReplaceAll(reply, " )", ")")
	reply = strings.TrimSpace(reply)
	return reply
}

func shouldPreserveEnglishTerms(question string) bool {
	compact := compactText(question)
	if compact == "" {
		return false
	}
	if !regexp.MustCompile(`[A-Za-z]`).MatchString(question) {
		return false
	}
	return containsAny(compact, "是啥", "是什么", "什么意思", "缩写", "全称", "怎么读")
}

func protectLiteralTerms(reply string) (string, []string) {
	protected := make([]string, 0, 8)
	replace := func(pattern *regexp.Regexp, input string) string {
		return pattern.ReplaceAllStringFunc(input, func(match string) string {
			placeholder := fmt.Sprintf("〔保留项%d〕", len(protected))
			protected = append(protected, match)
			return placeholder
		})
	}

	reply = replace(emailPattern, reply)
	reply = replace(domainPattern, reply)
	reply = replace(hyphenCodePattern, reply)
	return reply, protected
}

func restoreLiteralTerms(reply string, protected []string) string {
	for i, item := range protected {
		reply = strings.ReplaceAll(reply, fmt.Sprintf("〔保留项%d〕", i), item)
	}
	return reply
}

func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func buildSearchQueries(query string, retrievalTerms []string, yearMilestones []knowledgebase.YearMilestone) []string {
	normalized := normalizeText(query)
	if normalized == "" {
		return nil
	}

	baseQueries := []string{normalized}
	baseQueries = append(baseQueries, buildYearSearchQueries(normalized, yearMilestones)...)
	rewritten := normalized
	replacer := strings.NewReplacer(
		"你们家", "青禾家居",
		"你家", "青禾家居",
		"你们", "青禾家居",
		"你", "青禾家居",
		"这个品牌", "青禾家居",
		"这家", "青禾家居",
	)
	rewritten = replacer.Replace(rewritten)
	rewritten = normalizeText(rewritten)
	if rewritten != "" && rewritten != normalized {
		baseQueries = append(baseQueries, rewritten)
	}

	queries := make([]string, 0, len(baseQueries)*3)
	for _, baseQuery := range uniqueStrings(baseQueries) {
		variants := []string{baseQuery}
		variants = append(variants, buildFuzzyRewriteQueries(baseQuery, retrievalTerms)...)
		for _, variant := range uniqueStrings(variants) {
			queries = append(queries, variant)
			if isFeaturedProductIntent(compactText(variant)) {
				queries = append(queries,
					"青禾家居主推产品有哪些",
					"介绍一下青禾家居重点推荐的核心单品",
				)
			}
		}
		if !strings.Contains(baseQuery, "青禾家居") {
			queries = append(queries, normalizeText("青禾家居 "+baseQuery))
		}
	}
	return uniqueStrings(queries)
}

func buildYearSearchQueries(query string, milestones []knowledgebase.YearMilestone) []string {
	year, ok := extractReferencedYear(query, milestones)
	if !ok {
		return nil
	}

	out := make([]string, 0, 3)
	expanded := normalizeYearQuery(query, year)
	if expanded != "" && expanded != query {
		out = append(out, expanded)
	}
	if milestone, ok := findYearMilestoneByYear(milestones, year); ok && milestone.Title != "" {
		out = append(out, normalizeText(fmt.Sprintf("%d 年 %s", year, milestone.Title)))
	}
	return uniqueStrings(out)
}

func buildYearMilestoneReply(query string, milestones []knowledgebase.YearMilestone) (string, bool) {
	year, ok := extractReferencedYear(query, milestones)
	if !ok {
		return "", false
	}

	normalized := normalizeYearQuery(query, year)
	if !isYearMilestoneIntent(compactText(normalized)) {
		return "", false
	}

	milestone, ok := findYearMilestoneByYear(milestones, year)
	if !ok {
		years := listMilestoneYears(milestones)
		if len(years) == 0 {
			return "", false
		}
		return fmt.Sprintf("按当前资料，知识库里未单独记录 %d 年的年度里程碑；目前明确列出的年份节点有 %s。😊", year, strings.Join(years, "、")), true
	}

	switch {
	case milestone.Title != "" && milestone.Summary != "":
		return fmt.Sprintf("按当前资料，%d 年青禾家居的里程碑是%s：%s 😊", year, milestone.Title, milestone.Summary), true
	case milestone.Summary != "":
		return fmt.Sprintf("按当前资料，%d 年青禾家居%s 😊", year, milestone.Summary), true
	case milestone.Title != "":
		return fmt.Sprintf("按当前资料，%d 年青禾家居的里程碑是%s 😊", year, milestone.Title), true
	default:
		return "", false
	}
}

func extractReferencedYear(query string, milestones []knowledgebase.YearMilestone) (int, bool) {
	match := yearMentionPattern.FindStringSubmatch(query)
	if len(match) != 2 {
		return 0, false
	}
	raw := strings.TrimSpace(match[1])
	year, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if len(raw) == 4 {
		return year, true
	}
	return resolveShortYear(year, milestones), true
}

func resolveShortYear(year int, milestones []knowledgebase.YearMilestone) int {
	candidates := []int{2000 + year, 1900 + year}
	for _, candidate := range candidates {
		if _, ok := findYearMilestoneByYear(milestones, candidate); ok {
			return candidate
		}
	}
	if year <= 29 {
		return 2000 + year
	}
	return 1900 + year
}

func normalizeYearQuery(query string, year int) string {
	return normalizeText(yearMentionPattern.ReplaceAllString(query, fmt.Sprintf("%d年", year)))
}

func findYearMilestoneByYear(milestones []knowledgebase.YearMilestone, year int) (knowledgebase.YearMilestone, bool) {
	for _, item := range milestones {
		if item.Year == year {
			return item, true
		}
	}
	return knowledgebase.YearMilestone{}, false
}

func listMilestoneYears(milestones []knowledgebase.YearMilestone) []string {
	out := make([]string, 0, len(milestones))
	for _, item := range milestones {
		out = append(out, fmt.Sprintf("%d年", item.Year))
	}
	return out
}

func isYearMilestoneIntent(compact string) bool {
	if compact == "" {
		return false
	}
	if !containsAny(compact,
		"做了啥", "做了什么", "做过什么", "干了啥", "干了什么", "发生了什么",
		"有什么动作", "有什么进展", "有什么变化", "里程碑", "历程", "发展",
		"那年", "这一年", "那一年", "当年",
	) {
		return false
	}
	if containsAny(compact, "价格", "售价", "多少钱", "材质", "尺寸", "配送", "发货", "售后") {
		return false
	}
	return true
}

type preparedRetrievalTerm struct {
	Text    string
	Compact string
}

type fuzzyRewriteCandidate struct {
	Text  string
	Score float64
}

func buildFuzzyRewriteQueries(query string, retrievalTerms []string) []string {
	terms := prepareRetrievalTerms(retrievalTerms)
	if len(terms) == 0 {
		return nil
	}

	normalized := normalizeText(query)
	if normalized == "" {
		return nil
	}

	runes := []rune(normalized)
	rewrites := make(map[string]float64, maxFuzzyRewrites*2)
	for start := 0; start < len(runes); start++ {
		if !unicode.Is(unicode.Han, runes[start]) {
			continue
		}
		end := start
		for end < len(runes) && unicode.Is(unicode.Han, runes[end]) {
			end++
		}
		collectSequenceRewriteCandidates(runes, normalized, start, end, terms, rewrites)
		start = end - 1
	}

	if len(rewrites) == 0 {
		return nil
	}

	candidates := make([]fuzzyRewriteCandidate, 0, len(rewrites))
	for text, score := range rewrites {
		candidates = append(candidates, fuzzyRewriteCandidate{Text: text, Score: score})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			ri := len([]rune(candidates[i].Text))
			rj := len([]rune(candidates[j].Text))
			if ri == rj {
				return candidates[i].Text < candidates[j].Text
			}
			return ri < rj
		}
		return candidates[i].Score > candidates[j].Score
	})

	out := make([]string, 0, maxFuzzyRewrites)
	for _, item := range candidates {
		out = append(out, item.Text)
		if len(out) >= maxFuzzyRewrites {
			break
		}
	}
	return out
}

func prepareRetrievalTerms(terms []string) []preparedRetrievalTerm {
	out := make([]preparedRetrievalTerm, 0, len(terms))
	seen := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		compact := compactText(term)
		runes := []rune(compact)
		if len(runes) < 2 || len(runes) > 8 {
			continue
		}
		if !containsHanTerm(compact) {
			continue
		}
		if _, ok := seen[compact]; ok {
			continue
		}
		seen[compact] = struct{}{}
		out = append(out, preparedRetrievalTerm{
			Text:    normalizeText(term),
			Compact: compact,
		})
		if len(out) >= 256 {
			break
		}
	}
	return out
}

func collectSequenceRewriteCandidates(queryRunes []rune, original string, seqStart int, seqEnd int, terms []preparedRetrievalTerm, rewrites map[string]float64) {
	seq := queryRunes[seqStart:seqEnd]
	maxWindow := 6
	if maxWindow > len(seq) {
		maxWindow = len(seq)
	}
	for size := 2; size <= maxWindow; size++ {
		for offset := 0; offset+size <= len(seq); offset++ {
			fragment := string(seq[offset : offset+size])
			if !isMeaningfulFuzzyFragment(fragment) {
				continue
			}
			replacement, score := bestFuzzyRetrievalTerm(fragment, terms)
			if replacement == "" || score < 0.72 {
				continue
			}
			globalStart := seqStart + offset
			globalEnd := globalStart + size
			rewritten := replaceRuneRange(queryRunes, globalStart, globalEnd, []rune(replacement))
			rewrittenText := normalizeText(string(rewritten))
			if rewrittenText == original {
				continue
			}
			if prev, ok := rewrites[rewrittenText]; !ok || score > prev {
				rewrites[rewrittenText] = score
			}
		}
	}
}

func bestFuzzyRetrievalTerm(fragment string, terms []preparedRetrievalTerm) (string, float64) {
	compact := compactText(fragment)
	if compact == "" {
		return "", 0
	}

	bestTerm := ""
	bestScore := 0.0
	for _, term := range terms {
		score := scoreFuzzyTerms(compact, term.Compact)
		if score > bestScore {
			bestScore = score
			bestTerm = term.Text
		}
	}
	if compactText(bestTerm) == compact {
		return "", 0
	}
	return bestTerm, bestScore
}

func scoreFuzzyTerms(fragment string, candidate string) float64 {
	a := []rune(fragment)
	b := []rune(candidate)
	if len(a) < 2 || len(b) < 2 {
		return 0
	}

	lenDiff := len(a) - len(b)
	if lenDiff < 0 {
		lenDiff = -lenDiff
	}
	if lenDiff > 1 {
		return 0
	}

	dist := levenshteinDistance(a, b)
	if dist == 0 {
		return 1
	}
	if dist > 2 {
		return 0
	}

	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}
	score := 1 - float64(dist)/float64(maxLen)
	score -= 0.18 * float64(lenDiff)

	shared := sharedRuneCount(a, b)
	score += 0.12 * (float64(shared) / float64(maxLen))

	if a[0] == b[0] {
		score += 0.12
	}
	if a[len(a)-1] == b[len(b)-1] {
		score += 0.12
	}
	if len(a) == len(b) && dist == 1 && (a[0] == b[0] || a[len(a)-1] == b[len(b)-1]) {
		score += 0.08
	}
	if score > 0.99 {
		return 0.99
	}
	return score
}

func levenshteinDistance(a []rune, b []rune) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := 0; j <= len(b); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			insertCost := curr[j-1] + 1
			deleteCost := prev[j] + 1
			replaceCost := prev[j-1] + cost
			curr[j] = minInt(insertCost, deleteCost, replaceCost)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

func sharedRuneCount(a []rune, b []rune) int {
	counts := make(map[rune]int, len(a))
	for _, r := range a {
		counts[r]++
	}
	shared := 0
	for _, r := range b {
		if counts[r] <= 0 {
			continue
		}
		counts[r]--
		shared++
	}
	return shared
}

func replaceRuneRange(input []rune, start int, end int, replacement []rune) []rune {
	out := make([]rune, 0, len(input)-(end-start)+len(replacement))
	out = append(out, input[:start]...)
	out = append(out, replacement...)
	out = append(out, input[end:]...)
	return out
}

func isMeaningfulFuzzyFragment(fragment string) bool {
	runes := []rune(compactText(fragment))
	if len(runes) < 2 {
		return false
	}
	meaningful := 0
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
		switch r {
		case '你', '我', '他', '她', '它', '们', '的', '了', '吗', '呢', '啊', '呀', '请', '想', '问', '看', '帮', '下', '在', '有', '是', '和', '与', '及':
			continue
		default:
			meaningful++
		}
	}
	return meaningful >= 2 || (len(runes) == 2 && meaningful >= 1)
}

func containsHanTerm(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func minInt(values ...int) int {
	best := values[0]
	for _, value := range values[1:] {
		if value < best {
			best = value
		}
	}
	return best
}

func matchPresetReply(query string) (string, bool) {
	compact := compactText(query)
	if compact == "" {
		return "", false
	}

	if isExactMatch(compact, "你好", "您好", "hello", "hi", "哈喽", "嗨", "在吗", "有人吗", "在不在", "你好呀", "您好呀", "你好啊", "您好啊") {
		return "您好呀，这里是青禾家居客服 😊 很高兴为您服务。您想了解产品、材质、配送、售后，还是品牌信息呢？", true
	}
	if containsAny(compact,
		"你是谁",
		"你叫什么",
		"怎么称呼你",
		"你是做什么的",
		"你是干什么的",
		"你能做什么",
		"你会什么",
		"你可以帮我什么",
		"你能帮我什么",
		"你们客服在吗",
		"这是人工吗",
	) {
		return "您好呀，我是青禾家居客服 😊 我可以为您介绍青禾家居的产品、材质、配送、售后和品牌相关信息；如果遇到现有资料暂未明确说明的问题，我也会提醒您联系人工客服进一步核实。", true
	}
	if isExactMatch(compact, "谢谢", "多谢", "感谢", "谢了", "thanks", "thankyou", "thankyouverymuch") {
		return "不客气呀，这是我应该做的 😊 如果您还想了解青禾家居的产品或服务信息，随时告诉我。", true
	}
	if isExactMatch(compact, "再见", "拜拜", "bye", "seeyou", "回头聊") {
		return "好的，感谢您咨询青禾家居呀 🌿 后续如需了解更多信息，随时来找我。", true
	}
	return "", false
}

func buildStructuredFacts(query string, prices []knowledgebase.ProductPrice) string {
	if len(prices) == 0 {
		return ""
	}
	if !needsStructuredPriceFacts(query, prices) {
		return ""
	}

	sorted := make([]knowledgebase.ProductPrice, len(prices))
	copy(sorted, prices)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].PriceValue == sorted[j].PriceValue {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].PriceValue > sorted[j].PriceValue
	})

	var builder strings.Builder
	builder.WriteString("补充结构化事实如下：\n")
	builder.WriteString("主推产品价格表：\n")
	for _, item := range sorted {
		builder.WriteString("- ")
		builder.WriteString(item.Name)
		builder.WriteString("：")
		builder.WriteString(formatPriceValue(item.PriceValue))
		builder.WriteString("（原文：")
		builder.WriteString(item.PriceText)
		builder.WriteString("）\n")
	}
	builder.WriteString("使用要求：\n")
	builder.WriteString("- 如果用户询问最贵、最便宜、最昂贵、最高、最低、多少钱、价格高于/低于/以内/以上、主推产品数量等问题，必须先依据上表完成比较、筛选或统计，再回答。\n")
	builder.WriteString("- 上表已经提供了可直接比较的价格事实时，不要回答“资料未提及”或“无法确认”。")
	return strings.TrimSpace(builder.String())
}

func needsStructuredPriceFacts(query string, prices []knowledgebase.ProductPrice) bool {
	compact := compactText(query)
	if compact == "" {
		return false
	}
	if containsAny(compact,
		"最贵", "最便宜", "最昂贵", "最便宜", "价格最高", "价格最低", "售价最高", "售价最低", "最高价", "最低价",
		"多少钱", "价格", "售价", "零售价", "价位",
		"多少个主推产品", "多少款主推产品", "一共多少个主推产品", "一共多少款主推产品", "主推产品有多少", "价格表里有多少产品", "价格表里有多少款产品",
	) {
		return true
	}
	if _, _, ok := parsePriceThresholdQuery(query); ok {
		return true
	}
	for _, item := range prices {
		if strings.Contains(normalizeText(query), item.Name) || strings.Contains(compact, compactText(item.Name)) {
			return true
		}
	}
	return false
}

func buildPromptBody(conversationHistory string, contextText string, structuredFacts string, question string) string {
	var builder strings.Builder
	builder.WriteString("/no_think\n")
	if strings.TrimSpace(conversationHistory) != "" {
		builder.WriteString("最近对话如下：\n")
		builder.WriteString(conversationHistory)
		builder.WriteString("\n\n")
	}
	if strings.TrimSpace(contextText) != "" {
		builder.WriteString("知识片段如下：\n")
		builder.WriteString(contextText)
		builder.WriteString("\n\n")
	}
	if strings.TrimSpace(structuredFacts) != "" {
		builder.WriteString(structuredFacts)
		builder.WriteString("\n\n")
	}
	builder.WriteString("用户问题：")
	builder.WriteString(question)
	builder.WriteString("\n\n请直接用中文回答。")
	return builder.String()
}

func buildTermContextReply(query string, results []knowledgebase.SearchResult) (string, bool) {
	if len(results) == 0 {
		return "", false
	}
	if !isTermDefinitionQuestion(query) {
		return "", false
	}

	terms := extractQuestionTerms(query)
	if len(terms) == 0 {
		return "", false
	}

	term := terms[0]
	contexts := collectTermContexts(term, results)
	if len(contexts) == 0 {
		return "", false
	}

	switch inferTermCategory(term, contexts) {
	case "product-code":
		return fmt.Sprintf("按当前资料，%s 在这里主要作为产品编号或商品管理标识使用，常见于产品参数卡、SKU 速查表和采购报价场景中。😊", term), true
	case "contact":
		return fmt.Sprintf("按当前资料，%s 主要出现在联系方式相关场景中，用于邮箱、域名或对外联系口径。😊", term), true
	case "brand-name":
		return fmt.Sprintf("按当前资料，%s 在这里主要作为品牌英文名称或品牌相关标识使用。😊", term), true
	default:
		return fmt.Sprintf("按当前资料，%s 主要出现在这些场景：%s。如果您想看它对应的具体条目或上下文，我也可以继续帮您查。😊", term, strings.Join(contexts, "、")), true
	}
}

func buildDeterministicReply(query string, prices []knowledgebase.ProductPrice, retrievalTerms []string) (string, bool) {
	if len(prices) == 0 {
		return "", false
	}

	compact := compactText(query)
	if compact == "" {
		return "", false
	}

	sorted := sortProductPrices(prices)

	switch {
	case isHighestPriceQuestion(compact):
		item := sorted[0]
		return fmt.Sprintf("青禾家居当前主推产品里，价格最高的是%s，建议零售价为%s 😊", item.Name, item.PriceText), true
	case isLowestPriceQuestion(compact):
		item := sorted[len(sorted)-1]
		return fmt.Sprintf("青禾家居当前主推产品里，价格最低的是%s，建议零售价为%s 😊", item.Name, item.PriceText), true
	case isProductCountQuestion(compact):
		names := make([]string, 0, len(sorted))
		for _, item := range sorted {
			names = append(names, item.Name)
		}
		return fmt.Sprintf("按当前资料，青禾家居重点展示的主推产品共%d款，分别是%s。😊", len(sorted), strings.Join(names, "、")), true
	}

	if mode, value, ok := parsePriceThresholdQuery(query); ok {
		filtered := filterProductPricesByThreshold(sorted, mode, value)
		if len(filtered) == 0 {
			return fmt.Sprintf("按当前资料，主推产品里没有符合%s%s条件的产品。😊", describeThresholdMode(mode), formatPriceValue(value)), true
		}
		items := make([]string, 0, len(filtered))
		for _, item := range filtered {
			items = append(items, fmt.Sprintf("%s（%s）", item.Name, item.PriceText))
		}
		return fmt.Sprintf("按当前资料，主推产品里%s%s的有%d款：%s。😊", describeThresholdMode(mode), formatPriceValue(value), len(filtered), strings.Join(items, "、")), true
	}

	if isAskingPrice(compact) {
		matches := matchProductsInQuery(query, sorted)
		if len(matches) == 1 {
			item := matches[0]
			return fmt.Sprintf("%s的建议零售价为%s 😊", item.Name, item.PriceText), true
		}
	}

	if isFeaturedProductIntent(compact) {
		return buildFeaturedProductReply(sorted, compact), true
	}
	for _, rewritten := range buildFuzzyRewriteQueries(query, retrievalTerms) {
		if isFeaturedProductIntent(compactText(rewritten)) {
			return buildFeaturedProductReply(sorted, compactText(rewritten)), true
		}
	}

	return "", false
}

func formatPriceValue(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d元", int64(value))
	}
	return fmt.Sprintf("%.2f元", value)
}

func sortProductPrices(prices []knowledgebase.ProductPrice) []knowledgebase.ProductPrice {
	sorted := make([]knowledgebase.ProductPrice, len(prices))
	copy(sorted, prices)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].PriceValue == sorted[j].PriceValue {
			return sorted[i].Name < sorted[j].Name
		}
		return sorted[i].PriceValue > sorted[j].PriceValue
	})
	return sorted
}

func isHighestPriceQuestion(compact string) bool {
	return containsAny(compact, "最贵", "最昂贵", "价格最高", "售价最高", "最高价")
}

func isLowestPriceQuestion(compact string) bool {
	return containsAny(compact, "最便宜", "价格最低", "售价最低", "最低价")
}

func isProductCountQuestion(compact string) bool {
	return containsAny(compact,
		"多少个主推产品", "多少款主推产品", "一共多少个主推产品", "一共多少款主推产品", "主推产品有多少", "价格表里有多少产品", "价格表里有多少款产品",
	)
}

func isFeaturedProductIntent(compact string) bool {
	if compact == "" {
		return false
	}
	if containsAny(compact, "价格", "售价", "多少钱", "最贵", "最便宜", "最高价", "最低价") {
		return false
	}
	if !(containsAny(compact, "讲讲", "介绍", "说说", "推荐", "看看", "了解", "想买", "想看", "有哪些", "哪个", "哪款", "最好", "最好的", "最推荐", "最值得买", "爆款", "明星", "王牌", "招牌", "主推", "核心") &&
		containsAny(compact, "产品", "单品", "商品", "款", "系列")) {
		return false
	}
	return true
}

func buildFeaturedProductReply(prices []knowledgebase.ProductPrice, compact string) string {
	names := make([]string, 0, len(prices))
	for _, item := range prices {
		names = append(names, item.Name)
	}
	if containsAny(compact, "最好", "最好的", "最推荐", "最值得买") {
		return fmt.Sprintf("按当前资料，没有唯一能被定义为“最好”的单一产品；青禾家居当前重点展示和推荐的核心单品包括%s。😊", strings.Join(names, "、"))
	}
	return fmt.Sprintf("按当前资料，青禾家居当前重点展示和推荐的核心单品包括%s。😊", strings.Join(names, "、"))
}

func isTermDefinitionQuestion(query string) bool {
	compact := compactText(query)
	if compact == "" {
		return false
	}
	if !termTokenPattern.MatchString(query) {
		return false
	}
	return containsAny(compact, "是啥", "是什么", "什么意思", "啥意思", "缩写", "全称", "怎么理解", "含义", "代表什么", "是什么东西")
}

func extractQuestionTerms(query string) []string {
	matches := termTokenPattern.FindAllString(query, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]string, 0, len(matches))
	for _, item := range matches {
		key := strings.ToLower(strings.TrimSpace(item))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}

func collectTermContexts(term string, results []knowledgebase.SearchResult) []string {
	termLower := strings.ToLower(term)
	seen := map[string]struct{}{}
	out := make([]string, 0, 3)
	for _, item := range results {
		section := fallbackSection(item.Section)
		textLower := strings.ToLower(item.Text)
		sectionLower := strings.ToLower(section)
		if !strings.Contains(textLower, termLower) && !strings.Contains(sectionLower, termLower) {
			continue
		}

		candidates := []string{}
		switch {
		case strings.Contains(item.Text, "SKU 编号") || strings.Contains(item.Text, "SKU 速查表") || strings.Contains(item.Text, "常销 SKU") || strings.Contains(item.Text, "标准 SKU"):
			candidates = append(candidates, "产品编号与商品管理")
		case strings.Contains(item.Text, "邮箱") || strings.Contains(item.Text, "域名") || strings.Contains(item.Text, "官网域名口径"):
			candidates = append(candidates, "联系方式与对外联系口径")
		case strings.Contains(item.Text, "英文名称") || strings.Contains(item.Text, "核心商标"):
			candidates = append(candidates, "品牌英文名称与品牌标识")
		}
		candidates = append(candidates, section)

		for _, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			out = append(out, candidate)
			if len(out) >= 3 {
				return out
			}
		}
	}
	return out
}

func inferTermCategory(term string, contexts []string) string {
	termLower := strings.ToLower(term)
	if termLower == "sku" {
		return "product-code"
	}
	for _, item := range contexts {
		switch {
		case strings.Contains(item, "商品管理") || strings.Contains(item, "产品编号"):
			return "product-code"
		case strings.Contains(item, "联系方式") || strings.Contains(item, "联系口径"):
			return "contact"
		case strings.Contains(item, "英文名称") || strings.Contains(item, "品牌标识"):
			return "brand-name"
		}
	}
	return ""
}

func isAskingPrice(compact string) bool {
	return containsAny(compact, "多少钱", "价格", "售价", "零售价", "价位")
}

func matchProductsInQuery(query string, prices []knowledgebase.ProductPrice) []knowledgebase.ProductPrice {
	normalized := normalizeText(query)
	compact := compactText(query)
	matches := make([]knowledgebase.ProductPrice, 0, 2)
	for _, item := range prices {
		if strings.Contains(normalized, item.Name) || strings.Contains(compact, compactText(item.Name)) {
			matches = append(matches, item)
		}
	}
	return matches
}

func filterProductPricesByThreshold(prices []knowledgebase.ProductPrice, mode string, value float64) []knowledgebase.ProductPrice {
	out := make([]knowledgebase.ProductPrice, 0, len(prices))
	for _, item := range prices {
		switch mode {
		case "gt":
			if item.PriceValue > value {
				out = append(out, item)
			}
		case "gte":
			if item.PriceValue >= value {
				out = append(out, item)
			}
		case "lt":
			if item.PriceValue < value {
				out = append(out, item)
			}
		case "lte":
			if item.PriceValue <= value {
				out = append(out, item)
			}
		}
	}
	return out
}

func describeThresholdMode(mode string) string {
	switch mode {
	case "gt":
		return "高于"
	case "gte":
		return "不低于"
	case "lt":
		return "低于"
	case "lte":
		return "不超过"
	default:
		return ""
	}
}

func matchClarifyReply(query string) (string, bool) {
	compact := compactText(query)
	if compact == "" {
		return "", false
	}
	if containsAny(compact,
		"有推荐吗",
		"怎么选",
		"怎么挑",
		"怎么搭",
		"有什么推荐",
		"推荐一下",
		"介绍一下",
		"说说看",
		"看看有什么",
		"想了解一下",
		"想咨询一下",
	) {
		return "当然可以呀 😊 您是想了解青禾家居的产品、材质、配送、售后，还是品牌信息？您告诉我具体方向后，我按现有资料为您介绍。", true
	}
	return "", false
}

func parsePriceThresholdQuery(query string) (string, float64, bool) {
	query = normalizeText(query)
	if match := priceThresholdAbovePattern.FindStringSubmatch(query); len(match) == 2 {
		value, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			if strings.Contains(query, "不低于") || strings.Contains(query, "至少") || strings.Contains(query, "起码") {
				return "gte", value, true
			}
			return "gt", value, true
		}
	}
	if match := priceThresholdBelowPattern.FindStringSubmatch(query); len(match) == 2 {
		value, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			if strings.Contains(query, "不超过") || strings.Contains(query, "不高于") {
				return "lte", value, true
			}
			return "lt", value, true
		}
	}
	if match := priceWithinPattern.FindStringSubmatch(query); len(match) == 2 {
		value, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			return "lte", value, true
		}
	}
	if match := priceFromPattern.FindStringSubmatch(query); len(match) == 2 {
		value, err := strconv.ParseFloat(match[1], 64)
		if err == nil {
			return "gte", value, true
		}
	}
	return "", 0, false
}

func compactText(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func isExactMatch(query string, candidates ...string) bool {
	for _, candidate := range candidates {
		if query == candidate {
			return true
		}
	}
	return false
}

func containsAny(query string, candidates ...string) bool {
	for _, candidate := range candidates {
		if strings.Contains(query, candidate) {
			return true
		}
	}
	return false
}

func uniqueStrings(items []string) []string {
	out := make([]string, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}
