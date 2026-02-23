package documents

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/context-maximiser/code-graph/libs/core-models-go"
)

// ChunkMeta holds metadata for a document chunk produced by the parser.
type ChunkMeta struct {
	Content     string // The chunk text
	HeadingPath string // Heading hierarchy, e.g. "Architecture > Components"
	StartOffset int    // Byte offset in original document
	EndOffset   int    // Byte offset end
	ChunkIndex  int    // 0-based index within document
	TextHash    string // SHA-256 hex of Content
}

// DocumentParser handles parsing and feature extraction from documents
type DocumentParser struct {
	chunkSize int
}

// NewDocumentParser creates a new document parser
func NewDocumentParser() *DocumentParser {
	return &DocumentParser{
		chunkSize: 1000, // Default chunk size in words
	}
}

// ParseDocument processes a document file and extracts features
func (dp *DocumentParser) ParseDocument(filePath string) (*models.Document, []*models.Feature, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read document: %w", err)
	}

	// Extract document metadata
	doc := &models.Document{
		Title:     extractTitle(string(content)),
		Type:      inferDocumentType(filePath),
		SourceURL: filePath,
		Content:   string(content),
	}

	// Extract features (section headers) from the document
	features, err := dp.extractFeatures(string(content), filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to extract features: %w", err)
	}

	return doc, features, nil
}

// ChunkDocument breaks a document into smaller, semantically coherent chunks
func (dp *DocumentParser) ChunkDocument(content string) []string {
	// Split by paragraphs first
	paragraphs := strings.Split(content, "\n\n")
	var chunks []string
	var currentChunk strings.Builder
	wordCount := 0

	for _, paragraph := range paragraphs {
		// Clean up the paragraph
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}

		// Count words in this paragraph
		words := strings.Fields(paragraph)
		paragraphWordCount := len(words)

		// If adding this paragraph would exceed chunk size, save current chunk
		if wordCount+paragraphWordCount > dp.chunkSize && currentChunk.Len() > 0 {
			chunks = append(chunks, currentChunk.String())
			currentChunk.Reset()
			wordCount = 0
		}

		// Add paragraph to current chunk
		if currentChunk.Len() > 0 {
			currentChunk.WriteString("\n\n")
		}
		currentChunk.WriteString(paragraph)
		wordCount += paragraphWordCount
	}

	// Add remaining content as final chunk
	if currentChunk.Len() > 0 {
		chunks = append(chunks, currentChunk.String())
	}

	return chunks
}

// ChunkDocumentWithMeta breaks a document into chunks with heading paths, offsets, and text hashes.
func (dp *DocumentParser) ChunkDocumentWithMeta(content string) []ChunkMeta {
	headingRe := regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)

	// Split into paragraphs (double newline separated).
	paragraphs := strings.Split(content, "\n\n")

	var chunks []ChunkMeta
	var currentText strings.Builder
	wordCount := 0
	chunkStart := 0 // byte offset of the start of the current chunk
	bytePos := 0    // running byte offset
	chunkIndex := 0

	// Track heading stack: headings[0] = h1, headings[1] = h2, etc.
	headings := make([]string, 6)

	flushChunk := func() {
		text := currentText.String()
		if strings.TrimSpace(text) == "" {
			return
		}
		h := sha256.Sum256([]byte(text))
		chunks = append(chunks, ChunkMeta{
			Content:     text,
			HeadingPath: buildHeadingPath(headings),
			StartOffset: chunkStart,
			EndOffset:   chunkStart + len(text),
			ChunkIndex:  chunkIndex,
			TextHash:    hex.EncodeToString(h[:]),
		})
		chunkIndex++
		currentText.Reset()
		wordCount = 0
	}

	for i, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			// Account for the double-newline separator.
			if i < len(paragraphs)-1 {
				bytePos += 2
			}
			continue
		}

		// Check if this paragraph is/contains a heading and update the stack.
		for _, line := range strings.Split(paragraph, "\n") {
			if m := headingRe.FindStringSubmatch(line); m != nil {
				level := len(m[1]) - 1 // 0-indexed
				headings[level] = strings.TrimSpace(m[2])
				// Clear deeper headings.
				for j := level + 1; j < 6; j++ {
					headings[j] = ""
				}
			}
		}

		paragraphWords := len(strings.Fields(paragraph))

		// If adding this paragraph would exceed chunk size, flush.
		if wordCount+paragraphWords > dp.chunkSize && currentText.Len() > 0 {
			flushChunk()
			chunkStart = bytePos
		}

		if currentText.Len() > 0 {
			currentText.WriteString("\n\n")
		}
		currentText.WriteString(paragraph)
		wordCount += paragraphWords

		// Advance byte position past the paragraph + separator.
		bytePos += len(paragraph)
		if i < len(paragraphs)-1 {
			bytePos += 2 // the \n\n separator
		}
	}

	flushChunk()
	return chunks
}

