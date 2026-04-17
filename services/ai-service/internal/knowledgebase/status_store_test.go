package knowledgebase

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusStoreLoadReturnsDefaultForEmptyFile(t *testing.T) {
	rootDir := t.TempDir()
	store := newStatusStore(rootDir)
	path := store.pathFor("site_demo")

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	status, err := store.Load("site_demo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if status.SiteID != "site_demo" {
		t.Fatalf("status.SiteID = %q, want %q", status.SiteID, "site_demo")
	}
	if status.IndexStatus != StatusIdle {
		t.Fatalf("status.IndexStatus = %q, want %q", status.IndexStatus, StatusIdle)
	}
}

func TestStatusStoreSaveAndLoad(t *testing.T) {
	rootDir := t.TempDir()
	store := newStatusStore(rootDir)

	input := SiteStatus{
		SiteID:         "site_demo",
		KnowledgeDir:   filepath.Join(rootDir, "site_demo"),
		IndexStatus:    StatusReady,
		IndexedChunks:  3,
		LastIndexError: "",
		ActiveJobID:    "",
	}
	if err := store.Save(input); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	status, err := store.Load("site_demo")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if status.SiteID != input.SiteID {
		t.Fatalf("status.SiteID = %q, want %q", status.SiteID, input.SiteID)
	}
	if status.IndexStatus != input.IndexStatus {
		t.Fatalf("status.IndexStatus = %q, want %q", status.IndexStatus, input.IndexStatus)
	}
	if status.IndexedChunks != input.IndexedChunks {
		t.Fatalf("status.IndexedChunks = %d, want %d", status.IndexedChunks, input.IndexedChunks)
	}
}
