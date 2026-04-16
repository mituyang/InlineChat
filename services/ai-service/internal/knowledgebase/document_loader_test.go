package knowledgebase

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestNewChunkIDIsStableUUID(t *testing.T) {
	first := newChunk("site_test", "knowledge.md", "产品介绍", "这是一段知识内容。")
	second := newChunk("site_test", "knowledge.md", "产品介绍", "这是一段知识内容。")

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