// buildHeadingPath joins non-empty heading levels with " > ".
func buildHeadingPath(headings []string) string {
	var parts []string
	for _, h := range headings {
		if h != "" {
			parts = append(parts, h)
		}
	}
	return strings.Join(parts, " > ")
}

// textHash returns the SHA-256 hex string of s.
func textHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// extractFeatures extracts features from a document by identifying section headers
// and named "Feature: X" / "Requirement: X" patterns with optional "- Status: Y" lines.
func (dp *DocumentParser) extractFeatures(content, filePath string) ([]*models.Feature, error) {
	var allFeatures []*models.Feature
	docType := strings.ToLower(inferDocumentType(filePath))

	// 1. Heading-based features from markdown headers (h1-h3).
	chunks := dp.ChunkDocument(content)
	headerPattern := regexp.MustCompile(`(?m)^#{1,3}\s+(.+)$`)
	for _, chunk := range chunks {
		for _, match := range headerPattern.FindAllStringSubmatch(chunk, -1) {
			if len(match) < 2 {
				continue
			}
			headerText := strings.TrimSpace(match[1])
			if isGenericHeader(headerText) {
				continue
			}
			allFeatures = append(allFeatures, &models.Feature{
				Name:        headerText,
				Description: fmt.Sprintf("Section: %s", headerText),
				Status:      "documented",
				Priority:    "medium",
				Tags:        []string{"section", docType},
			})
		}
	}

	// 2. Named features: lines like "Feature: X" or "Requirement: X" with optional
	// "- Status: Y" in the following lines.
	namedFeaturePattern := regexp.MustCompile(`(?i)^(?:Feature|Requirement|Capability|User Story):\s+(.+)$`)
	statusPattern := regexp.MustCompile(`(?i)-\s+Status:\s+(.+)$`)
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		match := namedFeaturePattern.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		name := strings.TrimSpace(match[1])
		status := "documented"
		// Scan the next lines (up to 10) for a "- Status:" entry.
		for j := i + 1; j < len(lines) && j < i+10; j++ {
			nextLine := strings.TrimSpace(lines[j])
			if statusMatch := statusPattern.FindStringSubmatch(nextLine); statusMatch != nil {
				status = strings.ToLower(strings.TrimSpace(statusMatch[1]))
				break
			}
			// Stop scanning if we hit the next named feature block.
			if namedFeaturePattern.MatchString(nextLine) {
				break
			}
		}
		allFeatures = append(allFeatures, &models.Feature{
			Name:        name,
			Description: fmt.Sprintf("Feature: %s", name),
			Status:      status,
			Priority:    "medium",
			Tags:        []string{"feature", docType},
		})
	}

	return dp.deduplicateFeatures(allFeatures), nil
}

// deduplicateFeatures removes similar features and merges them
func (dp *DocumentParser) deduplicateFeatures(features []*models.Feature) []*models.Feature {
	seen := make(map[string]*models.Feature)
	var result []*models.Feature

	for _, feature := range features {
		// Create a normalized key for deduplication
		normalizedName := strings.ToLower(strings.TrimSpace(feature.Name))
		normalizedName = regexp.MustCompile(`\s+`).ReplaceAllString(normalizedName, " ")

		if existing, exists := seen[normalizedName]; exists {
			// Merge with existing feature
			if len(feature.Description) > len(existing.Description) {
				existing.Description = feature.Description
			}
			// Merge tags
			existing.Tags = append(existing.Tags, feature.Tags...)
			existing.Tags = removeDuplicateStrings(existing.Tags)
		} else {
			seen[normalizedName] = feature
			result = append(result, feature)
		}
	}

	return result
}

