package knowledgebase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"go.uber.org/zap"

	"inlinechat/services/ai-service/internal/reranker"
)

const (
	maxRerankCandidates = 8
	maxRerankTextRunes  = 120
	maxRerankTotalRunes = 420
)

type embedder interface {
	CreateEmbeddings(ctx context.Context, inputs []string) ([][]float64, error)
}

type rerankClient interface {
	Rerank(ctx context.Context, query string, texts []string) ([]reranker.Result, error)
}

type vectorIndex interface {
	Ready(ctx context.Context) error
	ReplaceSite(ctx context.Context, siteID string, points []vectorPoint) error
	Search(ctx context.Context, siteID string, vector []float64, limit int) ([]qdrantSearchResult, error)
}

type Manager struct {
	rootDir            string
	embedder           embedder
	reranker           rerankClient
	qdrant             vectorIndex
	statusStore        *statusStore
	logger             *zap.Logger
	indexEmbedBatch    int
	retrievalCandidate int
	rerankTopK         int
	rerankMinScore     float64

	mu         sync.Mutex
	activeJobs map[string]string
}

func New(rootDir string, embedder embedder, rerankerClient rerankClient, qdrant vectorIndex, logger *zap.Logger, indexEmbedBatch int, retrievalCandidate int, rerankTopK int, rerankMinScore float64) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	if indexEmbedBatch <= 0 {
		indexEmbedBatch = 4
	}
	if retrievalCandidate <= 0 {
		retrievalCandidate = 12
	}
	if rerankTopK <= 0 {
		rerankTopK = 4
	}
	return &Manager{
		rootDir:            strings.TrimSpace(rootDir),
		embedder:           embedder,
		reranker:           rerankerClient,
		qdrant:             qdrant,
		statusStore:        newStatusStore(rootDir),
		logger:             logger,
		indexEmbedBatch:    indexEmbedBatch,
		retrievalCandidate: retrievalCandidate,
		rerankTopK:         rerankTopK,
		rerankMinScore:     rerankMinScore,
		activeJobs:         make(map[string]string),
	}
}

func NewQdrantClient(baseURL string, apiKey string, collection string, timeout time.Duration) *qdrantClient {
	return newQdrantClient(baseURL, apiKey, collection, timeout)
}

func (m *Manager) Ready() error {
	if strings.TrimSpace(m.rootDir) == "" {
		return fmt.Errorf("knowledge base root dir is required")
	}
	return nil
}

func (m *Manager) GetStatus(siteID string) (SiteStatus, error) {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return SiteStatus{}, fmt.Errorf("site_id is required")
	}
	status, err := m.statusStore.Load(siteID)
	if err != nil {
		return SiteStatus{}, err
	}
	m.mu.Lock()
	if jobID := strings.TrimSpace(m.activeJobs[siteID]); jobID != "" {
		status.ActiveJobID = jobID
		status.IndexStatus = StatusIndexing
	}
	m.mu.Unlock()
	return status, nil
}

func (m *Manager) LoadPrimaryDocument(siteID string) (string, error) {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return "", fmt.Errorf("site_id is required")
	}
	path := filepath.Join(knowledgeDirForSite(m.rootDir, siteID), "knowledge.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read primary document failed: %w", err)
	}
	return normalizeSourceText(path, string(raw)), nil
}

func (m *Manager) TriggerReindex(ctx context.Context, siteID string) (ReindexJob, error) {
	siteID = strings.TrimSpace(siteID)
	if siteID == "" {
		return ReindexJob{}, fmt.Errorf("site_id is required")
	}

	m.mu.Lock()
	if jobID := strings.TrimSpace(m.activeJobs[siteID]); jobID != "" {
		m.mu.Unlock()
		return ReindexJob{SiteID: siteID, JobID: jobID, Status: StatusIndexing}, nil
	}
	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	m.activeJobs[siteID] = jobID
	m.mu.Unlock()

	status, err := m.statusStore.Load(siteID)
	if err != nil {
		m.mu.Lock()
		delete(m.activeJobs, siteID)
		m.mu.Unlock()
		return ReindexJob{}, err
	}
	status.ActiveJobID = jobID
	status.IndexStatus = StatusIndexing
	status.LastIndexError = ""
	if err := m.statusStore.Save(status); err != nil {
		m.mu.Lock()
		delete(m.activeJobs, siteID)
		m.mu.Unlock()
		return ReindexJob{}, err
	}

	go m.runReindex(siteID, jobID)
	return ReindexJob{SiteID: siteID, JobID: jobID, Status: StatusIndexing}, nil
}

