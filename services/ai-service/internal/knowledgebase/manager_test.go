package knowledgebase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"inlinechat/services/ai-service/internal/reranker"
)

type fakeEmbedder struct{}

func (fakeEmbedder) CreateEmbeddings(_ context.Context, inputs []string) ([][]float64, error) {
	out := make([][]float64, 0, len(inputs))
	for idx, item := range inputs {
		out = append(out, []float64{float64(len(item)), float64(idx + 1), 1})
	}
	return out, nil
}

type fakeReranker struct{}

func (fakeReranker) Rerank(_ context.Context, _ string, texts []string) ([]reranker.Result, error) {
	results := make([]reranker.Result, 0, len(texts))
	for idx := range texts {
		results = append(results, reranker.Result{
			Index: idx,
			Score: float64(len(texts) - idx),
		})
	}
	return results, nil
}

type failingReranker struct{}

func (failingReranker) Rerank(_ context.Context, _ string, _ []string) ([]reranker.Result, error) {
	return nil, context.DeadlineExceeded
}

type fakeVectorIndex struct {
	sitePoints map[string][]vectorPoint
}

func newFakeVectorIndex() *fakeVectorIndex {
	return &fakeVectorIndex{sitePoints: make(map[string][]vectorPoint)}
}

func (f *fakeVectorIndex) Ready(_ context.Context) error {
	return nil
}

func (f *fakeVectorIndex) ReplaceSite(_ context.Context, siteID string, points []vectorPoint) error {
	copied := make([]vectorPoint, len(points))
	copy(copied, points)
	f.sitePoints[siteID] = copied
	return nil
}

func (f *fakeVectorIndex) Search(_ context.Context, siteID string, _ []float64, limit int) ([]qdrantSearchResult, error) {
	items := f.sitePoints[siteID]
	if limit > len(items) {
		limit = len(items)
	}
	results := make([]qdrantSearchResult, 0, limit)
	for _, item := range items[:limit] {
		results = append(results, qdrantSearchResult{
			ID:         item.ID,
			Section:    item.Section,
			Text:       item.Text,
			SourcePath: item.SourcePath,
			Score:      0.9,
		})
	}
	return results, nil
}

type emptySearchVectorIndex struct {
	*fakeVectorIndex
}

func newEmptySearchVectorIndex() *emptySearchVectorIndex {
	return &emptySearchVectorIndex{fakeVectorIndex: newFakeVectorIndex()}
}

func (f *emptySearchVectorIndex) Search(_ context.Context, _ string, _ []float64, _ int) ([]qdrantSearchResult, error) {
	return nil, nil
}

func TestManagerTriggerReindexAndSearch(t *testing.T) {
	rootDir := t.TempDir()
	siteDir := filepath.Join(rootDir, "site_demo")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, "faq.md"), []byte("# 售后政策\n支持 7 天无理由退换货。"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	vectorIndex := newFakeVectorIndex()
	manager := New(
		rootDir,
		fakeEmbedder{},
		fakeReranker{},
		vectorIndex,
		zap.NewNop(),
		2,
		8,
		2,
		0.5,
	)

	job, err := manager.TriggerReindex(context.Background(), "site_demo")
	if err != nil {
		t.Fatalf("TriggerReindex() error = %v", err)
	}
	if job.Status != StatusIndexing {
		t.Fatalf("job.Status = %q", job.Status)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := manager.GetStatus("site_demo")
		if statusErr != nil {
			t.Fatalf("GetStatus() error = %v", statusErr)
		}
		if status.IndexStatus == StatusReady {
			if status.IndexedChunks == 0 {
				t.Fatal("expected indexed chunks to be greater than 0")
			}
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	status, err := manager.GetStatus("site_demo")
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}
	if status.IndexStatus != StatusReady {
		t.Fatalf("status.IndexStatus = %q", status.IndexStatus)
	}
	if !strings.Contains(status.KnowledgeDir, "site_demo") {
		t.Fatalf("KnowledgeDir = %q", status.KnowledgeDir)
	}

	results, err := manager.Search(context.Background(), "site_demo", "支持退换货吗")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results after reindex")
	}
	if !strings.Contains(results[0].Text, "7 天无理由退换货") {
		t.Fatalf("unexpected result text = %q", results[0].Text)
	}
}

