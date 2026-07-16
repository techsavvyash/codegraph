// Package docs implements in-repo markdown ingestion for RFC-011: document
// discovery (Source), deterministic chunking with hash-based incremental
// sync (Chunker), and graph writes (Ingestor). It was parked as docsparked
// after the RFC-006 Phase 0c demolition and un-parked for RFC-011.
package docs

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// ChunkMeta holds metadata for a document chunk produced by the chunker.
type ChunkMeta struct {
	Content     string // The chunk text
	HeadingPath string // Heading hierarchy, e.g. "Architecture > Components"
	StartOffset int    // Byte offset in original document
	EndOffset   int    // Byte offset end
	ChunkIndex  int    // 0-based index within document
	TextHash    string // SHA-256 hex of Content, used for hash-based chunk-sync
}

// Chunker breaks documents into chunks suitable for incremental (hash-diffed)
// re-indexing: unchanged chunks keep the same TextHash across runs.
type Chunker struct {
	chunkSize int
}

// NewChunker creates a Chunker with the given target chunk size (in words).
func NewChunker(chunkSize int) *Chunker {
	if chunkSize <= 0 {
		chunkSize = 1000
	}
	return &Chunker{chunkSize: chunkSize}
}

var headingRe = regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)

// ChunkDocumentWithMeta breaks a document into chunks with heading paths,
// byte offsets, and text hashes. Chunk boundaries and hashes are deterministic
// across runs so callers can diff TextHash against a prior run to detect
// unchanged chunks (hash chunk-sync).
//
// A chunk is flushed when the word budget is exceeded OR when a paragraph
// opens with an H1/H2 heading (RFC-011 §4): major sections get their own
// chunks, and each chunk's HeadingPath reflects the section it belongs to
// rather than blending across a section boundary.
func (c *Chunker) ChunkDocumentWithMeta(content string) []ChunkMeta {
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
		chunks = append(chunks, ChunkMeta{
			Content:     text,
			HeadingPath: buildHeadingPath(headings),
			StartOffset: chunkStart,
			EndOffset:   chunkStart + len(text),
			ChunkIndex:  chunkIndex,
			TextHash:    textHash(text),
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

		// Section boundary: a paragraph opening with an H1/H2 heading closes
		// the current chunk. This must happen BEFORE the heading stack below
		// is updated, so the outgoing chunk keeps the section path it was
		// written under.
		if lvl := openingHeadingLevel(paragraph); lvl >= 1 && lvl <= 2 && currentText.Len() > 0 {
			flushChunk()
			chunkStart = bytePos
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
		if wordCount+paragraphWords > c.chunkSize && currentText.Len() > 0 {
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

// openingHeadingLevel returns the heading level (1-6) if the paragraph's first
// line is a markdown ATX heading, or 0 otherwise.
func openingHeadingLevel(paragraph string) int {
	firstLine := paragraph
	if idx := strings.IndexByte(paragraph, '\n'); idx >= 0 {
		firstLine = paragraph[:idx]
	}
	m := headingRe.FindStringSubmatch(firstLine)
	if m == nil {
		return 0
	}
	return len(m[1])
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