func (m *Manager) Search(ctx context.Context, siteID string, query string) ([]SearchResult, error) {
	siteID = strings.TrimSpace(siteID)
	query = strings.TrimSpace(query)
	if siteID == "" {
		return nil, fmt.Errorf("site_id is required")
	}
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	keywordCandidates := m.keywordSearchCandidates(siteID, query, maxInt(m.retrievalCandidate*2, 24))
	if directCandidates := selectAuthoritativeCandidates(query, keywordCandidates, m.rerankTopK); len(directCandidates) > 0 {
		return fallbackSearchResults(directCandidates, m.rerankTopK), nil
	}

	var vectorCandidates []qdrantSearchResult
	status, err := m.GetStatus(siteID)
	if err != nil {
		m.logger.Warn("load knowledge status failed, continue with live keyword retrieval",
			zap.String("site_id", siteID),
			zap.String("query", query),
			zap.Error(err),
		)
	} else if status.IndexStatus == StatusReady && status.IndexedChunks > 0 && m.embedder != nil && m.qdrant != nil {
		vectorCandidates, err = m.semanticSearch(ctx, siteID, query)
		if err != nil {
			m.logger.Warn("semantic search failed, fallback to keyword search",
				zap.String("site_id", siteID),
				zap.String("query", query),
				zap.Error(err),
			)
		}
	}

	candidates := mergeSearchCandidates(vectorCandidates, keywordCandidates, m.retrievalCandidate)
	candidates = prioritizeSearchCandidates(query, candidates)
	if len(candidates) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, nil
	}
	if m.reranker == nil {
		return fallbackSearchResults(candidates, m.rerankTopK), nil
	}

	texts, candidateIndexes := buildRerankInputs(candidates)
	rerankResults, err := m.reranker.Rerank(ctx, query, texts)
	if err != nil {
		m.logger.Warn("rerank failed, fallback to merged retrieval order",
			zap.String("site_id", siteID),
			zap.String("query", query),
			zap.Error(err),
		)
		return fallbackSearchResults(candidates, m.rerankTopK), nil
	}

	scored := make([]SearchResult, 0, len(rerankResults))
	for _, item := range rerankResults {
		if item.Index < 0 || item.Index >= len(candidateIndexes) {
			continue
		}
		if item.Score < m.rerankMinScore {
			continue
		}
		candidate := candidates[candidateIndexes[item.Index]]
		scored = append(scored, SearchResult{
			ID:         candidate.ID,
			Section:    candidate.Section,
			Text:       candidate.Text,
			SourcePath: candidate.SourcePath,
			Kind:       candidate.Kind,
			Keywords:   candidate.Keywords,
			Score:      item.Score,
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			return scored[i].ID < scored[j].ID
		}
		return scored[i].Score > scored[j].Score
	})
	if len(scored) == 0 {
		return fallbackSearchResults(candidates, m.rerankTopK), nil
	}
	if len(scored) > m.rerankTopK {
		scored = scored[:m.rerankTopK]
	}
	return scored, nil
}

func (m *Manager) semanticSearch(ctx context.Context, siteID string, query string) ([]qdrantSearchResult, error) {
	vectors, err := m.embedder.CreateEmbeddings(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("query embedding is empty")
	}
	return m.qdrant.Search(ctx, siteID, vectors[0], m.retrievalCandidate)
}

func (m *Manager) keywordSearchCandidates(siteID string, query string, limit int) []qdrantSearchResult {
	_, chunks, err := loadSiteChunks(m.rootDir, siteID)
	if err != nil {
		m.logger.Warn("load site chunks for keyword search failed",
			zap.String("site_id", siteID),
			zap.String("query", query),
			zap.Error(err),
		)
		return nil
	}
	return keywordSearchCandidates(chunks, query, limit)
}

