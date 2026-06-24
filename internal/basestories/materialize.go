package basestories

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// embedRoot is the directory prefix inside the embed.FS. The //go:embed
// directive embeds `stories`, so every path is rooted at "stories/...".
const embedRoot = "stories"

// Materialize extracts the embedded story library to a content-addressed cache
// directory and returns the absolute path of the materialized library root —
// the directory that contains each base story (so "@kitsoki/dev-story"
// resolves to <root>/dev-story/app.yaml).
//
// The cache dir is ${XDG_CACHE_HOME:-~/.cache}/kitsoki/stories/<contentHash>/.
// Extraction is idempotent: if the dir already exists (a prior run, or a
// concurrent one that finished), it is returned without re-writing. ctx is
// honoured between files so a long extraction can be cancelled.
//
// Returns [ErrNotStaged] when only the .gitkeep placeholder is embedded (the
// library was not staged into the binary). DI-friendly: a pure, stateless
// function with no receiver — callers inject it (or a closure over it) as the
// loader's embedded-fallback resolver.
func Materialize(ctx context.Context) (string, error) {
	sum, files, err := hashTree()
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", ErrNotStaged
	}

	base, err := cacheBaseDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(base, sum)

	// Idempotent: a complete extraction is marked by a `.materialized`
	// sentinel written last. Presence of the sentinel means a prior run
	// finished, so reuse the dir as-is. (Checking the dir alone would race a
	// half-written extraction; the sentinel is the commit point.)
	sentinel := filepath.Join(root, ".materialized")
	if _, statErr := os.Stat(sentinel); statErr == nil {
		return root, nil
	}

	// Extract into a temp sibling dir, then atomically rename into place so a
	// crash mid-extraction never leaves a partial tree that a later run would
	// mistake for complete. If the rename target already exists (a concurrent
	// winner), discard our copy and reuse theirs.
	tmp, err := os.MkdirTemp(base, "."+sum+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("basestories: create temp extract dir: %w", err)
	}
	defer os.RemoveAll(tmp) // no-op once renamed away

	for _, name := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		rel := strings.TrimPrefix(name, embedRoot+"/")
		if rel == name || rel == "" {
			continue // not under the embed root (shouldn't happen)
		}
		dest := filepath.Join(tmp, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return "", fmt.Errorf("basestories: mkdir %q: %w", filepath.Dir(dest), mkErr)
		}
		b, readErr := stories.ReadFile(name)
		if readErr != nil {
			return "", fmt.Errorf("basestories: read embedded %q: %w", name, readErr)
		}
		if wErr := os.WriteFile(dest, b, 0o644); wErr != nil {
			return "", fmt.Errorf("basestories: write %q: %w", dest, wErr)
		}
	}
	if wErr := os.WriteFile(filepath.Join(tmp, ".materialized"), []byte(sum), 0o644); wErr != nil {
		return "", fmt.Errorf("basestories: write sentinel: %w", wErr)
	}

	if renErr := os.Rename(tmp, root); renErr != nil {
		// A concurrent winner already materialized this hash — reuse it.
		if _, statErr := os.Stat(sentinel); statErr == nil {
			return root, nil
		}
		return "", fmt.Errorf("basestories: publish cache dir %q: %w", root, renErr)
	}
	return root, nil
}

// hashTree walks the embedded FS and returns (hex SHA-256 of the content tree,
// sorted list of embedded file paths). The hash mixes each file's path, byte
// length, and bytes in stable lexical order so it is deterministic and
// collision-resistant against renames as well as edits. The .gitkeep
// placeholder is excluded from BOTH the hash and the file list so an un-staged
// binary hashes to the empty set (len(files)==0 → ErrNotStaged) and a staged
// binary's key doesn't depend on the placeholder.
func hashTree() (string, []string, error) {
	var files []string
	err := fs.WalkDir(stories, embedRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Base(path) == ".gitkeep" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("basestories: walk embedded library: %w", err)
	}
	sort.Strings(files)

	h := sha256.New()
	var lenbuf [8]byte
	for _, name := range files {
		b, readErr := stories.ReadFile(name)
		if readErr != nil {
			return "", nil, fmt.Errorf("basestories: read embedded %q: %w", name, readErr)
		}
		// path \0 len(bytes) bytes — length-prefixing prevents boundary
		// ambiguity between adjacent files.
		h.Write([]byte(name))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(b)))
		h.Write(lenbuf[:])
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), files, nil
}

// cacheBaseDir returns ${XDG_CACHE_HOME:-~/.cache}/kitsoki/stories, creating it
// if absent. This is the parent of the per-hash materialized roots.
func cacheBaseDir() (string, error) {
	var base string
	if xdg := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); xdg != "" {
		base = xdg
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("basestories: locate cache dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "kitsoki", "stories")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("basestories: create cache dir %q: %w", dir, err)
	}
	return dir, nil
}
