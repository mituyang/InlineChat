package knowledgebase

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"go.uber.org/zap"
)

const (
	defaultChunkChars   = 1000
	defaultChunkOverlap = 120
	defaultEmbedBatch   = 16
)

var priceNumberPattern = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)

var (
	headingIndexPattern = regexp.MustCompile(`^(?:第[0-9一二三四五六七八九十百千]+[章节篇部分]\s*|[0-9]+(?:\.[0-9]+)*\s*)`)
	asciiLetterPattern  = regexp.MustCompile(`[A-Za-z]`)
	hanSequencePattern  = regexp.MustCompile(`[\p{Han}]{2,}`)
	yearHeadingPattern  = regexp.MustCompile(`^(20[0-9]{2})\s*年(?:\s*[:：]\s*(.+))?$`)
)

type embedder interface {
	CreateEmbeddings(ctx context.Context, inputs []string) ([][]float64, error)
}

type Chunk struct {
	ID        int
	Section   string
	Text      string
	Embedding []float64
	Norm      float64
}

type SearchResult struct {
	ID      int
	Section string
	Text    string
	Score   float64
}

type ProductPrice struct {
	Name       string
	PriceText  string
	PriceValue float64
	Section    string
}

type YearMilestone struct {
	Year    int
	Title   string
	Summary string
	Section string
}

type Status struct {
	ChunkCount int
	LoadedAt   time.Time
	LastError  string
}

type Manager struct {
	path     string
	embedder embedder
	logger   *zap.Logger

	mu             sync.RWMutex
	chunks         []Chunk
	productPrices  []ProductPrice
	retrievalTerms []string
	yearMilestones []YearMilestone
	loadedAt       time.Time
	lastError      error
}

func New(path string, embedder embedder, logger *zap.Logger) *Manager {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Manager{
		path:     strings.TrimSpace(path),
		embedder: embedder,
		logger:   logger,
	}
}

func (m *Manager) Reload(ctx context.Context) (Status, error) {
	raw, err := os.ReadFile(m.path)
	if err != nil {
		m.setLastError(err)
		return Status{}, fmt.Errorf("read knowledge base failed: %w", err)
	}

	rawChunks := splitMarkdownIntoChunks(string(raw), defaultChunkChars, defaultChunkOverlap)
	if len(rawChunks) == 0 {
		err := fmt.Errorf("knowledge base contains no chunks")
		m.setLastError(err)
		return Status{}, err
	}

	inputs := make([]string, 0, len(rawChunks))
	for _, chunk := range rawChunks {
		inputs = append(inputs, chunk.embeddingText())
	}

	embeddings, err := m.embedAll(ctx, inputs)
	if err != nil {
		m.setLastError(err)
		return Status{}, fmt.Errorf("embed knowledge base failed: %w", err)
	}

	chunks := make([]Chunk, 0, len(rawChunks))
	for i, rawChunk := range rawChunks {
		embedding := embeddings[i]
		chunks = append(chunks, Chunk{
			ID:        rawChunk.ID,
			Section:   rawChunk.Section,
			Text:      rawChunk.Text,
			Embedding: embedding,
			Norm:      vectorNorm(embedding),
		})
	}

	productPrices := extractProductPrices(string(raw))
	retrievalTerms := extractRetrievalTerms(rawChunks, productPrices)
	yearMilestones := extractYearMilestones(rawChunks)

	status := Status{
		ChunkCount: len(chunks),
		LoadedAt:   time.Now(),
	}

	m.mu.Lock()
	m.chunks = chunks
	m.productPrices = productPrices
	m.retrievalTerms = retrievalTerms
	m.yearMilestones = yearMilestones
	m.loadedAt = status.LoadedAt
	m.lastError = nil
	m.mu.Unlock()

	m.logger.Info("knowledge base loaded",
		zap.String("path", m.path),
		zap.Int("chunk_count", status.ChunkCount),
	)
	return status, nil
}