// Helper functions

func extractTitle(content string) string {
	// Try to find title from markdown header
	titlePattern := regexp.MustCompile(`(?m)^#\s+(.+)$`)
	matches := titlePattern.FindStringSubmatch(content)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}

	// Try to find title from first line
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && len(line) > 5 && len(line) < 100 {
			// Remove markdown formatting
			line = regexp.MustCompile(`[#*_`+"`"+`]`).ReplaceAllString(line, "")
			return strings.TrimSpace(line)
		}
	}

	return "Untitled Document"
}

func inferDocumentType(filePath string) string {
	filename := strings.ToLower(filepath.Base(filePath))
	ext := filepath.Ext(filename)

	switch ext {
	case ".md":
		if strings.Contains(filename, "readme") {
			return "README"
		}
		if strings.Contains(filename, "rfc") {
			return "RFC"
		}
		if strings.Contains(filename, "spec") {
			return "Specification"
		}
		if strings.Contains(filename, "arch") {
			return "Architecture"
		}
		return "Markdown Document"
	case ".txt":
		return "Text Document"
	case ".rst":
		return "reStructuredText"
	default:
		return "Document"
	}
}


func isGenericHeader(header string) bool {
	genericHeaders := []string{
		"introduction", "overview", "conclusion", "summary",
		"table of contents", "contents", "index", "references",
		"appendix", "notes", "todo", "changelog",
	}
	
	lowerHeader := strings.ToLower(header)
	for _, generic := range genericHeaders {
		if strings.Contains(lowerHeader, generic) {
			return true
		}
	}
	
	// Skip very short or very long headers
	return len(header) < 3 || len(header) > 80
}

func removeDuplicateStrings(slice []string) []string {
	seen := make(map[string]bool)
	var result []string
	
	for _, str := range slice {
		if !seen[str] {
			seen[str] = true
			result = append(result, str)
		}
	}
	
	return result
}

// ExtractedData represents the structured output from document parsing
type ExtractedData struct {
	Document *models.Document  `json:"document"`
	Features []*models.Feature `json:"features"`
	Symbols  []string          `json:"symbols,omitempty"` // References to code symbols
}

// ParseToJSON parses a document and returns JSON-formatted extracted data
func (dp *DocumentParser) ParseToJSON(filePath string) ([]byte, error) {
	doc, features, err := dp.ParseDocument(filePath)
	if err != nil {
		return nil, err
	}

	extracted := ExtractedData{
		Document: doc,
		Features: features,
		Symbols:  extractCodeSymbols(doc.Content),
	}

	return json.MarshalIndent(extracted, "", "  ")
}

// extractCodeSymbols finds references to code symbols in the document
func extractCodeSymbols(content string) []string {
	var symbols []string
	
	// Pattern for code references in backticks
	codePattern := regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_]*(?:\\.[A-Za-z_][A-Za-z0-9_]*)*(?:\\(\\))?)`")
	matches := codePattern.FindAllStringSubmatch(content, -1)
	
	for _, match := range matches {
		if len(match) > 1 {
			symbol := match[1]
			// Filter out common words that aren't likely to be code symbols
			if isLikelyCodeSymbol(symbol) {
				symbols = append(symbols, symbol)
			}
		}
	}
	
	return removeDuplicateStrings(symbols)
}

func isLikelyCodeSymbol(symbol string) bool {
	// Filter out common English words
	commonWords := []string{
		"the", "and", "or", "but", "if", "then", "else", "when", "where",
		"what", "how", "why", "who", "which", "that", "this", "these", "those",
		"can", "will", "would", "should", "could", "may", "might", "must",
		"is", "are", "was", "were", "be", "been", "being", "have", "has", "had",
		"do", "does", "did", "get", "got", "set", "put", "let", "make", "take",
	}
	
	lowerSymbol := strings.ToLower(symbol)
	for _, word := range commonWords {
		if lowerSymbol == word {
			return false
		}
	}
	
	// Must contain at least one capital letter or underscore (typical code patterns)
	return regexp.MustCompile(`[A-Z_]`).MatchString(symbol)
}