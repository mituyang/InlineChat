package knowledgebase

import (
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	defaultChunkChars   = 900
	defaultChunkOverlap = 120
)

var (
	htmlScriptPattern = regexp.MustCompile(`(?is)<script.*?</script>`)
	htmlStylePattern  = regexp.MustCompile(`(?is)<style.*?</style>`)
	htmlTagPattern    = regexp.MustCompile(`(?s)<[^>]+>`)
)

type paragraph struct {
	Section string
	Text    string
}

func loadSiteChunks(rootDir string, siteID string) (string, []Chunk, error) {
	knowledgeDir := knowledgeDirForSite(rootDir, siteID)
	info, err := os.Stat(knowledgeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return knowledgeDir, nil, fmt.Errorf("knowledge directory does not exist")
		}
		return knowledgeDir, nil, fmt.Errorf("stat knowledge directory failed: %w", err)
	}
	if !info.IsDir() {
		return knowledgeDir, nil, fmt.Errorf("knowledge directory is not a directory")
	}

	files := make([]string, 0, 16)
	err = filepath.WalkDir(knowledgeDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != knowledgeDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !isKnowledgeFile(path) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return knowledgeDir, nil, fmt.Errorf("scan knowledge directory failed: %w", err)
	}
	sort.Strings(files)

	chunks := make([]Chunk, 0, len(files)*4)
	for _, path := range files {
		relPath, err := filepath.Rel(knowledgeDir, path)
		if err != nil {
			return knowledgeDir, nil, fmt.Errorf("resolve relative path failed: %w", err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return knowledgeDir, nil, fmt.Errorf("read knowledge file failed: %w", err)
		}
		text := normalizeSourceText(path, string(raw))
		if strings.TrimSpace(text) == "" {
			continue
		}
		fileChunks := chunkDocument(siteID, relPath, filepath.Ext(path), text, defaultChunkChars, defaultChunkOverlap)
		chunks = append(chunks, fileChunks...)
	}
	return knowledgeDir, chunks, nil
}

func isKnowledgeFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt", ".html", ".htm":
		return true
	default:
		return false
	}
}

func normalizeSourceText(path string, raw string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm":
		return normalizeText(stripHTML(raw))
	default:
		return normalizeText(raw)
	}
}

func stripHTML(raw string) string {
	text := htmlScriptPattern.ReplaceAllString(raw, " ")
	text = htmlStylePattern.ReplaceAllString(text, " ")
	text = htmlTagPattern.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	return text
}

func chunkDocument(siteID string, relativePath string, ext string, raw string, chunkSize int, overlap int) []Chunk {
	if chunkSize <= 0 {
		chunkSize = defaultChunkChars
	}
	if overlap < 0 {
		overlap = 0
	}
	paragraphs := extractParagraphs(relativePath, ext, raw)
	if len(paragraphs) == 0 {
		return nil
	}

	chunks := make([]Chunk, 0, len(paragraphs))
	var builder strings.Builder
	currentSection := relativePath
	for _, item := range paragraphs {
		part := strings.TrimSpace(item.Text)
		if part == "" {
			continue
		}
		nextSection := currentSection
		if item.Section != "" {
			nextSection = item.Section
		}
		if builder.Len() > 0 && nextSection != currentSection {
			text := strings.TrimSpace(builder.String())
			chunks = append(chunks, newChunk(siteID, relativePath, currentSection, text))
			builder.Reset()
		}
		currentSection = nextSection
		candidate := part
		if builder.Len() > 0 {
			candidate = builder.String() + "\n\n" + part
		}
		if runeCount(candidate) > chunkSize && builder.Len() > 0 {
			text := strings.TrimSpace(builder.String())
			chunks = append(chunks, newChunk(siteID, relativePath, currentSection, text))
			builder.Reset()
			if overlap > 0 {
				builder.WriteString(trimToLastRunes(text, overlap))
			}
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(part)
	}
	if builder.Len() > 0 {
		text := strings.TrimSpace(builder.String())
		chunks = append(chunks, newChunk(siteID, relativePath, currentSection, text))
	}
	return dedupeChunks(chunks)
}

func extractParagraphs(relativePath string, ext string, raw string) []paragraph {
	lines := strings.Split(raw, "\n")
	currentSection := relativePath
	buffer := make([]string, 0, 4)
	out := make([]paragraph, 0, len(lines)/3+1)

	flush := func() {
		text := normalizeText(strings.Join(buffer, " "))
		buffer = buffer[:0]
		if text == "" {
			return
		}
		out = append(out, paragraph{
			Section: currentSection,
			Text:    text,
		})
	}

	isMarkdown := strings.EqualFold(ext, ".md")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			continue
		}
		if isMarkdown && strings.HasPrefix(trimmed, "#") {
			flush()
			currentSection = strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			if currentSection == "" {
				currentSection = relativePath
			}
			continue
		}
		buffer = append(buffer, trimmed)
	}
	flush()
	return out
}

func newChunk(siteID string, relativePath string, section string, text string) Chunk {
	section = strings.TrimSpace(section)
	if section == "" {
		section = relativePath
	}
	text = normalizeText(text)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(siteID+"\n"+relativePath+"\n"+section+"\n"+text))
	return Chunk{
		ID:         id.String(),
		Section:    section,
		Text:       text,
		SourcePath: filepath.ToSlash(relativePath),
	}
}

func dedupeChunks(items []Chunk) []Chunk {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]Chunk, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		if _, ok := seen[item.ID]; ok {
			continue
		}
		seen[item.ID] = struct{}{}
		out = append(out, item)
	}
	return out
}

func knowledgeDirForSite(rootDir string, siteID string) string {
	return filepath.Join(strings.TrimSpace(rootDir), strings.TrimSpace(siteID))
}

func normalizeText(text string) string {
	lines := strings.Split(text, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		cleaned = append(cleaned, strings.Join(strings.Fields(trimmed), " "))
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

func trimToLastRunes(text string, limit int) string {
	if limit <= 0 || text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[len(runes)-limit:]))
}

func runeCount(text string) int {
	return utf8.RuneCountInString(text)
}