func (m *Manager) Search(ctx context.Context, query string, topK int, minSimilarity float64) ([]SearchResult, error) {
	query = normalizeText(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if topK <= 0 {
		topK = 5
	}

	m.mu.RLock()
	chunks := append([]Chunk(nil), m.chunks...)
	lastErr := m.lastError
	m.mu.RUnlock()
	if len(chunks) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("knowledge base is not loaded")
	}

	queryVectors, err := m.embedder.CreateEmbeddings(ctx, []string{query})
	if err != nil {
		return nil, err
	}
	queryVector := queryVectors[0]
	queryNorm := vectorNorm(queryVector)
	if queryNorm == 0 {
		return nil, fmt.Errorf("query embedding is empty")
	}

	results := make([]SearchResult, 0, len(chunks))
	for _, chunk := range chunks {
		score := cosineSimilarity(queryVector, queryNorm, chunk.Embedding, chunk.Norm)
		if score < minSimilarity {
			continue
		}
		results = append(results, SearchResult{
			ID:      chunk.ID,
			Section: chunk.Section,
			Text:    chunk.Text,
			Score:   score,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].ID < results[j].ID
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > topK {
		results = results[:topK]
	}
	return results, nil
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()

	status := Status{
		ChunkCount: len(m.chunks),
		LoadedAt:   m.loadedAt,
	}
	if m.lastError != nil {
		status.LastError = m.lastError.Error()
	}
	return status
}

func (m *Manager) ProductPrices() []ProductPrice {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]ProductPrice, len(m.productPrices))
	copy(out, m.productPrices)
	return out
}

func (m *Manager) Terms() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]string, len(m.retrievalTerms))
	copy(out, m.retrievalTerms)
	return out
}

func (m *Manager) YearMilestones() []YearMilestone {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]YearMilestone, len(m.yearMilestones))
	copy(out, m.yearMilestones)
	return out
}

func (m *Manager) Ready() error {
	status := m.Status()
	if status.LastError != "" {
		return errors.New(status.LastError)
	}
	if status.ChunkCount == 0 {
		return fmt.Errorf("knowledge base is empty")
	}
	return nil
}

func (m *Manager) setLastError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastError = err
}

func (m *Manager) embedAll(ctx context.Context, inputs []string) ([][]float64, error) {
	out := make([][]float64, 0, len(inputs))
	for start := 0; start < len(inputs); start += defaultEmbedBatch {
		end := start + defaultEmbedBatch
		if end > len(inputs) {
			end = len(inputs)
		}
		vectors, err := m.embedder.CreateEmbeddings(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vectors...)
	}
	return out, nil
}

type rawChunk struct {
	ID      int
	Section string
	Text    string
}

func (c rawChunk) embeddingText() string {
	if c.Section == "" {
		return c.Text
	}
	return c.Section + "\n" + c.Text
}

func splitMarkdownIntoChunks(raw string, maxChars int, overlap int) []rawChunk {
	if maxChars <= 0 {
		maxChars = defaultChunkChars
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxChars {
		overlap = maxChars / 4
	}

	sections := make([]string, 0, 6)
	paragraphs := make([]rawChunk, 0, 64)
	var currentParagraph []string
	chunkID := 1

	flushParagraph := func() {
		text := normalizeText(strings.Join(currentParagraph, " "))
		currentParagraph = nil
		if text == "" {
			return
		}
		section := normalizeText(strings.Join(sections, " > "))
		parts := splitLongText(text, maxChars, overlap)
		for _, part := range parts {
			paragraphs = append(paragraphs, rawChunk{
				ID:      chunkID,
				Section: section,
				Text:    part,
			})
			chunkID++
		}
	}

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			flushParagraph()
			continue
		}
		if strings.HasPrefix(line, "#") {
			flushParagraph()
			level, title := parseHeading(line)
			if title == "" {
				continue
			}
			if level <= 0 {
				level = 1
			}
			if level > len(sections)+1 {
				level = len(sections) + 1
			}
			sections = append([]string(nil), sections[:level-1]...)
			sections = append(sections, title)
			continue
		}
		currentParagraph = append(currentParagraph, line)
	}
	flushParagraph()

	if len(paragraphs) == 0 {
		text := normalizeText(raw)
		parts := splitLongText(text, maxChars, overlap)
		for _, part := range parts {
			paragraphs = append(paragraphs, rawChunk{
				ID:      chunkID,
				Section: "",
				Text:    part,
			})
			chunkID++
		}
	}
	return paragraphs
}

