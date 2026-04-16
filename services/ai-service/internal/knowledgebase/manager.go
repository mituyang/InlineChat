package knowledgebase

import (
	"context"
	"fmt"
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

	status, err := m.GetStatus(siteID)
	if err != nil {
		return nil, err
	}
	if status.IndexStatus != StatusReady || status.IndexedChunks == 0 {
		return nil, nil
	}

	keywordCandidates := m.keywordSearchCandidates(siteID, query, m.retrievalCandidate)
	vectorCandidates, err := m.semanticSearch(ctx, siteID, query)
	if err != nil {
		m.logger.Warn("semantic search failed, fallback to keyword search",
			zap.String("site_id", siteID),
			zap.String("query", query),
			zap.Error(err),
		)
	}

	candidates := mergeSearchCandidates(vectorCandidates, keywordCandidates, m.retrievalCandidate)
	if len(candidates) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, nil
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
	section := strings.TrimSpace(item.Section)
	text := strings.TrimSpace(item.Text)
	switch {
	case section == "":
		return text
	case text == "":
		return section
	default:
		return section + "\n" + text
	}
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
		if !ok || item.Score > existing.Score {
			merged[item.ID] = item
		}
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
	return score
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
			inputs = append(inputs, item.Text)
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
