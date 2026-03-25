package knowledgebase

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	defaultChunkChars   = 1000
	defaultChunkOverlap = 120
	defaultEmbedBatch   = 16
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

type Status struct {
	ChunkCount int
	LoadedAt   time.Time
	LastError  string
}

type Manager struct {
	path     string
	embedder embedder
	logger   *zap.Logger

	mu        sync.RWMutex
	chunks    []Chunk
	loadedAt  time.Time
	lastError error
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

	status := Status{
		ChunkCount: len(chunks),
		LoadedAt:   time.Now(),
	}

	m.mu.Lock()
	m.chunks = chunks
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