func parseHeading(line string) (int, string) {
	level := 0
	for level < len(line) && line[level] == '#' {
		level++
	}
	title := normalizeText(strings.TrimSpace(line[level:]))
	return level, title
}

func splitLongText(text string, maxChars int, overlap int) []string {
	text = normalizeText(text)
	if text == "" {
		return nil
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return []string{text}
	}

	parts := make([]string, 0, (len(runes)/maxChars)+1)
	for start := 0; start < len(runes); {
		end := start + maxChars
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, strings.TrimSpace(string(runes[start:end])))
		if end == len(runes) {
			break
		}
		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return parts
}

func normalizeText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func compactTermText(text string) string {
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range strings.TrimSpace(text) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func extractRetrievalTerms(chunks []rawChunk, prices []ProductPrice) []string {
	weights := make(map[string]int, 128)
	for _, chunk := range chunks {
		if chunk.Section == "" {
			continue
		}
		for _, section := range strings.Split(chunk.Section, " > ") {
			addHeadingRetrievalTerms(weights, section, 4)
		}
	}
	for _, item := range prices {
		addRetrievalTerm(weights, item.Name, 6)
		for _, gram := range extractHanNGrams(item.Name, 2, 4) {
			addRetrievalTerm(weights, gram, 2)
		}
	}
	addChunkTextRetrievalTerms(weights, chunks)

	type weightedTerm struct {
		Text   string
		Weight int
	}
	items := make([]weightedTerm, 0, len(weights))
	for term, weight := range weights {
		items = append(items, weightedTerm{Text: term, Weight: weight})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Weight == items[j].Weight {
			ri := len([]rune(items[i].Text))
			rj := len([]rune(items[j].Text))
			if ri == rj {
				return items[i].Text < items[j].Text
			}
			return ri > rj
		}
		return items[i].Weight > items[j].Weight
	})

	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Text)
		if len(out) >= 256 {
			break
		}
	}
	return out
}

func addChunkTextRetrievalTerms(weights map[string]int, chunks []rawChunk) {
	docFreq := make(map[string]int, 256)
	totalFreq := make(map[string]int, 256)

	for _, chunk := range chunks {
		seen := make(map[string]struct{}, 32)
		for _, seq := range hanSequencePattern.FindAllString(chunk.Text, -1) {
			for _, gram := range extractHanNGrams(seq, 2, 4) {
				if !isUsefulTextRetrievalTerm(gram) {
					continue
				}
				totalFreq[gram]++
				if _, ok := seen[gram]; ok {
					continue
				}
				seen[gram] = struct{}{}
				docFreq[gram]++
			}
		}
	}

	for term, docs := range docFreq {
		runes := []rune(term)
		total := totalFreq[term]
		switch {
		case len(runes) == 2 && total < 3:
			continue
		case len(runes) >= 3 && docs < 2:
			continue
		}
		weight := docs + (total / 2)
		addRetrievalTerm(weights, term, weight)
	}
}

func isUsefulTextRetrievalTerm(term string) bool {
	runes := []rune(compactTermText(term))
	if len(runes) < 2 || len(runes) > 4 {
		return false
	}
	meaningful := 0
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
		switch r {
		case '的', '了', '是', '和', '与', '及', '在', '把', '让', '将', '向', '按', '可', '更', '较', '很', '也', '都', '且', '并':
			continue
		default:
			meaningful++
		}
	}
	return meaningful >= 2 || (len(runes) == 2 && meaningful >= 1)
}

func addHeadingRetrievalTerms(weights map[string]int, section string, weight int) {
	section = normalizeHeadingTerm(section)
	if section == "" {
		return
	}
	addRetrievalTerm(weights, section, weight)
	for _, part := range splitHeadingParts(section) {
		addRetrievalTerm(weights, part, weight+1)
		for _, gram := range extractHanNGrams(part, 2, 4) {
			addRetrievalTerm(weights, gram, 1)
		}
	}
	for _, gram := range extractHanNGrams(section, 2, 4) {
		addRetrievalTerm(weights, gram, 1)
	}
}