func buildRerankInputs(candidates []qdrantSearchResult) ([]string, []int) {
	if len(candidates) == 0 {
		return nil, nil
	}

	texts := make([]string, 0, minInt(len(candidates), maxRerankCandidates))
	indexes := make([]int, 0, minInt(len(candidates), maxRerankCandidates))
	totalRunes := 0
	for idx, item := range candidates {
		if len(texts) >= maxRerankCandidates {
			break
		}
		text := trimLeadingRunes(buildRerankText(item), maxRerankTextRunes)
		if text == "" {
			continue
		}
		textRunes := runeCount(text)
		if totalRunes+textRunes > maxRerankTotalRunes && len(texts) > 0 {
			break
		}
		texts = append(texts, text)
		indexes = append(indexes, idx)
		totalRunes += textRunes
	}
	if len(texts) == 0 {
		text := trimLeadingRunes(buildRerankText(candidates[0]), maxRerankTextRunes)
		if text == "" {
			return nil, nil
		}
		return []string{text}, []int{0}
	}
	return texts, indexes
}

func buildRerankText(item qdrantSearchResult) string {
	return buildChunkIndexText(item.Kind, item.Section, item.Text, item.Keywords)
}

func buildChunkIndexText(kind string, section string, text string, keywords []string) string {
	parts := make([]string, 0, 4)
	section = strings.TrimSpace(section)
	text = strings.TrimSpace(text)
	if kindLabel := chunkKindLabel(kind); kindLabel != "" {
		parts = append(parts, "类型："+kindLabel)
	}
	if section != "" {
		parts = append(parts, "章节："+section)
	}
	if limitedKeywords := limitKeywords(keywords, 6); len(limitedKeywords) > 0 {
		parts = append(parts, "关键词："+strings.Join(limitedKeywords, "、"))
	}
	if text != "" {
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func trimLeadingRunes(text string, limit int) string {
	if limit <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}

func chunkKindLabel(kind string) string {
	switch strings.TrimSpace(kind) {
	case ChunkKindFact:
		return "事实字段"
	case ChunkKindFAQ:
		return "常见问题"
	default:
		return ""
	}
}

func limitKeywords(keywords []string, limit int) []string {
	keywords = normalizeKeywords(keywords)
	if limit > 0 && len(keywords) > limit {
		keywords = keywords[:limit]
	}
	return keywords
}

func fallbackSearchResults(candidates []qdrantSearchResult, limit int) []SearchResult {
	if len(candidates) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	out := make([]SearchResult, 0, limit)
	for _, item := range candidates[:limit] {
		out = append(out, SearchResult{
			ID:         item.ID,
			Section:    item.Section,
			Text:       item.Text,
			SourcePath: item.SourcePath,
			Kind:       item.Kind,
			Keywords:   item.Keywords,
			Score:      item.Score,
		})
	}
	return out
}

func mergeSearchCandidates(primary []qdrantSearchResult, secondary []qdrantSearchResult, limit int) []qdrantSearchResult {
	if len(primary) == 0 && len(secondary) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = len(primary) + len(secondary)
	}
	merged := make(map[string]qdrantSearchResult, len(primary)+len(secondary))
	for _, item := range primary {
		merged[item.ID] = item
	}
	for _, item := range secondary {
		existing, ok := merged[item.ID]
		if !ok {
			merged[item.ID] = item
			continue
		}
		merged[item.ID] = mergeSearchCandidate(existing, item)
	}
	out := make([]qdrantSearchResult, 0, len(merged))
	for _, item := range merged {
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score == out[j].Score {
			if out[i].Section == out[j].Section {
				return out[i].ID < out[j].ID
			}
			return out[i].Section < out[j].Section
		}
		return out[i].Score > out[j].Score
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func mergeSearchCandidate(left qdrantSearchResult, right qdrantSearchResult) qdrantSearchResult {
	if right.Score > left.Score {
		left.Score = right.Score
	}
	if strings.TrimSpace(left.Section) == "" {
		left.Section = right.Section
	}
	if strings.TrimSpace(left.Text) == "" {
		left.Text = right.Text
	}
	if strings.TrimSpace(left.SourcePath) == "" {
		left.SourcePath = right.SourcePath
	}
	if strings.TrimSpace(left.Kind) == "" {
		left.Kind = right.Kind
	}
	left.Keywords = mergeKeywords(left.Keywords, right.Keywords)
	return left
}

func selectAuthoritativeCandidates(query string, candidates []qdrantSearchResult, limit int) []qdrantSearchResult {
	if len(candidates) == 0 {
		return nil
	}
	compactQuery := compactKeywordQuery(query)
	listSeeking := isListSeekingKnowledgeQuestion(compactQuery)
	factSeeking := !listSeeking && hasStrongFactCandidateMatch(candidates, compactQuery)
	overviewSeeking := !listSeeking && !factSeeking && isOverviewSeekingKnowledgeQuestion(compactQuery)
	if !listSeeking && !factSeeking && !overviewSeeking {
		return nil
	}

	structured := make([]qdrantSearchResult, 0, len(candidates))
	for _, item := range candidates {
		boost := authoritativeCandidateBoost(item, compactQuery, listSeeking, factSeeking, overviewSeeking)
		if boost <= 0 {
			continue
		}
		item.Score += boost
		structured = append(structured, item)
	}
	if len(structured) == 0 {
		return nil
	}
	sortSearchCandidates(structured)
	if limit > 0 && len(structured) > limit {
		structured = structured[:limit]
	}
	return structured
}

func prioritizeSearchCandidates(query string, candidates []qdrantSearchResult) []qdrantSearchResult {
	if len(candidates) == 0 {
		return nil
	}
	compactQuery := compactKeywordQuery(query)
	listSeeking := isListSeekingKnowledgeQuestion(compactQuery)
	factSeeking := !listSeeking && hasStrongFactCandidateMatch(candidates, compactQuery)
	overviewSeeking := !listSeeking && !factSeeking && isOverviewSeekingKnowledgeQuestion(compactQuery)
	prioritized := make([]qdrantSearchResult, len(candidates))
	copy(prioritized, candidates)
	for idx := range prioritized {
		prioritized[idx].Score += authoritativeCandidateBoost(prioritized[idx], compactQuery, listSeeking, factSeeking, overviewSeeking)
	}
	sortSearchCandidates(prioritized)
	return prioritized
}

func authoritativeCandidateBoost(item qdrantSearchResult, compactQuery string, listSeeking bool, factSeeking bool, overviewSeeking bool) float64 {
	if compactQuery == "" {
		return 0
	}
	switch item.Kind {
	case ChunkKindFAQ:
		question, ok := extractFAQQuestionFromChunkText(item.Text)
		if !ok {
			return 10
		}
		compactQuestion := compactKeywordQuery(question)
		if compactQuestion == "" {
			return 10
		}
		if strings.Contains(compactQuestion, compactQuery) || strings.Contains(compactQuery, compactQuestion) {
			return 18
		}
		if scoreKeywordOverlap(compactQuestion, compactQuery) >= 2 {
			return 14
		}
		if listSeeking || factSeeking {
			return 10
		}
	case ChunkKindFact:
		if listSeeking && hasProductNameFact(item.Text) {
			return 12
		}
		if score := scoreFactSearchResultMatch(item, compactQuery); score > 0 {
			boost := 4.0 + score
			if factSeeking {
				boost += 1.5
			}
			return boost
		}
	case ChunkKindNarrative:
		if overviewSeeking {
			if scoreNarrativeOverviewMatch(item, compactQuery) > 0 {
				return 10
			}
			return 6
		}
	}
	return 0
}

func sortSearchCandidates(items []qdrantSearchResult) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Score == items[j].Score {
			if items[i].Section == items[j].Section {
				return items[i].ID < items[j].ID
			}
			return items[i].Section < items[j].Section
		}
		return items[i].Score > items[j].Score
	})
}

