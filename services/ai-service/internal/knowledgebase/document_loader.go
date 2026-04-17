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
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	defaultChunkChars   = 900
	defaultChunkOverlap = 120
)

var (
	htmlScriptPattern         = regexp.MustCompile(`(?is)<script.*?</script>`)
	htmlStylePattern          = regexp.MustCompile(`(?is)<style.*?</style>`)
	htmlTagPattern            = regexp.MustCompile(`(?s)<[^>]+>`)
	sectionOrderPrefixPattern = regexp.MustCompile(`^\d+(?:\.\d+)*\.?\s*`)
)

type paragraph struct {
	Section  string
	Text     string
	Kind     string
	Keywords []string
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
	currentKeywords := []string(nil)
	flushBuilder := func() {
		text := strings.TrimSpace(builder.String())
		builder.Reset()
		if text == "" {
			currentKeywords = nil
			return
		}
		chunks = append(chunks, newChunk(siteID, relativePath, currentSection, text, ChunkKindNarrative, currentKeywords))
		currentKeywords = nil
	}
	for _, item := range paragraphs {
		part := strings.TrimSpace(item.Text)
		if part == "" {
			continue
		}
		nextSection := currentSection
		if item.Section != "" {
			nextSection = item.Section
		}
		if item.Kind == ChunkKindFact || item.Kind == ChunkKindFAQ {
			if builder.Len() > 0 {
				flushBuilder()
			}
			chunks = append(chunks, newChunk(siteID, relativePath, nextSection, part, item.Kind, item.Keywords))
			currentSection = nextSection
			continue
		}
		if builder.Len() > 0 && nextSection != currentSection {
			flushBuilder()
		}
		currentSection = nextSection
		candidate := part
		if builder.Len() > 0 {
			candidate = builder.String() + "\n\n" + part
		}
		if runeCount(candidate) > chunkSize && builder.Len() > 0 {
			text := strings.TrimSpace(builder.String())
			flushBuilder()
			if overlap > 0 {
				builder.WriteString(trimToLastRunes(text, overlap))
			}
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(part)
		currentKeywords = mergeKeywords(currentKeywords, item.Keywords)
	}
	if builder.Len() > 0 {
		flushBuilder()
	}
	return dedupeChunks(chunks)
}

func extractParagraphs(relativePath string, ext string, raw string) []paragraph {
	lines := strings.Split(raw, "\n")
	currentSection := relativePath
	sectionStack := make([]string, 0, 4)
	buffer := make([]string, 0, 4)
	out := make([]paragraph, 0, len(lines)/3+1)
	pendingFAQSection := ""
	pendingFAQQuestion := ""
	pendingFAQAnswers := make([]string, 0, 4)

	flush := func() {
		text := normalizeText(strings.Join(buffer, " "))
		buffer = buffer[:0]
		if text == "" {
			return
		}
		out = append(out, paragraph{
			Section:  currentSection,
			Text:     text,
			Kind:     ChunkKindNarrative,
			Keywords: narrativeSectionKeywords(currentSection),
		})
	}
	flushFAQ := func() {
		if pendingFAQQuestion == "" {
			pendingFAQAnswers = pendingFAQAnswers[:0]
			return
		}
		answer := normalizeText(strings.Join(pendingFAQAnswers, "\n"))
		if answer != "" {
			section := strings.TrimSpace(pendingFAQSection)
			if section == "" {
				section = currentSection
			}
			out = append(out, paragraph{
				Section:  section,
				Text:     "问题：" + normalizeText(pendingFAQQuestion) + "\n答案：" + answer,
				Kind:     ChunkKindFAQ,
				Keywords: normalizeKeywords([]string{section, pendingFAQQuestion, answer}),
			})
		}
		pendingFAQSection = ""
		pendingFAQQuestion = ""
		pendingFAQAnswers = pendingFAQAnswers[:0]
	}

	isMarkdown := strings.EqualFold(ext, ".md")
	for idx := 0; idx < len(lines); {
		trimmed := strings.TrimSpace(lines[idx])
		if trimmed == "" {
			flush()
			if pendingFAQQuestion != "" && len(pendingFAQAnswers) > 0 {
				flushFAQ()
			}
			idx++
			continue
		}
		if isMarkdown && isMarkdownTableLine(trimmed) {
			flush()
			if pendingFAQQuestion != "" {
				flushFAQ()
			}
			tableParagraphs, consumed := parseMarkdownTable(currentSection, lines[idx:])
			if consumed > 0 {
				out = append(out, tableParagraphs...)
				idx += consumed
				continue
			}
		}
		if isMarkdown && strings.HasPrefix(trimmed, "#") {
			flush()
			if pendingFAQQuestion != "" {
				flushFAQ()
			}
			level, heading, ok := parseMarkdownHeading(trimmed)
			if !ok || heading == "" {
				currentSection = buildSectionPath(relativePath, sectionStack)
				idx++
				continue
			}
			if question, ok := extractFAQQuestion(heading); ok {
				pendingFAQSection = currentSection
				pendingFAQQuestion = question
				pendingFAQAnswers = pendingFAQAnswers[:0]
				idx++
				continue
			}
			if looksLikeQuestion(heading) {
				pendingFAQSection = currentSection
				pendingFAQQuestion = heading
				pendingFAQAnswers = pendingFAQAnswers[:0]
				idx++
				continue
			}
			if level < 1 {
				level = 1
			}
			if level > len(sectionStack)+1 {
				level = len(sectionStack) + 1
			}
			sectionStack = append(sectionStack[:level-1], heading)
			currentSection = buildSectionPath(relativePath, sectionStack)
			idx++
			continue
		}
		if question, ok := extractFAQQuestion(trimmed); ok {
			flush()
			if pendingFAQQuestion != "" {
				flushFAQ()
			}
			pendingFAQSection = currentSection
			pendingFAQQuestion = question
			pendingFAQAnswers = pendingFAQAnswers[:0]
			idx++
			continue
		}
		if pendingFAQQuestion != "" {
			if answer, ok := extractFAQAnswer(trimmed); ok {
				pendingFAQAnswers = append(pendingFAQAnswers, answer)
				idx++
				continue
			}
			pendingFAQAnswers = append(pendingFAQAnswers, trimListPrefix(trimmed))
			idx++
			continue
		}
		if factParagraph, ok := parseFactParagraph(currentSection, trimmed); ok {
			flush()
			out = append(out, factParagraph)
			idx++
			continue
		}
		buffer = append(buffer, trimmed)
		idx++
	}
	flush()
	flushFAQ()
	return out
}

func newChunk(siteID string, relativePath string, section string, text string, kind string, keywords []string) Chunk {
	section = strings.TrimSpace(section)
	if section == "" {
		section = relativePath
	}
	text = normalizeText(text)
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = ChunkKindNarrative
	}
	keywords = normalizeKeywords(keywords)
	id := uuid.NewSHA1(uuid.NameSpaceURL, []byte(siteID+"\n"+relativePath+"\n"+section+"\n"+kind+"\n"+strings.Join(keywords, "\n")+"\n"+text))
	return Chunk{
		ID:         id.String(),
		Section:    section,
		Text:       text,
		SourcePath: filepath.ToSlash(relativePath),
		Kind:       kind,
		Keywords:   keywords,
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

func parseMarkdownHeading(line string) (int, string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	level := 0
	for level < len(trimmed) && trimmed[level] == '#' {
		level++
	}
	title := strings.TrimSpace(trimmed[level:])
	if level == 0 || title == "" {
		return 0, "", false
	}
	return level, title, true
}

func buildSectionPath(relativePath string, stack []string) string {
	if len(stack) == 0 {
		return relativePath
	}
	return strings.Join(stack, " / ")
}

func parseMarkdownTable(section string, lines []string) ([]paragraph, int) {
	if len(lines) < 2 {
		return nil, 0
	}
	headerLine := strings.TrimSpace(lines[0])
	separatorLine := strings.TrimSpace(lines[1])
	if !isMarkdownTableLine(headerLine) || !isMarkdownTableSeparator(separatorLine) {
		return nil, 0
	}

	headers := splitMarkdownTableRow(headerLine)
	if len(headers) == 0 {
		return nil, 0
	}

	out := make([]paragraph, 0, 4)
	consumed := 2
	for consumed < len(lines) {
		rowLine := strings.TrimSpace(lines[consumed])
		if !isMarkdownTableLine(rowLine) {
			break
		}
		cells := splitMarkdownTableRow(rowLine)
		if len(cells) > 0 {
			out = append(out, buildTableParagraph(section, headers, cells))
		}
		consumed++
	}
	return out, consumed
}

func isMarkdownTableLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "|") && strings.HasSuffix(line, "|") && strings.Count(line, "|") >= 2
}

