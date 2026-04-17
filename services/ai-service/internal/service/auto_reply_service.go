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
	maxStateContextMessages  = 4
	maxStateSummaryChars     = 320
	maxPromptContextChars    = 3600
	maxPromptDocumentChars   = 64000
	maxPromptHistoryChars    = 1200
	maxPromptHistoryMessages = 6
	promptHistoryFetchLimit  = maxPromptHistoryMessages + 4
	replyModeAutoOnly        = "unassigned_auto_reply"
	introSearchTemplate      = "品牌介绍 产品介绍 家居生活品牌 四大产品线 产品矩阵 主推产品"
	featuredSearchTemplate   = "青禾家居的主推产品有哪些 当前重点展示产品包括哪些 明星单品 重点展示产品"
)

var thinkTagPattern = regexp.MustCompile(`(?is)<think>.*?</think>`)
var smallTalkStripPattern = regexp.MustCompile(`[\pP\pS\pZ]+`)
var sectionOrderPrefixPattern = regexp.MustCompile(`^\d+(?:\.\d+)*\.?\s*`)
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

	state := buildConversationState(query, recentMessages, event.Message.ID)
	searchQuery := buildSearchQuery(query, state)
	searchCtx, searchCancel := s.withCallTimeout(ctx)
	results, err := s.kb.Search(searchCtx, conversation.SiteID, searchQuery)
	searchCancel()
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
	if reply, ok := buildDirectKnowledgeReply(query, results, s.unknownReply); ok {
		return s.createReply(ctx, event, reply)
	}

	conversationHistory := buildConversationHistory(recentMessages, event.Message.ID, maxPromptHistoryMessages, maxPromptHistoryChars)
	primaryDocument, docErr := s.kb.LoadPrimaryDocument(conversation.SiteID)
	if docErr != nil {
		s.logger.Warn("load primary knowledge document failed",
			zap.Error(docErr),
			zap.String("site_id", conversation.SiteID),
			zap.Uint64("conversation_id", event.ConversationID),
		)
	}
	replyCtx, replyCancel := s.withCallTimeout(ctx)
	reply, err := s.generateReply(replyCtx, query, state.Summary, conversationHistory, results, primaryDocument)
	replyCancel()
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

