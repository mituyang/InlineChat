package knowledgebase

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const statusDirName = ".inlinechat-state"

type statusStore struct {
	rootDir string
}

func newStatusStore(rootDir string) *statusStore {
	return &statusStore{rootDir: strings.TrimSpace(rootDir)}
}

func (s *statusStore) Load(siteID string) (SiteStatus, error) {
	status := SiteStatus{
		SiteID:       siteID,
		KnowledgeDir: knowledgeDirForSite(s.rootDir, siteID),
		IndexStatus:  StatusIdle,
	}
	path := s.pathFor(siteID)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return status, nil
		}
		return SiteStatus{}, fmt.Errorf("read status file failed: %w", err)
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return SiteStatus{}, fmt.Errorf("decode status file failed: %w", err)
	}
	if strings.TrimSpace(status.SiteID) == "" {
		status.SiteID = siteID
	}
	if strings.TrimSpace(status.KnowledgeDir) == "" {
		status.KnowledgeDir = knowledgeDirForSite(s.rootDir, siteID)
	}
	if strings.TrimSpace(status.IndexStatus) == "" {
		status.IndexStatus = StatusIdle
	}
	return status, nil
}

func (s *statusStore) Save(status SiteStatus) error {
	if strings.TrimSpace(status.SiteID) == "" {
		return fmt.Errorf("site_id is required")
	}
	if strings.TrimSpace(status.KnowledgeDir) == "" {
		status.KnowledgeDir = knowledgeDirForSite(s.rootDir, status.SiteID)
	}
	if strings.TrimSpace(status.IndexStatus) == "" {
		status.IndexStatus = StatusIdle
	}
	path := s.pathFor(status.SiteID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create status directory failed: %w", err)
	}
	raw, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return fmt.Errorf("write status file failed: %w", err)
	}
	return nil
}

func (s *statusStore) pathFor(siteID string) string {
	filename := strings.TrimSpace(siteID) + ".json"
	return filepath.Join(s.rootDir, statusDirName, filename)
}