func isMarkdownTableSeparator(line string) bool {
	if !isMarkdownTableLine(line) {
		return false
	}
	for _, cell := range splitMarkdownTableRow(line) {
		trimmed := strings.TrimSpace(cell)
		trimmed = strings.Trim(trimmed, "-:")
		if trimmed != "" {
			return false
		}
	}
	return true
}

func splitMarkdownTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, normalizeInlineText(part))
	}
	return out
}

func buildTableParagraph(section string, headers []string, cells []string) paragraph {
	size := minInt(len(headers), len(cells))
	if size == 0 {
		return paragraph{Section: section}
	}
	headers = headers[:size]
	cells = cells[:size]

	firstHeader := compactStructuredToken(headers[0])
	if size >= 2 && isGenericFactKeyColumn(firstHeader) && isGenericFactValueColumn(compactStructuredToken(headers[1])) {
		key := normalizeInlineText(cells[0])
		value := normalizeInlineText(cells[1])
		return paragraph{
			Section:  section,
			Text:     key + "：" + value,
			Kind:     ChunkKindFact,
			Keywords: normalizeKeywords([]string{section, key, value}),
		}
	}

	lines := make([]string, 0, size)
	keywords := make([]string, 0, size*2+1)
	keywords = append(keywords, section)
	for idx := 0; idx < size; idx++ {
		header := normalizeInlineText(headers[idx])
		cell := normalizeInlineText(cells[idx])
		if header == "" || cell == "" {
			continue
		}
		lines = append(lines, header+"："+cell)
		keywords = append(keywords, header, cell)
	}
	return paragraph{
		Section:  section,
		Text:     strings.Join(lines, "\n"),
		Kind:     ChunkKindFact,
		Keywords: normalizeKeywords(keywords),
	}
}