func (s *AutoReplyService) generateReply(ctx context.Context, question string, stateSummary string, conversationHistory string, results []knowledgebase.SearchResult, primaryDocument string) (string, error) {
	promptBody := buildKnowledgePromptBody(stateSummary, conversationHistory, primaryDocument, results, question)
	if promptBody == "" {
		return s.unknownReply, nil
	}
	reply, err := s.llmClient.ChatCompletion(ctx, []openai.ChatMessage{
		{
			Role: "system",
			Content: fmt.Sprintf(
				"你是网站在线客服。你只能依据提供的站点知识内容回答问题，不得编造、补全或猜测未提供的信息。"+
					"如果提供了知识全文，你必须先在知识全文中查找答案；如果全文仍不足，再参考知识片段。"+
					"如果提供的知识内容仍不足以直接回答，必须只回复：%s "+
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

func (s *AutoReplyService) withCallTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.callTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, s.callTimeout)
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

func buildDirectKnowledgeReply(question string, results []knowledgebase.SearchResult, fallbackText string) (string, bool) {
	if len(results) == 0 {
		return "", false
	}

	question = normalizeQuery(question)
	for _, item := range results {
		if item.Kind == knowledgebase.ChunkKindFAQ {
			if answer, ok := extractFAQAnswerText(item.Text); ok {
				return sanitizeModelReply(answer, fallbackText), true
			}
		}
	}

	if isListSeekingQuestion(question) {
		if reply, ok := formatListReply(results, fallbackText); ok {
			return reply, true
		}
	}

	if reply, ok := formatBestFactReply(question, results, fallbackText); ok {
		return reply, true
	}
	if reply, ok := formatNarrativeOverviewReply(question, results, fallbackText); ok {
		return reply, true
	}
	return "", false
}

func extractFAQAnswerText(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	marker := "答案："
	idx := strings.Index(text, marker)
	if idx < 0 {
		return "", false
	}
	answer := strings.TrimSpace(text[idx+len(marker):])
	if answer == "" {
		return "", false
	}
	return answer, true
}

func isListSeekingQuestion(question string) bool {
	if question == "" {
		return false
	}
	for _, keyword := range []string{
		"有哪些", "哪些", "有什么", "有啥", "哪几个", "哪几款", "主推产品", "明星单品", "推荐产品",
	} {
		if strings.Contains(question, keyword) {
			return true
		}
	}
	return false
}

func formatListReply(results []knowledgebase.SearchResult, fallbackText string) (string, bool) {
	names := make([]string, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, item := range results {
		name := extractProductName(item.Text)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "", false
	}
	reply := "当前资料中可确认的明星单品包括：" + strings.Join(names, "、") + "。"
	return sanitizeModelReply(reply, fallbackText), true
}

type factPair struct {
	Key   string
	Value string
}

func formatBestFactReply(question string, results []knowledgebase.SearchResult, fallbackText string) (string, bool) {
	compactQuestion := compactQuery(question)
	if compactQuestion == "" {
		return "", false
	}
	queryTokens := extractCompactMatchTokens(question)
	if len(queryTokens) == 0 {
		return "", false
	}
	questionConcepts := inferQuestionFieldConcepts(compactQuestion)

	bestReply := ""
	bestScore := 0.0
	bestFieldScore := 0.0
	singleAnswer := looksLikeDirectAnswerQuestion(question)
	for _, item := range results {
		if item.Kind != knowledgebase.ChunkKindFact {
			continue
		}
		for _, pair := range extractFactPairs(item.Text) {
			totalScore, fieldScore := scoreFactPairReply(item, compactQuestion, queryTokens, questionConcepts, pair)
			if totalScore <= 0 {
				continue
			}
			reply := pair.Key + "：" + pair.Value
			if totalScore > bestScore || (totalScore == bestScore && fieldScore > bestFieldScore) {
				bestReply = reply
				bestScore = totalScore
				bestFieldScore = fieldScore
			}
		}
	}
	if bestReply == "" {
		return "", false
	}
	if bestFieldScore >= 1.2 || bestScore >= 3.2 || (singleAnswer && bestFieldScore > 0 && bestScore >= 1.0) {
		return sanitizeModelReply(bestReply, fallbackText), true
	}
	return "", false
}

func extractFactPairs(text string) []factPair {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	out := make([]factPair, 0, len(lines))
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		sep := strings.IndexAny(line, "：:")
		if sep <= 0 || sep >= len(line)-1 {
			continue
		}
		sepWidth := 1
		if strings.HasPrefix(line[sep:], "：") {
			sepWidth = len("：")
		}
		key := strings.TrimSpace(line[:sep])
		value := strings.TrimSpace(line[sep+sepWidth:])
		if key == "" || value == "" {
			continue
		}
		out = append(out, factPair{Key: key, Value: value})
	}
	return out
}

func scoreFactPairReply(item knowledgebase.SearchResult, compactQuestion string, queryTokens []string, questionConcepts []string, pair factPair) (float64, float64) {
	compactKey := compactQuery(pair.Key)
	if compactKey == "" {
		return 0, 0
	}

	fieldScore := scoreCompactTokenMatches(compactKey, queryTokens, 0.38, 0.08)
	if strings.Contains(compactQuestion, compactKey) || strings.Contains(compactKey, compactQuestion) {
		fieldScore += 2.8
	}
	fieldConcepts := inferFactFieldConcepts(pair.Key, pair.Value)
	fieldScore += scoreFieldConceptOverlap(questionConcepts, fieldConcepts)

	metadata := item.Section + "\n" + strings.Join(item.Keywords, "\n") + "\n" + pair.Value
	metadataScore := scoreCompactTokenMatches(compactQuery(metadata), queryTokens, 0.14, 0.04)
	if name := extractProductName(item.Text); name != "" && strings.Contains(compactQuestion, compactQuery(name)) {
		metadataScore += 0.6
	}
	return fieldScore + metadataScore, fieldScore
}

func scoreCompactTokenMatches(compactText string, tokens []string, base float64, scale float64) float64 {
	if compactText == "" || len(tokens) == 0 {
		return 0
	}
	score := 0.0
	matches := 0
	for _, token := range tokens {
		if token == "" || !strings.Contains(compactText, token) {
			continue
		}
		score += base + float64(minInt(runeCount(token), 6))*scale
		matches++
		if matches >= 8 {
			break
		}
	}
	return score
}

func extractCompactMatchTokens(text string) []string {
	compact := compactQuery(text)
	if compact == "" {
		return nil
	}
	seen := make(map[string]struct{}, 32)
	out := make([]string, 0, 32)
	appendToken := func(token string) {
		token = strings.TrimSpace(token)
		if runeCount(token) < 2 || isGenericQuestionToken(token) {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	appendToken(compact)
	for _, alias := range expandQuestionAliases(compact) {
		appendToken(alias)
	}
	runes := []rune(compact)
	for size := minInt(len(runes), 6); size >= 2; size-- {
		for start := 0; start+size <= len(runes); start++ {
			appendToken(string(runes[start : start+size]))
			if len(out) >= 64 {
				return out
			}
		}
	}
	return out
}

func isGenericQuestionToken(token string) bool {
	switch token {
	case "请问", "可以", "是否", "能否", "可否", "怎么", "如何", "什么", "多少", "支持", "一下", "多少钱":
		return true
	default:
		return false
	}
}

func expandQuestionAliases(compact string) []string {
	if compact == "" {
		return nil
	}
	out := make([]string, 0, 12)
	for _, concept := range inferQuestionFieldConcepts(compact) {
		switch concept {
		case "price":
			out = append(out, "价格", "价钱", "售价", "零售价", "建议零售价", "报价")
		case "invoice":
			out = append(out, "发票", "开票", "专票", "普票", "增值税专用发票", "普通发票")
		case "size":
			out = append(out, "尺寸", "规格", "大小", "长宽高")
		case "material":
			out = append(out, "材质", "材料", "面料")
		case "shipping":
			out = append(out, "发货", "物流", "配送", "时效")
		case "aftersale":
			out = append(out, "售后", "退换", "退货", "保修", "无理由")
		case "location":
			out = append(out, "地址", "位置", "所在地")
		case "scenario":
			out = append(out, "适用场景", "使用场景", "适合场景")
		}
	}
	return out
}

func inferQuestionFieldConcepts(compact string) []string {
	if compact == "" {
		return nil
	}
	out := make([]string, 0, 4)
	appendConcept := func(concept string) {
		for _, item := range out {
			if item == concept {
				return
			}
		}
		out = append(out, concept)
	}
	for _, rule := range []struct {
		concept  string
		keywords []string
	}{
		{concept: "price", keywords: []string{"多少钱", "多少元", "价格", "价钱", "售价", "报价", "费用"}},
		{concept: "invoice", keywords: []string{"发票", "开票", "专票", "普票", "税票"}},
		{concept: "size", keywords: []string{"尺寸", "规格", "多大", "多长", "多宽", "多高", "大小"}},
		{concept: "material", keywords: []string{"材质", "材料", "面料", "什么做的"}},
		{concept: "shipping", keywords: []string{"发货", "物流", "配送", "时效", "多久到", "多久发"}},
		{concept: "aftersale", keywords: []string{"售后", "退换", "退货", "退款", "保修", "无理由"}},
		{concept: "location", keywords: []string{"地址", "在哪里", "在哪儿", "在哪", "位置", "所在地"}},
		{concept: "scenario", keywords: []string{"适用场景", "使用场景", "适合场景", "适合什么场景", "用途"}},
	} {
		for _, keyword := range rule.keywords {
			if strings.Contains(compact, compactQuery(keyword)) {
				appendConcept(rule.concept)
				break
			}
		}
	}
	return out
}

func inferFactFieldConcepts(key string, value string) []string {
	compactKey := compactQuery(key)
	compactValue := compactQuery(value)
	if compactKey == "" && compactValue == "" {
		return nil
	}
	out := make([]string, 0, 4)
	appendConcept := func(concept string) {
		for _, item := range out {
			if item == concept {
				return
			}
		}
		out = append(out, concept)
	}
	if containsAnyCompact(compactKey, "价", "售价", "零售价", "报价", "金额", "费用") || strings.ContainsAny(value, "¥￥") {
		appendConcept("price")
	}
	if containsAnyCompact(compactKey, "发票", "开票", "专票", "普票", "税票") {
		appendConcept("invoice")
	}
	if containsAnyCompact(compactKey, "尺寸", "规格", "长", "宽", "高", "大小", "口径", "容量") {
		appendConcept("size")
	}
	if containsAnyCompact(compactKey, "材质", "材料", "面料") {
		appendConcept("material")
	}
	if containsAnyCompact(compactKey, "发货", "物流", "配送", "时效", "到货") {
		appendConcept("shipping")
	}
	if containsAnyCompact(compactKey, "售后", "退换", "退货", "退款", "保修", "无理由") {
		appendConcept("aftersale")
	}
	if containsAnyCompact(compactKey, "地址", "位置", "所在地", "地点") {
		appendConcept("location")
	}
	if containsAnyCompact(compactKey, "场景", "用途", "适用", "使用") {
		appendConcept("scenario")
	}
	return out
}

func scoreFieldConceptOverlap(questionConcepts []string, fieldConcepts []string) float64 {
	if len(questionConcepts) == 0 || len(fieldConcepts) == 0 {
		return 0
	}
	score := 0.0
	for _, left := range questionConcepts {
		for _, right := range fieldConcepts {
			if left == right {
				score += 2.4
				break
			}
		}
	}
	return score
}

func containsAnyCompact(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, compactQuery(needle)) {
			return true
		}
	}
	return false
}

func formatNarrativeOverviewReply(question string, results []knowledgebase.SearchResult, fallbackText string) (string, bool) {
	if !isOverviewSeekingQuestion(question) {
		return "", false
	}

	parts := make([]string, 0, 3)
	seen := make(map[string]struct{}, len(results))
	for _, item := range results {
		if item.Kind != knowledgebase.ChunkKindNarrative {
			continue
		}
		part := buildNarrativeOverviewPart(item)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		parts = append(parts, part)
		if len(parts) >= 3 {
			break
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	reply := strings.Join(parts, "；")
	reply = strings.TrimSpace(strings.TrimSuffix(reply, "；"))
	if reply == "" {
		return "", false
	}
	if !strings.HasSuffix(reply, "。") && !strings.HasSuffix(reply, "！") && !strings.HasSuffix(reply, "？") {
		reply += "。"
	}
	return sanitizeModelReply(reply, fallbackText), true
}

func buildNarrativeOverviewPart(item knowledgebase.SearchResult) string {
	body := compactKnowledgeText(item.Text)
	if body == "" {
		return ""
	}
	label := extractSectionLeafLabel(item.Section)
	if label == "" || strings.Contains(body, label+"：") || strings.HasPrefix(body, label) {
		return body
	}
	return label + "：" + body
}

func compactKnowledgeText(text string) string {
	lines := make([]string, 0, 8)
	for _, raw := range strings.Split(strings.TrimSpace(text), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}

	parts := make([]string, 0, len(lines))
	bullets := make([]string, 0, 4)
	pendingLabel := ""
	flushBullets := func() {
		if len(bullets) == 0 {
			return
		}
		joined := strings.Join(bullets, "、")
		if pendingLabel != "" {
			parts = append(parts, pendingLabel+joined)
		} else {
			parts = append(parts, joined)
		}
		bullets = bullets[:0]
		pendingLabel = ""
	}

	for _, line := range lines {
		if bullet := trimBulletPrefix(line); bullet != "" {
			bullets = append(bullets, bullet)
			continue
		}
		flushBullets()
		if strings.HasSuffix(line, "：") || strings.HasSuffix(line, ":") {
			pendingLabel = strings.TrimRight(line, ":：") + "："
			continue
		}
		if pendingLabel != "" {
			parts = append(parts, pendingLabel+line)
			pendingLabel = ""
			continue
		}
		parts = append(parts, line)
	}
	flushBullets()
	if pendingLabel != "" {
		parts = append(parts, strings.TrimSuffix(pendingLabel, "："))
	}
	return strings.Join(parts, "；")
}

func trimBulletPrefix(line string) string {
	line = strings.TrimSpace(line)
	for _, prefix := range []string{"- ", "* ", "• ", "· "} {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func extractSectionLeafLabel(section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return ""
	}
	parts := strings.Split(section, " / ")
	label := strings.TrimSpace(parts[len(parts)-1])
	if label == "" {
		return ""
	}
	label = sectionOrderPrefixPattern.ReplaceAllString(label, "")
	return strings.TrimSpace(label)
}

func isOverviewSeekingQuestion(question string) bool {
	question = normalizeQuery(question)
	if question == "" || isListSeekingQuestion(question) || looksLikeDirectAnswerQuestion(question) {
		return false
	}
	compact := compactQuery(question)
	for _, keyword := range []string{"介绍", "讲讲", "说说", "聊聊", "说明", "是什么", "有什么权益", "有哪些权益"} {
		if strings.Contains(compact, compactQuery(keyword)) {
			return true
		}
	}
	return isShortTopicLookupQuestion(question)
}

func extractProductName(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "产品名称：") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(line, "产品名称："))
		if name != "" {
			return name
		}
	}
	return ""
}

func formatFactReply(text string, fallbackText string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	sep := strings.Index(text, "：")
	if sep <= 0 || sep >= len(text)-len("：") {
		return "", false
	}
	key := strings.TrimSpace(text[:sep])
	value := strings.TrimSpace(text[sep+len("："):])
	if key == "" || value == "" {
		return "", false
	}
	return sanitizeModelReply(key+"："+value, fallbackText), true
}

type conversationState struct {
	ActiveTopic  string
	Focuses      []string
	ContextLines []string
	Summary      string
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

func buildPromptBody(stateSummary string, conversationHistory string, contextText string, question string) string {
	var builder strings.Builder
	if strings.TrimSpace(stateSummary) != "" {
		builder.WriteString("当前会话状态：\n")
		builder.WriteString(strings.TrimSpace(stateSummary))
		builder.WriteString("\n\n")
	}
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

func buildKnowledgePromptBody(stateSummary string, conversationHistory string, primaryDocument string, results []knowledgebase.SearchResult, question string) string {
	primaryDocument = truncateRunes(strings.TrimSpace(primaryDocument), maxPromptDocumentChars)
	if primaryDocument != "" {
		return buildKnowledgeDocumentPromptBody(stateSummary, conversationHistory, primaryDocument, question)
	}
	contextText := buildContext(results, maxPromptContextChars)
	if contextText == "" {
		return ""
	}
	return buildPromptBody(stateSummary, conversationHistory, contextText, question)
}

func buildKnowledgeDocumentPromptBody(stateSummary string, conversationHistory string, primaryDocument string, question string) string {
	var builder strings.Builder
	if strings.TrimSpace(stateSummary) != "" {
		builder.WriteString("当前会话状态：\n")
		builder.WriteString(strings.TrimSpace(stateSummary))
		builder.WriteString("\n\n")
	}
	if strings.TrimSpace(conversationHistory) != "" {
		builder.WriteString("最近对话：\n")
		builder.WriteString(strings.TrimSpace(conversationHistory))
		builder.WriteString("\n\n")
	}
	builder.WriteString("站点知识全文（knowledge.md，请直接在全文中查找答案）：\n")
	builder.WriteString(strings.TrimSpace(primaryDocument))
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

func buildSearchQuery(query string, state conversationState) string {
	query = normalizeQuery(query)
	if query == "" {
		return ""
	}
	if rewritten := rewriteBroadSearchQuery(query, state); rewritten != "" && rewritten != query {
		return rewritten
	}
	if !shouldAugmentSearchQuery(query, state) {
		return query
	}

	var builder strings.Builder
	builder.WriteString("当前问题：")
	builder.WriteString(query)
	if state.ActiveTopic != "" {
		builder.WriteString("\n当前主题：")
		builder.WriteString(state.ActiveTopic)
	}
	if len(state.Focuses) > 0 {
		builder.WriteString("\n关注点：")
		builder.WriteString(strings.Join(state.Focuses, "、"))
	}
	if len(state.ContextLines) > 0 {
		builder.WriteString("\n最近上下文：\n")
		for _, line := range state.ContextLines {
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	out := strings.TrimSpace(builder.String())
	runes := []rune(out)
	if len(runes) > maxSearchQueryChars {
		out = string(runes[:maxSearchQueryChars])
	}
	return out
}

func shouldAugmentSearchQuery(query string, state conversationState) bool {
	query = normalizeQuery(query)
	if query == "" {
		return false
	}
	if len(state.ContextLines) == 0 {
		return false
	}
	if state.ActiveTopic != "" && isBroadIntroIntent(compactQuery(query)) {
		return true
	}
	if state.ActiveTopic == "" {
		return false
	}
	if !contextualQuestionPattern.MatchString(query) {
		return false
	}
	return extractConversationTopic(query) == ""
}

func rewriteBroadSearchQuery(query string, state conversationState) string {
	query = normalizeQuery(query)
	if query == "" {
		return ""
	}
	if detailedQuestionPattern.MatchString(query) || contextualQuestionPattern.MatchString(query) || looksLikeDirectAnswerQuestion(query) {
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
	if topic := extractConversationTopic(query); topic != "" && hasIntroIntent(compact) && runeCount(query) <= 10 {
		return topic + " 介绍 说明 核心内容 核心权益 规则"
	}
	if topic := extractConversationTopic(query); topic != "" && isShortTopicLookupQuestion(query) {
		return topic + " 介绍 说明 核心内容 背景 历程"
	}
	if state.ActiveTopic != "" && isBroadIntroIntent(compact) {
		return state.ActiveTopic + " 产品介绍 规格 参数 卖点 适用场景"
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

func hasIntroIntent(compact string) bool {
	for _, keyword := range []string{"介绍", "讲讲", "说说", "聊聊", "说明"} {
		if strings.Contains(compact, compactQuery(keyword)) {
			return true
		}
	}
	return false
}

func isShortTopicLookupQuestion(question string) bool {
	question = normalizeQuery(question)
	if question == "" || runeCount(question) > 8 {
		return false
	}
	if isListSeekingQuestion(question) || looksLikeDirectAnswerQuestion(question) {
		return false
	}
	if contextualQuestionPattern.MatchString(question) {
		return false
	}
	topic := extractConversationTopic(question)
	if topic == "" {
		return false
	}
	return runeCount(topic) <= 8
}

func looksLikeDirectAnswerQuestion(question string) bool {
	question = normalizeQuery(question)
	if question == "" {
		return false
	}
	if isListSeekingQuestion(question) {
		return false
	}
	if strings.ContainsAny(question, "？?") {
		return true
	}
	compact := compactQuery(question)
	if len(inferQuestionFieldConcepts(compact)) > 0 {
		return true
	}
	for _, marker := range []string{"吗", "么", "呢", "几", "多少", "什么", "怎么", "如何", "哪", "可否", "能否", "是否", "可以", "支持"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	return false
}

func compactQuery(query string) string {
	return strings.ToLower(smallTalkStripPattern.ReplaceAllString(normalizeQuery(query), ""))
}

func buildConversationState(query string, messages []*chatclient.Message, currentMessageID uint64) conversationState {
	state := conversationState{}
	contextLines := collectRelevantContextLines(messages, currentMessageID, maxStateContextMessages, maxSearchContextChars)
	topic := extractConversationTopic(query)
	focuses := extractConversationFocuses(query)

	for idx := len(messages) - 1; idx >= 0; idx-- {
		item := messages[idx]
		if item == nil || item.ID == currentMessageID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(item.SenderType)) != "visitor" {
			continue
		}
		content := normalizeQuery(item.Content)
		if content == "" || isLowSignalContext(content) {
			continue
		}
		if _, ok := matchSmallTalkReply(content); ok {
			continue
		}
		if topic == "" {
			if candidate := extractConversationTopic(content); candidate != "" {
				topic = candidate
			}
		}
		focuses = appendUniqueStrings(focuses, extractConversationFocuses(content)...)
	}

	state.ActiveTopic = topic
	state.Focuses = focuses
	state.ContextLines = contextLines
	state.Summary = buildConversationStateSummary(state)
	return state
}

func collectRelevantContextLines(messages []*chatclient.Message, currentMessageID uint64, maxMessages int, maxChars int) []string {
	if maxMessages <= 0 || maxChars <= 0 || len(messages) == 0 {
		return nil
	}

	all := make([]string, 0, len(messages))
	for idx := len(messages) - 1; idx >= 0; idx-- {
		item := messages[idx]
		line := formatStateContextLine(item, currentMessageID)
		if line == "" {
			continue
		}
		all = append(all, line)
	}
	if len(all) == 0 {
		return nil
	}

	lines := make([]string, 0, minInt(len(all), maxMessages))
	totalChars := 0
	for idx := len(all) - 1; idx >= 0; idx-- {
		line := all[idx]
		if totalChars+len(line)+1 > maxChars && len(lines) > 0 {
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

func formatStateContextLine(item *chatclient.Message, currentMessageID uint64) string {
	if item == nil || item.ID == currentMessageID {
		return ""
	}
	role := mapSenderType(item.SenderType)
	content := normalizeQuery(item.Content)
	if role == "" || content == "" {
		return ""
	}
	if isLowSignalContext(content) {
		return ""
	}
	if _, ok := matchSmallTalkReply(content); ok {
		return ""
	}
	content = truncateRunes(content, 96)
	if content == "" {
		return ""
	}
	return role + "：" + content
}

func buildConversationStateSummary(state conversationState) string {
	lines := make([]string, 0, 2+len(state.ContextLines))
	if state.ActiveTopic != "" {
		lines = append(lines, "当前主题："+state.ActiveTopic)
	}
	if len(state.Focuses) > 0 {
		lines = append(lines, "关注点："+strings.Join(state.Focuses, "、"))
	}
	if len(state.ContextLines) > 0 {
		lines = append(lines, "最近有效上下文：")
		lines = append(lines, state.ContextLines...)
	}
	return truncateRunes(strings.Join(lines, "\n"), maxStateSummaryChars)
}

func extractConversationTopic(content string) string {
	topic := normalizeQuery(content)
	if topic == "" {
		return ""
	}
	for _, prefix := range []string{
		"我想了解", "想了解", "我想咨询", "想咨询", "我想问", "想问", "麻烦问下", "麻烦问问",
		"请问", "我想看看", "想看看", "看看", "介绍一下", "介绍下", "介绍", "关于", "聊聊", "说说", "讲讲",
		"我想买", "想买", "帮我看看", "帮我介绍下",
		"可以", "可否", "是否", "能否", "能不能", "能",
	} {
		topic = strings.TrimPrefix(topic, prefix)
	}
	for _, prefix := range []string{"你们的", "你们家", "你们", "这个", "那个", "这款", "那款", "这件", "那件", "这套", "那套", "它"} {
		topic = strings.TrimPrefix(topic, prefix)
	}
	for _, suffix := range []string{
		"多少钱", "价格是多少", "价格", "报价", "多大", "多长", "多宽", "多高", "大小", "尺寸", "规格",
		"材质", "材料", "是什么材质", "支持几天退换", "支持7天无理由吗", "支持退换吗", "能退吗", "退换货", "退货",
		"保修", "售后", "发货", "多久到", "多久", "安装", "颜色", "适合谁", "适合什么人", "怎么样", "好用吗",
		"在哪里", "在哪儿", "在哪", "哪里", "哪儿", "地址", "地点",
		"吗", "么", "呢", "呀", "啊", "区别", "哪个好",
	} {
		topic = strings.TrimSuffix(topic, suffix)
	}
	topic = strings.Trim(topic, "，。！？?;；:：、 ")
	if topic == "" || looksGenericTopic(topic) || looksLikeQuestion(topic) {
		return ""
	}
	if runeCount(topic) > 20 {
		topic = truncateRunes(topic, 20)
	}
	return topic
}

func looksGenericTopic(topic string) bool {
	switch topic {
	case "产品", "品牌", "产品线", "介绍", "客服", "售后", "发货", "价格", "尺寸", "材质",
		"支持", "退换", "退货", "保修", "安装", "颜色", "对比", "报价", "发票", "开票", "专票":
		return true
	default:
		return runeCount(topic) < 2
	}
}

func extractConversationFocuses(content string) []string {
	compact := compactQuery(content)
	if compact == "" {
		return nil
	}
	matches := make([]string, 0, 4)
	for _, item := range []struct {
		label    string
		keywords []string
	}{
		{label: "价格", keywords: []string{"多少钱", "价格", "售价", "报价", "费用", "贵吗"}},
		{label: "尺寸", keywords: []string{"尺寸", "规格", "多大", "多长", "多宽", "多高", "大小"}},
		{label: "材质", keywords: []string{"材质", "材料", "面料", "什么做的"}},
		{label: "售后", keywords: []string{"售后", "退换", "退货", "退款", "保修", "无理由"}},
		{label: "发货", keywords: []string{"发货", "多久到", "多久", "物流", "配送", "时效"}},
		{label: "地址", keywords: []string{"在哪里", "在哪儿", "在哪", "哪里", "哪儿", "地址", "地点"}},
		{label: "安装", keywords: []string{"安装", "组装"}},
		{label: "颜色", keywords: []string{"颜色", "配色"}},
		{label: "适用人群", keywords: []string{"适合", "适用", "人群"}},
		{label: "对比", keywords: []string{"区别", "差异", "哪个好", "哪个更好", "对比"}},
	} {
		for _, keyword := range item.keywords {
			if strings.Contains(compact, compactQuery(keyword)) {
				matches = appendUniqueStrings(matches, item.label)
				break
			}
		}
	}
	return matches
}

func appendUniqueStrings(dst []string, values ...string) []string {
	if len(values) == 0 {
		return dst
	}
	seen := make(map[string]struct{}, len(dst))
	for _, item := range dst {
		seen[item] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func runeCount(text string) int {
	return len([]rune(text))
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func looksLikeQuestion(text string) bool {
	text = normalizeQuery(text)
	if text == "" {
		return false
	}
	if strings.ContainsAny(text, "？?") {
		return true
	}
	for _, suffix := range []string{"吗", "么", "呢"} {
		if strings.HasSuffix(text, suffix) {
			return true
		}
	}
	return false
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
