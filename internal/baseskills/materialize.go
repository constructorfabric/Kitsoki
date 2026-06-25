package baseskills

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
// directive embeds `assets`, so every path is rooted at "assets/...".
const embedRoot = "assets"

// Materialize extracts the embedded agent toolkit to a content-addressed cache
// directory and returns the absolute path of the materialized root — the
// directory that contains the `skills/` and `agents/` source trees.
//
// The cache dir is ${XDG_CACHE_HOME:-~/.cache}/kitsoki/skills/<contentHash>/.
// Extraction is idempotent: a complete run is marked by a `.materialized`
// sentinel written last, and a present sentinel means the dir is reused as-is.
// Extraction goes to a temp sibling then atomically renames into place so a
// crash never leaves a partial tree a later run mistakes for complete.
//
// Returns [ErrNotStaged] when only the .gitkeep placeholder is embedded.
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

	sentinel := filepath.Join(root, ".materialized")
	if _, statErr := os.Stat(sentinel); statErr == nil {
		return root, nil
	}

	tmp, err := os.MkdirTemp(base, "."+sum+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("baseskills: create temp extract dir: %w", err)
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
			return "", fmt.Errorf("baseskills: mkdir %q: %w", filepath.Dir(dest), mkErr)
		}
		b, readErr := assets.ReadFile(name)
		if readErr != nil {
			return "", fmt.Errorf("baseskills: read embedded %q: %w", name, readErr)
		}
		if wErr := os.WriteFile(dest, b, 0o644); wErr != nil {
			return "", fmt.Errorf("baseskills: write %q: %w", dest, wErr)
		}
	}
	if wErr := os.WriteFile(filepath.Join(tmp, ".materialized"), []byte(sum), 0o644); wErr != nil {
		return "", fmt.Errorf("baseskills: write sentinel: %w", wErr)
	}

	if renErr := os.Rename(tmp, root); renErr != nil {
		if _, statErr := os.Stat(sentinel); statErr == nil {
			return root, nil // concurrent winner
		}
		return "", fmt.Errorf("baseskills: publish cache dir %q: %w", root, renErr)
	}
	return root, nil
}

// hashTree walks the embedded FS and returns (hex SHA-256 of the content tree,
// sorted list of embedded file paths), mixing each file's path, length, and
// bytes in stable lexical order. The .gitkeep placeholder is excluded from
// BOTH the hash and the list so an un-staged binary hashes to the empty set.
func hashTree() (string, []string, error) {
	var files []string
	err := fs.WalkDir(assets, embedRoot, func(path string, d fs.DirEntry, walkErr error) error {
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
		return "", nil, fmt.Errorf("baseskills: walk embedded toolkit: %w", err)
	}
	sort.Strings(files)

	h := sha256.New()
	var lenbuf [8]byte
	for _, name := range files {
		b, readErr := assets.ReadFile(name)
		if readErr != nil {
			return "", nil, fmt.Errorf("baseskills: read embedded %q: %w", name, readErr)
		}
		h.Write([]byte(name))
		h.Write([]byte{0})
		binary.BigEndian.PutUint64(lenbuf[:], uint64(len(b)))
		h.Write(lenbuf[:])
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), files, nil
}

// cacheBaseDir returns ${XDG_CACHE_HOME:-~/.cache}/kitsoki/skills, creating it
// if absent. This is the parent of the per-hash materialized roots.
func cacheBaseDir() (string, error) {
	var base string
	if xdg := strings.TrimSpace(os.Getenv("XDG_CACHE_HOME")); xdg != "" {
		base = xdg
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("baseskills: locate cache dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	dir := filepath.Join(base, "kitsoki", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("baseskills: create cache dir %q: %w", dir, err)
	}
	return dir, nil
}