func TestManagerSearchFallsBackWhenRerankerFails(t *testing.T) {
	rootDir := t.TempDir()
	siteDir := filepath.Join(rootDir, "site_demo")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, "faq.md"), []byte("# 产品体系\n青禾家居当前形成四大核心产品线：睡眠家居、居家照明、收纳整理、厨房家居。"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	vectorIndex := newFakeVectorIndex()
	manager := New(
		rootDir,
		fakeEmbedder{},
		failingReranker{},
		vectorIndex,
		zap.NewNop(),
		2,
		8,
		2,
		0.5,
	)

	if _, err := manager.TriggerReindex(context.Background(), "site_demo"); err != nil {
		t.Fatalf("TriggerReindex() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := manager.GetStatus("site_demo")
		if statusErr != nil {
			t.Fatalf("GetStatus() error = %v", statusErr)
		}
		if status.IndexStatus == StatusReady {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	results, err := manager.Search(context.Background(), "site_demo", "介绍下你们产品")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected vector fallback results when reranker fails")
	}
}

func TestBuildRerankInputsAppliesBudget(t *testing.T) {
	candidates := make([]qdrantSearchResult, 0, 12)
	for i := 0; i < 12; i++ {
		candidates = append(candidates, qdrantSearchResult{
			ID:         fmt.Sprintf("id-%d", i),
			Section:    "section",
			Text:       strings.Repeat("青", 100),
			SourcePath: "knowledge.md",
			Score:      0.9,
		})
	}

	texts, indexes := buildRerankInputs(candidates)
	if len(texts) != 3 {
		t.Fatalf("len(texts) = %d, want 3", len(texts))
	}
	if len(indexes) != len(texts) {
		t.Fatalf("len(indexes) = %d, want %d", len(indexes), len(texts))
	}
	for idx, text := range texts {
		if runeCount(text) > maxRerankTextRunes {
			t.Fatalf("text %d too long: %d", idx, runeCount(text))
		}
		if indexes[idx] != idx {
			t.Fatalf("indexes[%d] = %d, want %d", idx, indexes[idx], idx)
		}
	}
}

func TestBuildRerankInputsIncludesSectionContext(t *testing.T) {
	candidates := []qdrantSearchResult{
		{
			ID:         "c1",
			Section:    "总部地址",
			Text:       "杭州市滨江区长河街道青禾设计中心",
			SourcePath: "knowledge.md",
			Score:      0.9,
		},
	}

	texts, indexes := buildRerankInputs(candidates)
	if len(texts) != 1 || len(indexes) != 1 {
		t.Fatalf("unexpected lengths: texts=%d indexes=%d", len(texts), len(indexes))
	}
	if !strings.Contains(texts[0], "总部地址") {
		t.Fatalf("expected rerank text to include section, got %q", texts[0])
	}
}

func TestManagerSearchFallsBackToKeywordMatchingWhenVectorSearchIsEmpty(t *testing.T) {
	rootDir := t.TempDir()
	siteDir := filepath.Join(rootDir, "site_demo")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(siteDir, "brand.md"), []byte("# 品牌定位与核心理念\n## 品牌定位\n青禾家居定位为高性价比家居生活品牌。"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	vectorIndex := newEmptySearchVectorIndex()
	manager := New(
		rootDir,
		fakeEmbedder{},
		fakeReranker{},
		vectorIndex,
		zap.NewNop(),
		2,
		8,
		2,
		0.5,
	)

	if _, err := manager.TriggerReindex(context.Background(), "site_demo"); err != nil {
		t.Fatalf("TriggerReindex() error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, statusErr := manager.GetStatus("site_demo")
		if statusErr != nil {
			t.Fatalf("GetStatus() error = %v", statusErr)
		}
		if status.IndexStatus == StatusReady {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	results, err := manager.Search(context.Background(), "site_demo", "请介绍下你们的品牌定位是什么")
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected keyword fallback results when vector search is empty")
	}
	if !strings.Contains(results[0].Text, "高性价比家居生活品牌") {
		t.Fatalf("unexpected result text = %q", results[0].Text)
	}
}

func TestExtractKeywordFallbackTokensBuildsChineseNGrams(t *testing.T) {
	tokens := extractKeywordFallbackTokens("请介绍下你们的品牌定位是什么")
	if len(tokens) == 0 {
		t.Fatal("expected tokens")
	}
	if !slices.Contains(tokens, "品牌定位") {
		t.Fatalf("expected tokens to contain 品牌定位, got %q", strings.Join(tokens, ","))
	}
}

func TestKeywordSearchCandidatesMatchesSectionTitle(t *testing.T) {
	chunks := []Chunk{
		{
			ID:         "c1",
			Section:    "品牌定位",
			Text:       "青禾家居定位为高性价比家居生活品牌。",
			SourcePath: "brand.md",
		},
	}

	results := keywordSearchCandidates(chunks, "请介绍下你们的品牌定位是什么", 4)
	if len(results) == 0 {
		t.Fatal("expected keyword matches")
	}
	if results[0].ID != "c1" {
		t.Fatalf("unexpected result id = %q", results[0].ID)
	}
}

func TestExtractKeywordFallbackTokensExpandsLocationQuestion(t *testing.T) {
	tokens := extractKeywordFallbackTokens("你们总部在哪")
	if !slices.Contains(tokens, "总部地址") {
		t.Fatalf("expected tokens to contain 总部地址, got %q", strings.Join(tokens, ","))
	}
}

func TestKeywordSearchCandidatesPrefersHeadquarterAddressForLocationQuestion(t *testing.T) {
	chunks := []Chunk{
		{
			ID:         "hq",
			Section:    "总部地址",
			Text:       "杭州市滨江区长河街道青禾设计中心",
			SourcePath: "knowledge.md",
		},
		{
			ID:         "warehouse",
			Section:    "仓配地址",
			Text:       "华东仓：杭州临平",
			SourcePath: "knowledge.md",
		},
	}

	results := keywordSearchCandidates(chunks, "你们总部在哪", 4)
	if len(results) == 0 {
		t.Fatal("expected keyword matches")
	}
	if results[0].ID != "hq" {
		t.Fatalf("expected hq first, got %q", results[0].ID)
	}
}