func isGenericFactKeyColumn(header string) bool {
	switch header {
	case "项目", "字段", "参数", "指标", "名称", "品类":
		return true
	default:
		return false
	}
}

func isGenericFactValueColumn(header string) bool {
	switch header {
	case "内容", "值", "说明", "信息", "参数", "规格":
		return true
	default:
		return false
	}
}

func parseFactParagraph(section string, line string) (paragraph, bool) {
	text := trimListPrefix(line)
	sep := strings.IndexAny(text, "：:")
	if sep <= 0 || sep >= len(text)-1 {
		return paragraph{}, false
	}
	key := normalizeInlineText(text[:sep])
	value := normalizeInlineText(text[sep+1:])
	if !isLikelyFactKey(key) || value == "" || looksLikeQuestion(key) {
		return paragraph{}, false
	}
	return paragraph{
		Section:  section,
		Text:     key + "：" + value,
		Kind:     ChunkKindFact,
		Keywords: normalizeKeywords([]string{section, key, value}),
	}, true
}

func extractFAQQuestion(text string) (string, bool) {
	trimmed := trimMarkdownDecoration(trimListPrefix(text))
	lower := strings.ToLower(trimmed)
	prefixes := []string{"q:", "q：", "问:", "问：", "问题:", "问题："}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			question := trimMarkdownDecoration(normalizeInlineText(trimmed[len(prefix):]))
			if question == "" {
				return "", false
			}
			return question, true
		}
	}
	return "", false
}

