package embed

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestIngestMarkdownByHeading(t *testing.T) {
	dir := t.TempDir()
	content := `## Section One
Some text under section one.

## Section Two
More text here in section two.

## Section Three
Final section content.
`
	if err := os.WriteFile(filepath.Join(dir, "doc.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ChunkConfig{MaxBytes: 2000, Overlap: 200, Mode: ChunkModeHeading}
	res, err := Ingest(context.Background(), dir, []string{"*.md"}, cfg)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if res.FileCount != 1 {
		t.Errorf("FileCount = %d, want 1", res.FileCount)
	}
	if res.ChunkCount != 3 {
		t.Errorf("ChunkCount = %d, want 3", res.ChunkCount)
	}
	for i, want := range []string{"doc.md#0", "doc.md#1", "doc.md#2"} {
		if i >= len(res.Entries) {
			t.Fatalf("entry %d missing", i)
		}
		if res.Entries[i].ID != want {
			t.Errorf("entry[%d].ID = %q, want %q", i, res.Entries[i].ID, want)
		}
	}
	// Each chunk text should contain the heading.
	for i, heading := range []string{"Section One", "Section Two", "Section Three"} {
		text, _ := res.Entries[i].Meta["text"].(string)
		if !strings.Contains(text, heading) {
			t.Errorf("entry[%d] text %q does not contain heading %q", i, text, heading)
		}
	}
}

func TestIngestWindowChunks(t *testing.T) {
	dir := t.TempDir()
	maxBytes := 500
	// Build content that is 3 * maxBytes long
	word := "word "
	var sb strings.Builder
	for sb.Len() < 3*maxBytes {
		sb.WriteString(word)
	}
	data := sb.String()

	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ChunkConfig{MaxBytes: maxBytes, Overlap: 50, Mode: ChunkModeWindow}
	res, err := Ingest(context.Background(), dir, []string{"*.txt"}, cfg)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if res.ChunkCount < 3 {
		t.Errorf("ChunkCount = %d, want >= 3", res.ChunkCount)
	}
	// Verify overlap: chunk[1] begins within the tail of chunk[0]
	if len(res.Entries) >= 2 {
		c0text, _ := res.Entries[0].Meta["text"].(string)
		c1text, _ := res.Entries[1].Meta["text"].(string)
		// The start of chunk[1] should match the end of chunk[0] for 'overlap' bytes.
		overlap := 50
		if len(c0text) > overlap && len(c1text) > overlap {
			tail := c0text[len(c0text)-overlap:]
			head := c1text[:overlap]
			if tail != head {
				t.Errorf("expected overlap of %d bytes between chunk 0 and 1; tail=%q head=%q", overlap, tail, head)
			}
		}
	}
}

func TestIngestBinarySkip(t *testing.T) {
	dir := t.TempDir()
	// File containing a null byte — should be detected as binary.
	data := []byte("hello\x00world")
	if err := os.WriteFile(filepath.Join(dir, "bin.bin"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultChunkConfig()
	res, err := Ingest(context.Background(), dir, []string{"*.bin"}, cfg)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if res.ChunkCount != 0 {
		t.Errorf("ChunkCount = %d, want 0 (binary should be skipped)", res.ChunkCount)
	}
	if res.FileCount != 0 {
		t.Errorf("FileCount = %d, want 0", res.FileCount)
	}
}

func TestIngestCorpusHash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultChunkConfig()
	r1, err := Ingest(context.Background(), dir, []string{"*.txt"}, cfg)
	if err != nil {
		t.Fatalf("first Ingest error: %v", err)
	}
	r2, err := Ingest(context.Background(), dir, []string{"*.txt"}, cfg)
	if err != nil {
		t.Fatalf("second Ingest error: %v", err)
	}
	if r1.CorpusHash != r2.CorpusHash {
		t.Errorf("same corpus produced different hashes: %q vs %q", r1.CorpusHash, r2.CorpusHash)
	}

	// Modify the file and verify hash changes.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("helloX"), 0o644); err != nil {
		t.Fatal(err)
	}
	r3, err := Ingest(context.Background(), dir, []string{"*.txt"}, cfg)
	if err != nil {
		t.Fatalf("third Ingest error: %v", err)
	}
	if r1.CorpusHash == r3.CorpusHash {
		t.Error("modified corpus produced same hash — expected different")
	}
}

func TestIngestWindowChunksUnicode(t *testing.T) {
	dir := t.TempDir()
	// Each Greek letter is 2 bytes; 200 repetitions of "αβγδ" = 800 runes = 1600 bytes.
	data := strings.Repeat("αβγδ", 200)
	if err := os.WriteFile(filepath.Join(dir, "unicode.txt"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := ChunkConfig{MaxBytes: 100, Overlap: 0, Mode: ChunkModeWindow}
	res, err := Ingest(context.Background(), dir, []string{"*.txt"}, cfg)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	if res.ChunkCount <= 1 {
		t.Errorf("ChunkCount = %d, want > 1 (chunking must have occurred)", res.ChunkCount)
	}
	for i, e := range res.Entries {
		text, _ := e.Meta["text"].(string)
		if !utf8.ValidString(text) {
			t.Errorf("entry[%d] chunk is not valid UTF-8", i)
		}
	}
}

func TestIngestEscapeCheck(t *testing.T) {
	// Create a parent dir and a working dir inside it.
	parent := t.TempDir()
	workDir := filepath.Join(parent, "work")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a file in the parent, above the working dir.
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A glob of "../*.txt" won't match via filepath.Glob when joined with
	// workDir because filepath.Glob resolves it to the parent. Either it
	// matches and the escape check filters it, or it doesn't match at all.
	// Either way we must not panic and the result should have 0 entries from
	// the escaped path.
	cfg := DefaultChunkConfig()
	res, err := Ingest(context.Background(), workDir, []string{"../*.txt"}, cfg)
	if err != nil {
		t.Fatalf("Ingest error: %v", err)
	}
	// All escaped entries should be filtered.
	for _, e := range res.Entries {
		path, _ := e.Meta["path"].(string)
		if strings.HasPrefix(path, "..") {
			t.Errorf("escaped path %q leaked into result", path)
		}
	}
}
