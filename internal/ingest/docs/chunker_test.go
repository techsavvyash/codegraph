package docs

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestChunkDocumentWithMeta_SingleChunk(t *testing.T) {
	c := NewChunker(1000)
	content := "Hello world.\n\nThis is a test document."

	chunks := c.ChunkDocumentWithMeta(content)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	chunk := chunks[0]
	if chunk.ChunkIndex != 0 {
		t.Errorf("expected ChunkIndex 0, got %d", chunk.ChunkIndex)
	}
	if chunk.TextHash == "" {
		t.Error("TextHash should not be empty")
	}
	// Verify hash is correct.
	h := sha256.Sum256([]byte(chunk.Content))
	expectedHash := hex.EncodeToString(h[:])
	if chunk.TextHash != expectedHash {
		t.Errorf("TextHash mismatch: %s != %s", chunk.TextHash, expectedHash)
	}
}

func TestChunkDocumentWithMeta_MultipleChunks(t *testing.T) {
	c := NewChunker(5) // Very small chunk size to force splitting.

	// Each paragraph has more than 5 words.
	content := "This is the first paragraph with many words.\n\nThis is the second paragraph with enough words.\n\nThird paragraph here with some words."

	chunks := c.ChunkDocumentWithMeta(content)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks with chunkSize=5, got %d", len(chunks))
	}

	// Verify chunk indices are sequential.
	for i, chunk := range chunks {
		if chunk.ChunkIndex != i {
			t.Errorf("chunk %d has ChunkIndex %d", i, chunk.ChunkIndex)
		}
	}

	// Verify all content is accounted for.
	var allContent []string
	for _, chunk := range chunks {
		allContent = append(allContent, chunk.Content)
	}
	joined := strings.Join(allContent, "\n\n")
	if !strings.Contains(joined, "first paragraph") {
		t.Error("missing first paragraph in chunks")
	}
	if !strings.Contains(joined, "Third paragraph") {
		t.Error("missing third paragraph in chunks")
	}
}

func TestChunkDocumentWithMeta_HeadingTracking(t *testing.T) {
	c := NewChunker(1000)
	content := "# Main Title\n\nIntro text.\n\n## Section One\n\nSection one content.\n\n## Section Two\n\nSection two content."

	// H1/H2 paragraphs open a new chunk (RFC-011 §4), so this document yields
	// one chunk per section despite the large word budget.
	chunks := c.ChunkDocumentWithMeta(content)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 section-aligned chunks, got %d", len(chunks))
	}

	if !strings.Contains(chunks[0].HeadingPath, "Main Title") {
		t.Errorf("expected first heading path to contain 'Main Title', got %q", chunks[0].HeadingPath)
	}
	if chunks[1].HeadingPath != "Main Title > Section One" {
		t.Errorf("expected 'Main Title > Section One', got %q", chunks[1].HeadingPath)
	}
	if chunks[2].HeadingPath != "Main Title > Section Two" {
		t.Errorf("expected 'Main Title > Section Two', got %q", chunks[2].HeadingPath)
	}
	if !strings.Contains(chunks[2].Content, "Section two content.") {
		t.Errorf("section chunk should carry its body, got %q", chunks[2].Content)
	}
}

// TestChunkDocumentWithMeta_SectionFlushKeepsOutgoingPath pins the ordering
// contract: the chunk being closed by a new H2 keeps the OLD section's path.
func TestChunkDocumentWithMeta_SectionFlushKeepsOutgoingPath(t *testing.T) {
	c := NewChunker(1000)
	content := "## Alpha\n\nAlpha body.\n\n## Beta\n\nBeta body."

	chunks := c.ChunkDocumentWithMeta(content)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if chunks[0].HeadingPath != "Alpha" {
		t.Errorf("outgoing chunk path should be 'Alpha', got %q", chunks[0].HeadingPath)
	}
	if !strings.Contains(chunks[0].Content, "Alpha body.") || strings.Contains(chunks[0].Content, "Beta") {
		t.Errorf("outgoing chunk content wrong: %q", chunks[0].Content)
	}
	if chunks[1].HeadingPath != "Beta" {
		t.Errorf("incoming chunk path should be 'Beta', got %q", chunks[1].HeadingPath)
	}
}

// TestOpeningHeadingLevel covers the boundary-detection helper directly.
func TestOpeningHeadingLevel(t *testing.T) {
	cases := []struct {
		para string
		want int
	}{
		{"# Title", 1},
		{"## Section\nbody line", 2},
		{"### Deep", 3},
		{"plain paragraph", 0},
		{"body first\n## heading not first", 0},
		{"#nospace", 0},
	}
	for _, tc := range cases {
		if got := openingHeadingLevel(tc.para); got != tc.want {
			t.Errorf("openingHeadingLevel(%q) = %d, want %d", tc.para, got, tc.want)
		}
	}
}

func TestChunkDocumentWithMeta_HeadingSplitAcrossChunks(t *testing.T) {
	c := NewChunker(5)

	content := "# Title\n\nFirst paragraph with many words enough.\n\n## Subsection\n\nSecond paragraph with many words enough."

	chunks := c.ChunkDocumentWithMeta(content)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	// First chunk should have "Title" in heading path.
	if !strings.Contains(chunks[0].HeadingPath, "Title") {
		t.Errorf("first chunk heading path should contain 'Title', got %q", chunks[0].HeadingPath)
	}

	// Later chunks should pick up deeper headings.
	lastChunk := chunks[len(chunks)-1]
	if !strings.Contains(lastChunk.HeadingPath, "Subsection") {
		t.Errorf("last chunk heading path should contain 'Subsection', got %q", lastChunk.HeadingPath)
	}
}

func TestChunkDocumentWithMeta_Determinism(t *testing.T) {
	c := NewChunker(50)
	content := "# Hello\n\nSome content here.\n\n## World\n\nMore content."

	chunks1 := c.ChunkDocumentWithMeta(content)
	chunks2 := c.ChunkDocumentWithMeta(content)

	if len(chunks1) != len(chunks2) {
		t.Fatalf("non-deterministic chunk count: %d vs %d", len(chunks1), len(chunks2))
	}

	for i := range chunks1 {
		if chunks1[i].TextHash != chunks2[i].TextHash {
			t.Errorf("chunk %d has different hash on re-run", i)
		}
		if chunks1[i].HeadingPath != chunks2[i].HeadingPath {
			t.Errorf("chunk %d has different heading path on re-run", i)
		}
	}
}

func TestChunkDocumentWithMeta_EmptyDocument(t *testing.T) {
	c := NewChunker(100)
	chunks := c.ChunkDocumentWithMeta("")
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty document, got %d", len(chunks))
	}
}

func TestBuildHeadingPath(t *testing.T) {
	headings := []string{"Title", "Section", "", "", "", ""}
	path := buildHeadingPath(headings)
	if path != "Title > Section" {
		t.Errorf("expected 'Title > Section', got %q", path)
	}

	headings = []string{"", "", "", "", "", ""}
	path = buildHeadingPath(headings)
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}