func extractFAQAnswer(text string) (string, bool) {
	trimmed := trimMarkdownDecoration(trimListPrefix(text))
	lower := strings.ToLower(trimmed)
	prefixes := []string{"a:", "a：", "答:", "答：", "答案:", "答案："}
	for _, prefix := range prefixes {
		if strings.HasPrefix(lower, prefix) {
			answer := trimMarkdownDecoration(normalizeInlineText(trimmed[len(prefix):]))
			if answer == "" {
				return "", false
			}
			return answer, true
		}
	}
	return "", false
}

func looksLikeQuestion(text string) bool {
	text = normalizeInlineText(text)
	if text == "" {
		return false
	}
	if strings.ContainsAny(text, "？?") {
		return true
	}
	for _, suffix := range []string{"吗", "么", "呢"} {
		if strings.HasSuffix(text, suffix) {
			return true
		}
	}
	return false
}

func trimListPrefix(text string) string {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"- ", "* ", "• ", "1. ", "2. ", "3. ", "4. ", "5. "} {
		text = strings.TrimPrefix(text, prefix)
	}
	return strings.TrimSpace(text)
}

func isLikelyFactKey(key string) bool {
	key = normalizeInlineText(key)
	if key == "" {
		return false
	}
	if runeCount(key) > 24 {
		return false
	}
	letterCount := 0
	for _, r := range key {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			letterCount++
		case unicode.IsSpace(r):
		case strings.ContainsRune("/-+&()（）·", r):
		default:
			return false
		}
	}
	return letterCount > 0
}

func normalizeInlineText(text string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
}

func trimMarkdownDecoration(text string) string {
	text = normalizeInlineText(text)
	for {
		updated := strings.TrimSpace(text)
		updated = strings.TrimPrefix(updated, "**")
		updated = strings.TrimSuffix(updated, "**")
		updated = strings.TrimPrefix(updated, "__")
		updated = strings.TrimSuffix(updated, "__")
		updated = strings.TrimPrefix(updated, "`")
		updated = strings.TrimSuffix(updated, "`")
		updated = strings.TrimSpace(updated)
		if updated == text {
			return updated
		}
		text = updated
	}
}

func normalizeKeywords(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		normalized := normalizeInlineText(item)
		if normalized == "" {
			continue
		}
		if runeCount(normalized) > 36 {
			normalized = trimLeadingRunes(normalized, 36)
		}
		if runeCount(normalized) < 2 {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeKeywords(left []string, right []string) []string {
	if len(left) == 0 {
		return normalizeKeywords(right)
	}
	if len(right) == 0 {
		return normalizeKeywords(left)
	}
	items := make([]string, 0, len(left)+len(right))
	items = append(items, left...)
	items = append(items, right...)
	return normalizeKeywords(items)
}

func compactStructuredToken(text string) string {
	var builder strings.Builder
	for _, r := range normalizeInlineText(text) {
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

func narrativeSectionKeywords(section string) []string {
	section = normalizeInlineText(section)
	if section == "" {
		return nil
	}
	items := make([]string, 0, 6)
	for _, part := range strings.Split(section, " / ") {
		part = normalizeInlineText(sectionOrderPrefixPattern.ReplaceAllString(strings.TrimSpace(part), ""))
		if part == "" {
			continue
		}
		if strings.HasSuffix(strings.ToLower(part), ".md") || strings.HasSuffix(strings.ToLower(part), ".txt") {
			continue
		}
		items = append(items, part)
	}
	return normalizeKeywords(items)
}