func extractFAQQuestionFromChunkText(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", false
	}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "问题：") {
			continue
		}
		question := strings.TrimSpace(strings.TrimPrefix(line, "问题："))
		if question != "" {
			return question, true
		}
	}
	return "", false
}

func hasProductNameFact(text string) bool {
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "产品名称：") {
			return true
		}
	}
	return false
}

type factField struct {
	Key   string
	Value string
}

func hasStrongFactCandidateMatch(candidates []qdrantSearchResult, compactQuery string) bool {
	best := 0.0
	for _, item := range candidates {
		if item.Kind != ChunkKindFact {
			continue
		}
		score := scoreFactSearchResultMatch(item, compactQuery)
		if score > best {
			best = score
		}
	}
	return best >= 2.6
}

func scoreFactSearchResultMatch(item qdrantSearchResult, compactQuery string) float64 {
	return scoreFactChunkStructureMatch(item.Section, item.Text, item.Keywords, compactQuery)
}

func scoreFactChunkStructureMatch(section string, text string, keywords []string, compactQuery string) float64 {
	if compactQuery == "" {
		return 0
	}
	queryTokens := extractKeywordFallbackTokens(compactQuery)
	if len(queryTokens) == 0 {
		return 0
	}
	best := 0.0
	for _, field := range extractFactFields(text) {
		score := scoreFactFieldMatch(section, keywords, compactQuery, queryTokens, field)
		if score > best {
			best = score
		}
	}
	if best > 0 {
		return best
	}
	metadata := compactKeywordQuery(section + "\n" + strings.Join(keywords, "\n") + "\n" + text)
	return scoreCompactKeywordMatches(metadata, queryTokens, 0.08, 0.02)
}

