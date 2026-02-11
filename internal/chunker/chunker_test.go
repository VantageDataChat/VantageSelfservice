package chunker

import (
	"strings"
	"testing"
)

func TestNewTextChunker(t *testing.T) {
	tc := NewTextChunker()
	if tc.ChunkSize != DefaultChunkSize {
		t.Errorf("expected ChunkSize %d, got %d", DefaultChunkSize, tc.ChunkSize)
	}
	if tc.Overlap != DefaultOverlap {
		t.Errorf("expected Overlap %d, got %d", DefaultOverlap, tc.Overlap)
	}
}

func TestSplit_EmptyText(t *testing.T) {
	tc := &TextChunker{ChunkSize: 10, Overlap: 3}
	chunks := tc.Split("", "doc1")
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty text, got %d", len(chunks))
	}
}

func TestSplit_TextShorterThanChunkSize(t *testing.T) {
	tc := &TextChunker{ChunkSize: 100, Overlap: 20}
	chunks := tc.Split("hello", "doc1")
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "hello" {
		t.Errorf("expected chunk text 'hello', got %q", chunks[0].Text)
	}
	if chunks[0].Index != 0 {
		t.Errorf("expected index 0, got %d", chunks[0].Index)
	}
	if chunks[0].DocumentID != "doc1" {
		t.Errorf("expected documentID 'doc1', got %q", chunks[0].DocumentID)
	}
}

func TestSplit_TextEqualToChunkSize(t *testing.T) {
	tc := &TextChunker{ChunkSize: 5, Overlap: 2}
	chunks := tc.Split("abcde", "doc1")
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].Text != "abcde" {
		t.Errorf("expected 'abcde', got %q", chunks[0].Text)
	}
}

func TestSplit_BasicChunking(t *testing.T) {
	tc := &TextChunker{ChunkSize: 5, Overlap: 2}
	// "abcdefghij" (10 chars), step = 5-2 = 3
	// chunk 0: [0:5] = "abcde"
	// chunk 1: [3:8] = "defgh"
	// chunk 2: [6:10] = "ghij"
	chunks := tc.Split("abcdefghij", "doc1")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	expected := []string{"abcde", "defgh", "ghij"}
	for i, exp := range expected {
		if chunks[i].Text != exp {
			t.Errorf("chunk %d: expected %q, got %q", i, exp, chunks[i].Text)
		}
		if chunks[i].Index != i {
			t.Errorf("chunk %d: expected index %d, got %d", i, i, chunks[i].Index)
		}
		if chunks[i].DocumentID != "doc1" {
			t.Errorf("chunk %d: expected documentID 'doc1', got %q", i, chunks[i].DocumentID)
		}
	}
}

func TestSplit_OverlapCorrectness(t *testing.T) {
	tc := &TextChunker{ChunkSize: 6, Overlap: 2}
	// "abcdefghijklmn" (14 chars), step = 6-2 = 4
	// chunk 0: [0:6]  = "abcdef"
	// chunk 1: [4:10] = "efghij"
	// chunk 2: [8:14] = "ijklmn"
	chunks := tc.Split("abcdefghijklmn", "doc2")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	// Verify overlap between adjacent chunks
	for i := 0; i < len(chunks)-1; i++ {
		curr := chunks[i].Text
		next := chunks[i+1].Text
		overlapText := curr[len(curr)-tc.Overlap:]
		nextPrefix := next[:tc.Overlap]
		if overlapText != nextPrefix {
			t.Errorf("overlap mismatch between chunk %d and %d: %q vs %q",
				i, i+1, overlapText, nextPrefix)
		}
	}
}

func TestSplit_ZeroOverlap(t *testing.T) {
	tc := &TextChunker{ChunkSize: 3, Overlap: 0}
	// "abcdefg" (7 chars), step = 3
	// chunk 0: [0:3] = "abc"
	// chunk 1: [3:6] = "def"
	// chunk 2: [6:7] = "g"
	chunks := tc.Split("abcdefg", "doc1")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	expected := []string{"abc", "def", "g"}
	for i, exp := range expected {
		if chunks[i].Text != exp {
			t.Errorf("chunk %d: expected %q, got %q", i, exp, chunks[i].Text)
		}
	}
}

func TestSplit_LastChunkShorter(t *testing.T) {
	tc := &TextChunker{ChunkSize: 4, Overlap: 1}
	// "abcdefgh" (8 chars), step = 4-1 = 3
	// chunk 0: [0:4] = "abcd"
	// chunk 1: [3:7] = "defg"
	// chunk 2: [6:8] = "gh"
	chunks := tc.Split("abcdefgh", "doc1")
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	lastChunk := chunks[len(chunks)-1]
	if len(lastChunk.Text) >= tc.ChunkSize {
		t.Errorf("last chunk should be shorter than ChunkSize, got len=%d", len(lastChunk.Text))
	}
	if lastChunk.Text != "gh" {
		t.Errorf("expected last chunk 'gh', got %q", lastChunk.Text)
	}
}

func TestSplit_IncrementingIndex(t *testing.T) {
	tc := &TextChunker{ChunkSize: 3, Overlap: 1}
	chunks := tc.Split("abcdefghij", "doc1")
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d: expected index %d, got %d", i, i, c.Index)
		}
	}
}

func TestSplit_DocumentIDPropagated(t *testing.T) {
	tc := &TextChunker{ChunkSize: 5, Overlap: 0}
	docID := "test-doc-123"
	chunks := tc.Split("some text here", docID)
	for i, c := range chunks {
		if c.DocumentID != docID {
			t.Errorf("chunk %d: expected documentID %q, got %q", i, docID, c.DocumentID)
		}
	}
}

func TestSplit_DefaultsForInvalidChunkSize(t *testing.T) {
	tc := &TextChunker{ChunkSize: 0, Overlap: 10}
	chunks := tc.Split("hello world", "doc1")
	// Should fall back to DefaultChunkSize (512), so entire text fits in one chunk
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk with default chunk size, got %d", len(chunks))
	}
	if chunks[0].Text != "hello world" {
		t.Errorf("expected full text in single chunk, got %q", chunks[0].Text)
	}
}

func TestSplit_OverlapClampedToChunkSizeMinusOne(t *testing.T) {
	tc := &TextChunker{ChunkSize: 5, Overlap: 5}
	// Overlap >= ChunkSize should be clamped to ChunkSize-1 = 4, step = 1
	chunks := tc.Split("abcdefgh", "doc1")
	// With step=1: chunks at 0,1,2,3 (stops when end reaches len)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// Each chunk except the last should be exactly ChunkSize
	for i := 0; i < len(chunks)-1; i++ {
		if len(chunks[i].Text) != 5 {
			t.Errorf("chunk %d: expected length 5, got %d", i, len(chunks[i].Text))
		}
	}
}

func TestSplit_LargeText(t *testing.T) {
	tc := NewTextChunker() // 512 chunk, 128 overlap
	text := strings.Repeat("a", 2000)
	chunks := tc.Split(text, "doc-large")

	// Verify all text is covered
	step := tc.ChunkSize - tc.Overlap
	expectedChunks := 1
	pos := tc.ChunkSize
	for pos < len(text) {
		expectedChunks++
		pos += step
	}
	if len(chunks) != expectedChunks {
		t.Errorf("expected %d chunks, got %d", expectedChunks, len(chunks))
	}

	// Verify first chunk starts at beginning
	if chunks[0].Text[:10] != "aaaaaaaaaa" {
		t.Errorf("first chunk should start with the beginning of text")
	}
}