func normalizeHeadingTerm(section string) string {
	section = normalizeText(section)
	section = headingIndexPattern.ReplaceAllString(section, "")
	section = strings.Trim(section, ".-:：|/·,，。；、()（）[]【】")
	return normalizeText(section)
}

func splitHeadingParts(section string) []string {
	replacer := strings.NewReplacer(
		"（", " ",
		"）", " ",
		"(", " ",
		")", " ",
		"/", " ",
		"|", " ",
		"·", " ",
		"与", " ",
		"和", " ",
		"及", " ",
		"、", " ",
	)
	parts := strings.Fields(replacer.Replace(section))
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = normalizeHeadingTerm(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	if len(out) == 0 && section != "" {
		out = append(out, section)
	}
	return out
}

func addRetrievalTerm(weights map[string]int, term string, weight int) {
	term = normalizeText(term)
	term = headingIndexPattern.ReplaceAllString(term, "")
	term = strings.Trim(term, ".-:：|/·,，。；、()（）[]【】")
	compact := compactTermText(term)
	if weight <= 0 || len([]rune(compact)) < 2 {
		return
	}
	if !containsHanRune(compact) && !asciiLetterPattern.MatchString(compact) {
		return
	}
	weights[term] += weight
}

func extractHanNGrams(text string, minN int, maxN int) []string {
	compact := compactTermText(text)
	if compact == "" {
		return nil
	}
	runes := []rune(compact)
	if len(runes) < minN {
		return nil
	}
	if maxN > len(runes) {
		maxN = len(runes)
	}
	out := make([]string, 0, 8)
	seen := make(map[string]struct{}, 8)
	for size := minN; size <= maxN; size++ {
		for start := 0; start+size <= len(runes); start++ {
			part := string(runes[start : start+size])
			if !containsHanRune(part) {
				continue
			}
			if _, ok := seen[part]; ok {
				continue
			}
			seen[part] = struct{}{}
			out = append(out, part)
		}
	}
	return out
}

func containsHanRune(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func extractYearMilestones(chunks []rawChunk) []YearMilestone {
	type milestoneAccumulator struct {
		Year    int
		Title   string
		Section string
		Summary []string
	}

	items := make(map[string]*milestoneAccumulator, 8)
	for _, chunk := range chunks {
		year, title, section, ok := parseYearMilestoneChunk(chunk)
		if !ok {
			continue
		}
		key := fmt.Sprintf("%d|%s", year, title)
		item, exists := items[key]
		if !exists {
			item = &milestoneAccumulator{
				Year:    year,
				Title:   title,
				Section: section,
			}
			items[key] = item
		}
		text := normalizeText(chunk.Text)
		if text != "" {
			item.Summary = append(item.Summary, text)
		}
	}

	out := make([]YearMilestone, 0, len(items))
	for _, item := range items {
		out = append(out, YearMilestone{
			Year:    item.Year,
			Title:   item.Title,
			Summary: normalizeText(strings.Join(item.Summary, " ")),
			Section: item.Section,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Year == out[j].Year {
			return out[i].Title < out[j].Title
		}
		return out[i].Year < out[j].Year
	})
	return out
}

func parseYearMilestoneChunk(chunk rawChunk) (int, string, string, bool) {
	section := normalizeText(chunk.Section)
	if section == "" {
		return 0, "", "", false
	}
	parts := strings.Split(section, " > ")
	if len(parts) == 0 {
		return 0, "", "", false
	}
	match := yearHeadingPattern.FindStringSubmatch(normalizeText(parts[len(parts)-1]))
	if len(match) == 0 {
		return 0, "", "", false
	}
	year, err := strconv.Atoi(match[1])
	if err != nil {
		return 0, "", "", false
	}
	title := normalizeText(match[2])
	return year, title, section, true
}

func extractProductPrices(raw string) []ProductPrice {
	scanner := bufio.NewScanner(strings.NewReader(raw))
	sections := make([]string, 0, 6)
	tableLines := make([]string, 0, 8)
	prices := make([]ProductPrice, 0, 16)

	flushTable := func() {
		if len(tableLines) == 0 {
			return
		}
		section := normalizeText(strings.Join(sections, " > "))
		prices = append(prices, parseProductPriceTable(tableLines, section)...)
		tableLines = nil
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			flushTable()
			continue
		}
		if strings.HasPrefix(line, "#") {
			flushTable()
			level, title := parseHeading(line)
			if title == "" {
				continue
			}
			if level <= 0 {
				level = 1
			}
			if level > len(sections)+1 {
				level = len(sections) + 1
			}
			sections = append([]string(nil), sections[:level-1]...)
			sections = append(sections, title)
			continue
		}
		if isMarkdownTableLine(line) {
			tableLines = append(tableLines, line)
			continue
		}
		flushTable()
	}
	flushTable()

	if len(prices) == 0 {
		return nil
	}

	deduped := make([]ProductPrice, 0, len(prices))
	seen := make(map[string]struct{}, len(prices))
	for _, item := range prices {
		key := normalizeText(item.Name)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, item)
	}
	return deduped
}

func parseProductPriceTable(lines []string, section string) []ProductPrice {
	if len(lines) < 3 {
		return nil
	}

	headers := parseMarkdownTableRow(lines[0])
	if len(headers) == 0 {
		return nil
	}
	if !isMarkdownSeparatorRow(parseMarkdownTableRow(lines[1])) {
		return nil
	}

	nameIdx := findTableHeaderIndex(headers, "产品名称")
	priceIdx := findTableHeaderIndex(headers, "建议零售价")
	if nameIdx < 0 || priceIdx < 0 {
		return nil
	}

	out := make([]ProductPrice, 0, len(lines)-2)
	for _, line := range lines[2:] {
		cells := parseMarkdownTableRow(line)
		if len(cells) == 0 {
			continue
		}
		name := tableCell(cells, nameIdx)
		priceText := tableCell(cells, priceIdx)
		priceValue, ok := parsePriceValue(priceText)
		if !ok || name == "" {
			continue
		}
		out = append(out, ProductPrice{
			Name:       name,
			PriceText:  priceText,
			PriceValue: priceValue,
			Section:    section,
		})
	}
	return out
}

func isMarkdownTableLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|")
}