func extractFactFields(text string) []factField {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	out := make([]factField, 0, len(lines))
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
		out = append(out, factField{Key: key, Value: value})
	}
	return out
}

func scoreFactFieldMatch(section string, keywords []string, compactQuery string, queryTokens []string, field factField) float64 {
	compactKey := compactKeywordQuery(field.Key)
	if compactKey == "" {
		return 0
	}
	fieldScore := scoreCompactKeywordMatches(compactKey, queryTokens, 0.26, 0.05)
	if strings.Contains(compactQuery, compactKey) || strings.Contains(compactKey, compactQuery) {
		fieldScore += 2.2
	}
	fieldScore += scoreKnowledgeFieldConceptOverlap(inferKnowledgeQuestionFieldConcepts(compactQuery), inferKnowledgeFactFieldConcepts(field.Key, field.Value))
	metadata := compactKeywordQuery(section + "\n" + strings.Join(keywords, "\n") + "\n" + field.Value)
	metadataScore := scoreCompactKeywordMatches(metadata, queryTokens, 0.08, 0.02)
	return fieldScore + metadataScore
}

func scoreCompactKeywordMatches(compactText string, tokens []string, base float64, scale float64) float64 {
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

func scoreKeywordOverlap(left string, right string) int {
	if left == "" || right == "" {
		return 0
	}
	score := 0
	for _, token := range extractKeywordFallbackTokens(left) {
		compactToken := compactKeywordQuery(token)
		if compactToken == "" {
			continue
		}
		if strings.Contains(right, compactToken) || strings.Contains(compactToken, right) {
			score++
		}
	}
	return score
}

func isListSeekingKnowledgeQuestion(compactQuery string) bool {
	if compactQuery == "" {
		return false
	}
	for _, keyword := range []string{
		"有哪些", "哪些", "有什么", "有啥", "哪几个", "哪几款", "主推产品", "明星单品", "推荐产品",
	} {
		if strings.Contains(compactQuery, compactKeywordQuery(keyword)) {
			return true
		}
	}
	return false
}

func isOverviewSeekingKnowledgeQuestion(compactQuery string) bool {
	if compactQuery == "" || isListSeekingKnowledgeQuestion(compactQuery) || looksLikeSingleAnswerKnowledgeQuestion(compactQuery) {
		return false
	}
	hasIntro := false
	for _, keyword := range []string{"介绍", "讲讲", "说说", "聊聊", "说明"} {
		if strings.Contains(compactQuery, compactKeywordQuery(keyword)) {
			hasIntro = true
			break
		}
	}
	if hasIntro {
		return true
	}
	return isShortTopicKnowledgeLookup(compactQuery)
}

func scoreNarrativeOverviewMatch(item qdrantSearchResult, compactQuery string) float64 {
	if compactQuery == "" {
		return 0
	}
	compactSection := compactKeywordQuery(item.Section)
	compactText := compactKeywordQuery(item.Text)
	score := 0.0
	for _, token := range extractKeywordFallbackTokens(compactQuery) {
		compactToken := compactKeywordQuery(token)
		if compactToken == "" {
			continue
		}
		if strings.Contains(compactSection, compactToken) {
			score += 1.2
		}
		for _, keyword := range item.Keywords {
			if strings.Contains(compactKeywordQuery(keyword), compactToken) {
				score += 1.4
				break
			}
		}
		if strings.Contains(compactText, compactToken) {
			score += 0.4
		}
	}
	if strings.EqualFold(strings.TrimSpace(item.Section), strings.TrimSpace(item.SourcePath)) {
		score -= 1.0
	}
	return score
}

func isShortTopicKnowledgeLookup(compactQuery string) bool {
	if compactQuery == "" || runeCount(compactQuery) > 8 {
		return false
	}
	if looksLikeSingleAnswerKnowledgeQuestion(compactQuery) {
		return false
	}
	for _, keyword := range []string{"它", "这个", "那个", "这款", "那款", "这套", "那套"} {
		if strings.Contains(compactQuery, compactKeywordQuery(keyword)) {
			return false
		}
	}
	return true
}

func looksLikeSingleAnswerKnowledgeQuestion(compactQuery string) bool {
	if compactQuery == "" {
		return false
	}
	if isListSeekingKnowledgeQuestion(compactQuery) {
		return false
	}
	if len(inferKnowledgeQuestionFieldConcepts(compactQuery)) > 0 {
		return true
	}
	for _, marker := range []string{"吗", "么", "呢", "几", "多少", "什么", "怎么", "如何", "哪", "可否", "能否", "是否", "可以", "支持"} {
		if strings.Contains(compactQuery, marker) {
			return true
		}
	}
	return false
}

func keywordSearchCandidates(chunks []Chunk, query string, limit int) []qdrantSearchResult {
	if len(chunks) == 0 {
		return nil
	}
	keywords := extractKeywordFallbackTokens(query)
	if len(keywords) == 0 {
		return nil
	}
	fullQuery := normalizeText(query)
	compactQuery := compactKeywordQuery(query)
	scored := make([]qdrantSearchResult, 0, len(chunks))
	for _, item := range chunks {
		score := scoreChunkByKeywords(item, fullQuery, compactQuery, keywords)
		if score <= 0 {
			continue
		}
		scored = append(scored, qdrantSearchResult{
			ID:         item.ID,
			Section:    item.Section,
			Text:       item.Text,
			SourcePath: item.SourcePath,
			Kind:       item.Kind,
			Keywords:   item.Keywords,
			Score:      score,
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Score == scored[j].Score {
			if scored[i].Section == scored[j].Section {
				return scored[i].ID < scored[j].ID
			}
			return scored[i].Section < scored[j].Section
		}
		return scored[i].Score > scored[j].Score
	})
	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}
	return scored
}

func extractKeywordFallbackTokens(query string) []string {
	normalized := normalizeText(query)
	if normalized == "" {
		return nil
	}
	seen := make(map[string]struct{}, 32)
	out := make([]string, 0, 32)
	appendToken := func(token string) {
		token = strings.TrimSpace(token)
		if token == "" || runeCount(token) < 2 {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}
	if !strings.ContainsAny(normalized, " \n\t") {
		appendToken(normalized)
	}
	for _, token := range strings.Fields(normalized) {
		appendToken(token)
	}
	compact := compactKeywordQuery(normalized)
	appendToken(compact)
	for _, token := range expandQuestionIntentTokens(compact) {
		appendToken(token)
	}
	for _, token := range expandKnowledgeAliasTokens(compact) {
		appendToken(token)
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

func expandKnowledgeAliasTokens(compact string) []string {
	if compact == "" {
		return nil
	}
	out := make([]string, 0, 12)
	for _, concept := range inferKnowledgeQuestionFieldConcepts(compact) {
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

func inferKnowledgeQuestionFieldConcepts(compact string) []string {
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
			if strings.Contains(compact, compactKeywordQuery(keyword)) {
				appendConcept(rule.concept)
				break
			}
		}
	}
	return out
}

func inferKnowledgeFactFieldConcepts(key string, value string) []string {
	compactKey := compactKeywordQuery(key)
	if compactKey == "" && strings.TrimSpace(value) == "" {
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
	if containsAnyKnowledgeCompact(compactKey, "价", "售价", "零售价", "报价", "金额", "费用") || strings.ContainsAny(value, "¥￥") {
		appendConcept("price")
	}
	if containsAnyKnowledgeCompact(compactKey, "发票", "开票", "专票", "普票", "税票") {
		appendConcept("invoice")
	}
	if containsAnyKnowledgeCompact(compactKey, "尺寸", "规格", "长", "宽", "高", "大小", "口径", "容量") {
		appendConcept("size")
	}
	if containsAnyKnowledgeCompact(compactKey, "材质", "材料", "面料") {
		appendConcept("material")
	}
	if containsAnyKnowledgeCompact(compactKey, "发货", "物流", "配送", "时效", "到货") {
		appendConcept("shipping")
	}
	if containsAnyKnowledgeCompact(compactKey, "售后", "退换", "退货", "退款", "保修", "无理由") {
		appendConcept("aftersale")
	}
	if containsAnyKnowledgeCompact(compactKey, "地址", "位置", "所在地", "地点") {
		appendConcept("location")
	}
	if containsAnyKnowledgeCompact(compactKey, "场景", "用途", "适用", "使用") {
		appendConcept("scenario")
	}
	return out
}

func scoreKnowledgeFieldConceptOverlap(questionConcepts []string, fieldConcepts []string) float64 {
	if len(questionConcepts) == 0 || len(fieldConcepts) == 0 {
		return 0
	}
	score := 0.0
	for _, left := range questionConcepts {
		for _, right := range fieldConcepts {
			if left == right {
				score += 2.0
				break
			}
		}
	}
	return score
}

func containsAnyKnowledgeCompact(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, compactKeywordQuery(needle)) {
			return true
		}
	}
	return false
}

func expandQuestionIntentTokens(compact string) []string {
	if compact == "" {
		return nil
	}
	subject := extractQuestionSubject(compact)
	if subject == "" {
		return nil
	}
	expansions := make([]string, 0, 6)
	if hasLocationIntent(compact) {
		expansions = append(expansions,
			subject+"地址",
			subject+"所在地",
			subject+"位置",
		)
	}
	return expansions
}

func hasLocationIntent(compact string) bool {
	for _, token := range []string{"在哪里", "在哪儿", "在哪", "哪里", "哪儿", "地址", "所在地", "位置"} {
		if strings.Contains(compact, token) {
			return true
		}
	}
	return false
}

func extractQuestionSubject(compact string) string {
	subject := compact
	for _, prefix := range []string{"请问", "请", "你们家", "你们", "你家", "我想问", "想问", "我想了解", "想了解"} {
		subject = strings.TrimPrefix(subject, prefix)
	}
	for _, suffix := range []string{"在哪里", "在哪儿", "在哪", "哪里", "哪儿", "地址", "所在地", "位置", "是什么", "是啥", "吗", "呀", "啊", "呢"} {
		subject = strings.TrimSuffix(subject, suffix)
	}
	for _, filler := range []string{"的", "是", "一下"} {
		subject = strings.ReplaceAll(subject, filler, "")
	}
	subject = strings.TrimSpace(subject)
	if runeCount(subject) < 2 {
		return ""
	}
	if runeCount(subject) > 8 {
		return string([]rune(subject)[:8])
	}
	return subject
}

func compactKeywordQuery(query string) string {
	var builder strings.Builder
	builder.Grow(len(query))
	for _, r := range query {
		switch {
		case unicode.IsSpace(r), unicode.IsPunct(r), unicode.IsSymbol(r):
			continue
		case unicode.IsUpper(r):
			builder.WriteRune(unicode.ToLower(r))
		default:
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func scoreChunkByKeywords(item Chunk, fullQuery string, compactQuery string, keywords []string) float64 {
	section := normalizeText(item.Section)
	text := normalizeText(item.Text)
	compactSection := compactKeywordQuery(section)
	compactText := compactKeywordQuery(text)
	score := 0.0
	if fullQuery != "" {
		if strings.Contains(section, fullQuery) {
			score += 1.6
		}
		if strings.Contains(text, fullQuery) {
			score += 1.2
		}
	}
	if compactQuery != "" && compactQuery != fullQuery {
		if strings.Contains(compactSection, compactQuery) {
			score += 1.4
		}
		if strings.Contains(compactText, compactQuery) {
			score += 1.0
		}
	}
	for _, keyword := range keywords {
		if keyword == fullQuery || keyword == compactQuery {
			continue
		}
		keywordWeight := float64(minInt(runeCount(keyword), 6)) * 0.08
		if strings.Contains(compactSection, keyword) || strings.Contains(section, keyword) {
			score += 0.28 + keywordWeight
		}
		if strings.Contains(compactText, keyword) || strings.Contains(text, keyword) {
			score += 0.12 + keywordWeight*0.7
		}
	}
	score += scoreStructuredFieldIntent(item, compactQuery)
	for _, keyword := range item.Keywords {
		normalizedKeyword := normalizeInlineText(keyword)
		if normalizedKeyword == "" {
			continue
		}
		if fullQuery != "" && strings.Contains(fullQuery, normalizedKeyword) {
			score += 0.42
		}
		compactKeyword := compactKeywordQuery(normalizedKeyword)
		if compactKeyword == "" {
			continue
		}
		if compactQuery != "" && (strings.Contains(compactQuery, compactKeyword) || strings.Contains(compactKeyword, compactQuery)) {
			score += 0.34 + float64(minInt(runeCount(normalizedKeyword), 6))*0.05
		}
	}
	switch item.Kind {
	case ChunkKindFact:
		if score > 0 {
			score *= 1.2
		}
	case ChunkKindFAQ:
		if score > 0 {
			score *= 1.12
		}
	}
	return score
}

func scoreStructuredFieldIntent(item Chunk, compactQuery string) float64 {
	if compactQuery == "" || item.Kind != ChunkKindFact {
		return 0
	}
	return scoreFactChunkStructureMatch(item.Section, item.Text, item.Keywords, compactQuery)
}

func (m *Manager) runReindex(siteID string, jobID string) {
	defer func() {
		m.mu.Lock()
		delete(m.activeJobs, siteID)
		m.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	status, err := m.statusStore.Load(siteID)
	if err != nil {
		m.logger.Error("load site index status failed", zap.String("site_id", siteID), zap.Error(err))
		return
	}
	status.IndexStatus = StatusIndexing
	status.ActiveJobID = jobID
	status.KnowledgeDir = knowledgeDirForSite(m.rootDir, siteID)
	_ = m.statusStore.Save(status)

	knowledgeDir, chunks, err := loadSiteChunks(m.rootDir, siteID)
	if err != nil {
		status.IndexStatus = StatusError
		status.LastIndexError = err.Error()
		status.ActiveJobID = ""
		status.KnowledgeDir = knowledgeDir
		_ = m.statusStore.Save(status)
		m.logger.Warn("load site knowledge failed", zap.String("site_id", siteID), zap.Error(err))
		return
	}

	points := make([]vectorPoint, 0, len(chunks))
	for start := 0; start < len(chunks); start += m.indexEmbedBatch {
		end := start + m.indexEmbedBatch
		if end > len(chunks) {
			end = len(chunks)
		}
		inputs := make([]string, 0, end-start)
		for _, item := range chunks[start:end] {
			inputs = append(inputs, buildChunkIndexText(item.Kind, item.Section, item.Text, item.Keywords))
		}
		vectors, embedErr := m.embedder.CreateEmbeddings(ctx, inputs)
		if embedErr != nil {
			status.IndexStatus = StatusError
			status.LastIndexError = embedErr.Error()
			status.ActiveJobID = ""
			status.KnowledgeDir = knowledgeDir
			_ = m.statusStore.Save(status)
			m.logger.Warn("embed site chunks failed", zap.String("site_id", siteID), zap.Error(embedErr))
			return
		}
		for idx, item := range chunks[start:end] {
			if idx >= len(vectors) {
				continue
			}
			points = append(points, vectorPoint{
				ID:         item.ID,
				Vector:     vectors[idx],
				Section:    item.Section,
				Text:       item.Text,
				SourcePath: item.SourcePath,
				Kind:       item.Kind,
				Keywords:   item.Keywords,
				SiteID:     siteID,
			})
		}
	}

	if err := m.qdrant.ReplaceSite(ctx, siteID, points); err != nil {
		status.IndexStatus = StatusError
		status.LastIndexError = err.Error()
		status.ActiveJobID = ""
		status.KnowledgeDir = knowledgeDir
		_ = m.statusStore.Save(status)
		m.logger.Warn("replace qdrant site index failed", zap.String("site_id", siteID), zap.Error(err))
		return
	}

	status.IndexStatus = StatusReady
	status.IndexedChunks = len(points)
	status.LastIndexedAt = time.Now()
	status.LastIndexError = ""
	status.ActiveJobID = ""
	status.KnowledgeDir = knowledgeDir
	if err := m.statusStore.Save(status); err != nil {
		m.logger.Warn("save ready status failed", zap.String("site_id", siteID), zap.Error(err))
		return
	}
	m.logger.Info("site knowledge indexed",
		zap.String("site_id", siteID),
		zap.Int("chunk_count", len(points)),
		zap.String("knowledge_dir", knowledgeDir),
	)
}
