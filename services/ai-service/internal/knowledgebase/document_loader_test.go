package knowledgebase

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewChunkIDIsStableUUID(t *testing.T) {
	first := newChunk("site_test", "knowledge.md", "产品介绍", "这是一段知识内容。", ChunkKindNarrative, nil)
	second := newChunk("site_test", "knowledge.md", "产品介绍", "这是一段知识内容。", ChunkKindNarrative, nil)

	if first.ID != second.ID {
		t.Fatalf("expected stable chunk id, got %q and %q", first.ID, second.ID)
	}
	if _, err := uuid.Parse(first.ID); err != nil {
		t.Fatalf("expected chunk id to be uuid, got %q: %v", first.ID, err)
	}
}

func TestChunkDocumentFlushesAtSectionBoundary(t *testing.T) {
	raw := "# 公司信息\n总部位于杭州。\n\n# 联系方式\n客服电话 400-800-0000。"

	chunks := chunkDocument("site_test", "knowledge.md", ".md", raw, 200, 50)
	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2", len(chunks))
	}
	sections := []string{chunks[0].Section, chunks[1].Section}
	if !slices.Equal(sections, []string{"公司信息", "联系方式"}) {
		t.Fatalf("sections = %#v", sections)
	}
	if chunks[0].Text != "总部位于杭州。" {
		t.Fatalf("chunk[0].Text = %q", chunks[0].Text)
	}
	if chunks[1].Text != "客服电话 400-800-0000。" {
		t.Fatalf("chunk[1].Text = %q", chunks[1].Text)
	}
}

func TestLoadSiteChunksExtractsStructuredFactChunksFromMarkdownTable(t *testing.T) {
	rootDir := t.TempDir()
	siteDir := filepath.Join(rootDir, "site_test")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	content := `# 企业信息总览
| 项目 | 内容 |
| --- | --- |
| 品牌名称 | 青禾家居 |
| 总部所在地 | 浙江杭州 |
`
	if err := os.WriteFile(filepath.Join(siteDir, "knowledge.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, chunks, err := loadSiteChunks(rootDir, "site_test")
	if err != nil {
		t.Fatalf("loadSiteChunks() error = %v", err)
	}

	var fact Chunk
	for _, item := range chunks {
		if item.Kind == ChunkKindFact && strings.Contains(item.Text, "品牌名称：青禾家居") {
			fact = item
			break
		}
	}
	if fact.ID == "" {
		t.Fatalf("expected structured fact chunk, got %#v", chunks)
	}
	if fact.Section != "企业信息总览" {
		t.Fatalf("fact.Section = %q", fact.Section)
	}
	if !slices.Contains(fact.Keywords, "品牌名称") {
		t.Fatalf("expected fact keywords to contain 品牌名称, got %#v", fact.Keywords)
	}
}

func TestChunkDocumentExtractsFAQChunks(t *testing.T) {
	raw := `# 常见问题
### Q：支持几天退换？

A：支持 30 天无忧退换。`

	chunks := chunkDocument("site_test", "faq.md", ".md", raw, 200, 50)
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if chunks[0].Kind != ChunkKindFAQ {
		t.Fatalf("chunk.Kind = %q", chunks[0].Kind)
	}
	if chunks[0].Section != "常见问题" {
		t.Fatalf("chunk.Section = %q", chunks[0].Section)
	}
	if chunks[0].Text != "问题：支持几天退换？\n答案：支持 30 天无忧退换。" {
		t.Fatalf("chunk.Text = %q", chunks[0].Text)
	}
}

func TestChunkDocumentExtractsBoldFAQChunks(t *testing.T) {
	raw := `# 常见问题
**Q：青禾家居的主推产品有哪些？**
A：当前重点展示产品包括云感记忆棉枕、暮岚针织四件套和极简落地灯。`

	chunks := chunkDocument("site_test", "faq.md", ".md", raw, 300, 50)
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if chunks[0].Kind != ChunkKindFAQ {
		t.Fatalf("chunk.Kind = %q", chunks[0].Kind)
	}
	if chunks[0].Text != "问题：青禾家居的主推产品有哪些？\n答案：当前重点展示产品包括云感记忆棉枕、暮岚针织四件套和极简落地灯。" {
		t.Fatalf("chunk.Text = %q", chunks[0].Text)
	}
}
