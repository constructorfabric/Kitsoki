package app

// loadfiles.go reconstructs an AppDef from an in-memory file set — the story
// source embedded in a trace (see internal/store/story.go). It materialises
// the files to a temp dir laid out exactly as they were on disk (paths are
// relative to a shared capture root, preserving `import: ../sibling` layouts)
// and re-runs the ordinary Load. Because Load roots BaseDir at the entry
// manifest's directory, views/prompts/scripts resolve through the temp tree
// with no special-casing — the reconstructed machine behaves byte-for-byte as
// the live one did, even when the on-disk story has since changed or vanished.

import (
	"fmt"
	"os"
	"path/filepath"
)

// LoadFromFiles materialises files (keyed by capture-root-relative, forward-
// slash paths → raw bytes) under a fresh temp directory and loads the story
// rooted at entry (also capture-root-relative). It returns the loaded AppDef
// and a cleanup func that removes the temp directory.
//
// The temp directory must outlive all use of the returned AppDef: Load reads
// view/prompt templates lazily via BaseDir, so the renderer touches the temp
// tree on every render. Call cleanup only once the AppDef (and any machine
// built from it) is done. cleanup is always non-nil and safe to call even when
// LoadFromFiles returns an error.
func LoadFromFiles(files map[string][]byte, entry string) (*AppDef, func(), error) {
	dir, err := os.MkdirTemp("", "kitsoki-story-*")
	if err != nil {
		return nil, func() {}, fmt.Errorf("app.LoadFromFiles: mkdir temp: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	for rel, b := range files {
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("app.LoadFromFiles: mkdir %q: %w", dest, mkErr)
		}
		if wErr := os.WriteFile(dest, b, 0o644); wErr != nil {
			cleanup()
			return nil, func() {}, fmt.Errorf("app.LoadFromFiles: write %q: %w", dest, wErr)
		}
	}

	entryPath := filepath.Join(dir, filepath.FromSlash(entry))
	// Publish KITSOKI_APP_DIR so manifests using `${KITSOKI_APP_DIR}` resolve
	// against the materialised tree, matching the live loadAppWithEnv path in
	// cmd/kitsoki. The loader reads it during Load.
	if absEntryDir, absErr := filepath.Abs(filepath.Dir(entryPath)); absErr == nil {
		_ = os.Setenv("KITSOKI_APP_DIR", absEntryDir)
	}

	def, err := Load(entryPath)
	if err != nil {
		cleanup()
		return nil, func() {}, fmt.Errorf("app.LoadFromFiles: load %q: %w", entry, err)
	}
	return def, cleanup, nil
}
