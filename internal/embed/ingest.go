// Package embed — corpus ingestion: read files, chunk into Entries.
package embed

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// ChunkMode controls how files are split into chunks.
type ChunkMode string

const (
	// ChunkModeHeading splits Markdown files on heading lines (# through ######).
	ChunkModeHeading ChunkMode = "heading"
	// ChunkModeWindow uses a sliding byte window with configurable overlap.
	ChunkModeWindow ChunkMode = "window"
)

// ChunkConfig controls chunking behaviour during ingestion.
type ChunkConfig struct {
	// MaxBytes is the maximum byte size of a single chunk. Default 2000.
	MaxBytes int
	// Overlap is the number of bytes shared between adjacent window chunks.
	// Default 200.
	Overlap int
	// Mode selects the chunking strategy. When empty, heading mode is used
	// for .md/.markdown files and window mode is used for everything else.
	Mode ChunkMode
}

// DefaultChunkConfig returns a ChunkConfig with sensible defaults.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{MaxBytes: 2000, Overlap: 200}
}

// IngestResult is returned by Ingest.
type IngestResult struct {
	// Entries is the ordered set of chunks produced from the corpus. Each
	// Entry's ID is "<relpath>#<n>" (0-indexed). Vec is nil; callers must
	// embed before indexing.
	Entries []Entry
	// FileCount is the number of files that contributed at least one chunk.
	FileCount int
	// ChunkCount mirrors len(Entries).
	ChunkCount int
	// CorpusHash is the hex-encoded SHA-256 over all file contents streamed in
	// glob-sorted order.
	CorpusHash string
}

var headingRe = regexp.MustCompile(`^#{1,6} `)

const maxFileSize = 1 << 20 // 1 MiB

// Ingest reads files matching globs (relative to workingDir), chunks them
// according to cfg, and returns an IngestResult. Entries carry nil Vecs; the
// caller is responsible for embedding.
//
// Glob patterns are joined with workingDir. Paths that escape workingDir via
// ".." are silently skipped. Binary files (null byte in first 512 bytes) and
// files larger than 1 MiB are also skipped. The corpus hash is computed over
// all included files in sorted order regardless of how many chunks they
// produce.
func Ingest(_ context.Context, workingDir string, globs []string, cfg ChunkConfig) (IngestResult, error) {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = 2000
	}
	if cfg.Overlap < 0 {
		cfg.Overlap = 0
	}

	// Collect and deduplicate matched paths.
	seen := map[string]struct{}{}
	var paths []string
	for _, g := range globs {
		pattern := filepath.Join(workingDir, g)
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return IngestResult{}, fmt.Errorf("ingest: glob %q: %w", g, err)
		}
		for _, p := range matches {
			abs, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			if _, dup := seen[abs]; dup {
				continue
			}
			seen[abs] = struct{}{}
			paths = append(paths, abs)
		}
	}
	sort.Strings(paths)

	hash := sha256.New()
	var entries []Entry
	fileCount := 0

	for _, absPath := range paths {
		// Escape check.
		rel, err := filepath.Rel(workingDir, absPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}

		data, err := readFile(absPath)
		if err != nil || data == nil {
			// skip unreadable, too-large, or binary files
			continue
		}

		// Include in corpus hash.
		hash.Write(data)
		fileCount++

		// Choose chunk mode.
		mode := cfg.Mode
		if mode == "" {
			ext := strings.ToLower(filepath.Ext(absPath))
			if ext == ".md" || ext == ".markdown" {
				mode = ChunkModeHeading
			} else {
				mode = ChunkModeWindow
			}
		}

		var chunks []string
		if mode == ChunkModeHeading {
			chunks = chunkByHeading(string(data), cfg.MaxBytes, cfg.Overlap)
		} else {
			chunks = chunkByWindow(string(data), cfg.MaxBytes, cfg.Overlap)
		}

		for n, text := range chunks {
			entries = append(entries, Entry{
				ID:  rel + "#" + strconv.Itoa(n),
				Meta: map[string]any{
					"path":    rel,
					"ordinal": n,
					"text":    text,
				},
				Vec: nil,
			})
		}
	}

	return IngestResult{
		Entries:    entries,
		FileCount:  fileCount,
		ChunkCount: len(entries),
		CorpusHash: hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

// readFile reads the file at path and returns its bytes, or nil if it should
// be skipped (binary, too large, or read error).
func readFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxFileSize {
		return nil, nil // skip oversized
	}

	// Read first 512 bytes to detect binary.
	probe := make([]byte, 512)
	n, err := f.Read(probe)
	if err != nil && err != io.EOF {
		return nil, err
	}
	probe = probe[:n]
	for _, b := range probe {
		if b == 0 {
			return nil, nil // binary
		}
	}

	// Seek back and read full content.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	br := bufio.NewReader(f)
	data, err := io.ReadAll(br)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// chunkByHeading splits markdown content on heading lines. Each heading starts
// a new section. Sections that exceed maxBytes are sub-split with overlap using
// chunkByWindow.
func chunkByHeading(content string, maxBytes, overlap int) []string {
	var sections []string
	var current strings.Builder

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if headingRe.MatchString(line) && current.Len() > 0 {
			sections = append(sections, current.String())
			current.Reset()
		}
		current.WriteString(line)
		current.WriteByte('\n')
	}
	if current.Len() > 0 {
		sections = append(sections, current.String())
	}

	// Sub-split oversized sections.
	var chunks []string
	for _, sec := range sections {
		if len(sec) <= maxBytes {
			chunks = append(chunks, sec)
		} else {
			chunks = append(chunks, chunkByWindow(sec, maxBytes, overlap)...)
		}
	}
	if len(chunks) == 0 {
		return nil
	}
	return chunks
}

// chunkByWindow splits content into overlapping windows of maxBytes bytes.
func chunkByWindow(content string, maxBytes, overlap int) []string {
	if len(content) == 0 {
		return nil
	}
	if maxBytes <= 0 {
		maxBytes = 2000
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= maxBytes {
		overlap = maxBytes / 2
	}

	var chunks []string
	start := 0
	for start < len(content) {
		end := start + maxBytes
		if end > len(content) {
			end = len(content)
		}
		if end < len(content) {
			// Snap backward to avoid splitting a multi-byte rune.
			for end > start && !utf8.RuneStart(content[end]) {
				end--
			}
		}
		chunks = append(chunks, content[start:end])
		if end == len(content) {
			break
		}
		step := maxBytes - overlap
		if step <= 0 {
			step = 1
		}
		start += step
	}
	return chunks
}
