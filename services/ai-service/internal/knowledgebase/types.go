package knowledgebase

import "time"

const (
	StatusIdle     = "idle"
	StatusIndexing = "indexing"
	StatusReady    = "ready"
	StatusError    = "error"

	ChunkKindNarrative = "narrative"
	ChunkKindFact      = "fact"
	ChunkKindFAQ       = "faq"
)

type SearchResult struct {
	ID         string
	Section    string
	Text       string
	SourcePath string
	Kind       string
	Keywords   []string
	Score      float64
}

type SiteStatus struct {
	SiteID         string    `json:"site_id"`
	KnowledgeDir   string    `json:"knowledge_dir"`
	IndexStatus    string    `json:"index_status"`
	IndexedChunks  int       `json:"indexed_chunks"`
	LastIndexedAt  time.Time `json:"last_indexed_at,omitempty"`
	LastIndexError string    `json:"last_index_error,omitempty"`
	ActiveJobID    string    `json:"active_job_id,omitempty"`
}

type ReindexJob struct {
	SiteID string `json:"site_id"`
	JobID  string `json:"job_id"`
	Status string `json:"status"`
}

type Chunk struct {
	ID         string
	Section    string
	Text       string
	SourcePath string
	Kind       string
	Keywords   []string
}

type vectorPoint struct {
	ID         string
	Vector     []float64
	Section    string
	Text       string
	SourcePath string
	Kind       string
	Keywords   []string
	SiteID     string
}
