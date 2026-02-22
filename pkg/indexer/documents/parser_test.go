package documents

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestChunkDocumentWithMeta_SingleChunk(t *testing.T) {
	parser := &DocumentParser{chunkSize: 1000}
	content := "Hello world.\n\nThis is a test document."

	chunks := parser.ChunkDocumentWithMeta(content)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}

	c := chunks[0]
	if c.ChunkIndex != 0 {
		t.Errorf("expected ChunkIndex 0, got %d", c.ChunkIndex)
	}
	if c.TextHash == "" {
		t.Error("TextHash should not be empty")
	}
	// Verify hash is correct.
	h := sha256.Sum256([]byte(c.Content))
	expectedHash := hex.EncodeToString(h[:])
	if c.TextHash != expectedHash {
		t.Errorf("TextHash mismatch: %s != %s", c.TextHash, expectedHash)
	}
}

func TestChunkDocumentWithMeta_MultipleChunks(t *testing.T) {
	parser := &DocumentParser{chunkSize: 5} // Very small chunk size to force splitting.

	// Each paragraph has more than 5 words.
	content := "This is the first paragraph with many words.\n\nThis is the second paragraph with enough words.\n\nThird paragraph here with some words."

	chunks := parser.ChunkDocumentWithMeta(content)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks with chunkSize=5, got %d", len(chunks))
	}

	// Verify chunk indices are sequential.
	for i, c := range chunks {
		if c.ChunkIndex != i {
			t.Errorf("chunk %d has ChunkIndex %d", i, c.ChunkIndex)
		}
	}

	// Verify all content is accounted for.
	var allContent []string
	for _, c := range chunks {
		allContent = append(allContent, c.Content)
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
	parser := &DocumentParser{chunkSize: 1000}
	content := "# Main Title\n\nIntro text.\n\n## Section One\n\nSection one content.\n\n## Section Two\n\nSection two content."

	chunks := parser.ChunkDocumentWithMeta(content)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk (large chunkSize), got %d", len(chunks))
	}

	// The heading path should contain the deepest heading encountered.
	if !strings.Contains(chunks[0].HeadingPath, "Main Title") {
		t.Errorf("expected heading path to contain 'Main Title', got %q", chunks[0].HeadingPath)
	}
}

func TestChunkDocumentWithMeta_HeadingSplitAcrossChunks(t *testing.T) {
	parser := &DocumentParser{chunkSize: 5}

	content := "# Title\n\nFirst paragraph with many words enough.\n\n## Subsection\n\nSecond paragraph with many words enough."

	chunks := parser.ChunkDocumentWithMeta(content)
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
	parser := &DocumentParser{chunkSize: 50}
	content := "# Hello\n\nSome content here.\n\n## World\n\nMore content."

	chunks1 := parser.ChunkDocumentWithMeta(content)
	chunks2 := parser.ChunkDocumentWithMeta(content)

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
	parser := &DocumentParser{chunkSize: 100}
	chunks := parser.ChunkDocumentWithMeta("")
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