func parseMarkdownTableRow(line string) []string {
	line = strings.TrimSpace(line)
	if line == "" || !strings.Contains(line, "|") {
		return nil
	}
	parts := strings.Split(line, "|")
	if len(parts) >= 2 {
		parts = parts[1 : len(parts)-1]
	}
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, normalizeText(part))
	}
	return cells
}

func isMarkdownSeparatorRow(cells []string) bool {
	if len(cells) == 0 {
		return false
	}
	for _, cell := range cells {
		if cell == "" {
			return false
		}
		trimmed := strings.ReplaceAll(cell, ":", "")
		trimmed = strings.ReplaceAll(trimmed, "-", "")
		if trimmed != "" {
			return false
		}
	}
	return true
}

func findTableHeaderIndex(headers []string, target string) int {
	target = normalizeText(target)
	for idx, header := range headers {
		if normalizeText(header) == target {
			return idx
		}
	}
	return -1
}

func tableCell(cells []string, idx int) string {
	if idx < 0 || idx >= len(cells) {
		return ""
	}
	return normalizeText(cells[idx])
}

func parsePriceValue(priceText string) (float64, bool) {
	match := priceNumberPattern.FindString(strings.TrimSpace(priceText))
	if match == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func vectorNorm(vec []float64) float64 {
	var sum float64
	for _, v := range vec {
		sum += v * v
	}
	return math.Sqrt(sum)
}

func cosineSimilarity(a []float64, aNorm float64, b []float64, bNorm float64) float64 {
	if len(a) == 0 || len(b) == 0 || aNorm == 0 || bNorm == 0 {
		return 0
	}
	size := len(a)
	if len(b) < size {
		size = len(b)
	}
	var dot float64
	for i := 0; i < size; i++ {
		dot += a[i] * b[i]
	}
	return dot / (aNorm * bNorm)
}
