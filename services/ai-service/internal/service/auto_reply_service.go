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
	replyLockTTL      = 10 * time.Minute
	replySenderType   = "ai"
	replySenderID     = "ai-service"
	replyTemperature  = 0.1
	replyMaxTokens    = 256
	maxPromptContext  = 4800
	replyModeAutoOnly = "unassigned_auto_reply"
)

var thinkTagPattern = regexp.MustCompile(`(?s)<think>.*?</think>`)

var (
	priceThresholdAbovePattern = regexp.MustCompile(`(?:高于|大于|超过|不低于|至少|起码)\s*([0-9]+(?:\.[0-9]+)?)\s*元?`)
	priceThresholdBelowPattern = regexp.MustCompile(`(?:低于|小于|不超过|不高于)\s*([0-9]+(?:\.[0-9]+)?)\s*元?`)
	priceWithinPattern         = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*元?(?:以内|以下)`)
	priceFromPattern           = regexp.MustCompile(`([0-9]+(?:\.[0-9]+)?)\s*元?(?:以上)`)
	englishWordPattern         = regexp.MustCompile(`\b[A-Za-z]{2,}\b`)
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
	if reply, ok := matchPresetReply(query); ok {
		return s.createReply(ctx, event, reply)
	}
	structuredFacts := buildStructuredFacts(query, s.kb.ProductPrices())

	results, err := s.searchKnowledge(ctx, query)
	if err != nil {
		return err
	}
	reply := s.unknownReply
	if len(results) > 0 || structuredFacts != "" {
		generatedReply, genErr := s.generateReply(ctx, query, results, structuredFacts)
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
	reply = sanitizeModelReply(reply, s.unknownReply)

	return s.createReply(ctx, event, reply)
}

func (s *AutoReplyService) searchKnowledge(ctx context.Context, query string) ([]knowledgebase.SearchResult, error) {
	candidates := buildSearchQueries(query)
	merged := make([]knowledgebase.SearchResult, 0, s.retrieveTopK)
	seen := make(map[int]struct{}, s.retrieveTopK)

	for _, candidate := range candidates {
		results, err := s.kb.Search(ctx, candidate, s.retrieveTopK, s.minSimilarity)
		if err != nil {
			return nil, err
		}
		for _, item := range results {
			if _, ok := seen[item.ID]; ok {
				continue
			}
			seen[item.ID] = struct{}{}
			merged = append(merged, item)
		}
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

func (s *AutoReplyService) generateReply(ctx context.Context, question string, results []knowledgebase.SearchResult, structuredFacts string) (string, error) {
	if len(results) == 0 && strings.TrimSpace(structuredFacts) == "" {
		return s.unknownReply, nil
	}

	contextText := buildContext(results, maxPromptContext)
	promptBody := buildPromptBody(contextText, structuredFacts, question)
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

func fallbackSection(section string) string {
	section = strings.TrimSpace(section)
	if section == "" {
		return "未命名章节"
	}
	return section
}

func sanitizeModelReply(reply string, fallback string) string {
	reply = thinkTagPattern.ReplaceAllString(reply, "")
	reply = normalizeReplyLanguage(reply)
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return fallback
	}
	return reply
}

func normalizeReplyLanguage(reply string) string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return ""
	}

	for _, item := range englishReplyReplacements {
		reply = item.pattern.ReplaceAllString(reply, item.value)
	}

	reply = englishWordPattern.ReplaceAllStringFunc(reply, func(token string) string {
		if _, ok := allowedEnglishTokens[token]; ok {
			return token
		}
		return ""
	})

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

func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func buildSearchQueries(query string) []string {
	normalized := normalizeText(query)
	if normalized == "" {
		return nil
	}

	queries := []string{normalized}
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
		queries = append(queries, rewritten)
	}

	if !strings.Contains(normalized, "青禾家居") {
		queries = append(queries, normalizeText("青禾家居 "+normalized))
	}
	return uniqueStrings(queries)
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
	builder.WriteString("- 如果用户询问最贵、最便宜、最高、最低、多少钱、价格高于/低于/以内/以上、主推产品数量等问题，必须先依据上表完成比较、筛选或统计，再回答。\n")
	builder.WriteString("- 上表已经提供了可直接比较的价格事实时，不要回答“资料未提及”或“无法确认”。")
	return strings.TrimSpace(builder.String())
}

func needsStructuredPriceFacts(query string, prices []knowledgebase.ProductPrice) bool {
	compact := compactText(query)
	if compact == "" {
		return false
	}
	if containsAny(compact,
		"最贵", "最便宜", "价格最高", "价格最低", "售价最高", "售价最低", "最高价", "最低价",
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

func buildPromptBody(contextText string, structuredFacts string, question string) string {
	var builder strings.Builder
	builder.WriteString("/no_think\n")
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

func formatPriceValue(value float64) string {
	if value == float64(int64(value)) {
		return fmt.Sprintf("%d元", int64(value))
	}
	return fmt.Sprintf("%.2f元", value)
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
