package store

// trace_path.go derives default on-disk paths for per-session JSONL traces.
// See doc.go for the package overview.
//
// Two path schemes are used:
//
//  1. DefaultTracePath — home-anchored, keyed by (app, transport, thread).
//     Used by session continue / TUI where the key is deterministic and
//     reusable across process restarts (resume semantics).
//     Path:  ~/.kitsoki/sessions/<app>/<sha8>-<slug>.jsonl
//
//  2. DefaultRunTracePath — repo-anchored, keyed by (app, UTC timestamp).
//     Used by `kitsoki run` for one-shot TUI sessions started without an
//     explicit session key.  Post-mortems land next to the story sources
//     and don't require the operator to remember a --trace flag.
//     Path:  <anchor>/.kitsoki/sessions/<UTC>-<app>.jsonl
//     where <anchor> is the nearest ancestor directory containing a
//     .kitsoki/ subdirectory or .kitsoki-root marker, falling back to cwd.

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// slugUnsafe matches characters that are unsafe or inconvenient in a file-system
// path component.  We keep alphanumerics, '.', '_', and '-' (the last already
// being our replacement character).
var slugUnsafe = regexp.MustCompile(`[^a-zA-Z0-9._\-]`)

// DefaultTracePath returns the canonical on-disk path for a JSONL session trace
// keyed by (app, transport, thread).
//
// Path:  ~/.kitsoki/sessions/<app>/<sha8>-<slug>.jsonl
//
//   - <app>  is the app identifier (from AppDef.App.ID); sanitised with the same
//     slug rules as the thread component.
//   - <sha8> is the first 8 hex chars of sha256(transport + ":" + thread), giving
//     collision-safety and a fixed-width leading column.
//   - <slug> is transport + ":" + thread with unsafe characters replaced by '-'.
//
// The parent directory is NOT created by this function; callers are responsible
// for calling os.MkdirAll before opening the trace.
func DefaultTracePath(app, transport, thread string) string {
	key := transport + ":" + thread
	sum := sha256.Sum256([]byte(key))
	sha8 := fmt.Sprintf("%x", sum[:4]) // 4 bytes → 8 hex chars

	slug := slugUnsafe.ReplaceAllString(key, "-")
	appSlug := slugUnsafe.ReplaceAllString(app, "-")

	home, err := os.UserHomeDir()
	if err != nil {
		// Fallback: use a path relative to /tmp so the caller still gets a
		// deterministic, writable location even without a home directory.
		home = os.TempDir()
	}
	return filepath.Join(home, ".kitsoki", "sessions", appSlug, sha8+"-"+slug+".jsonl")
}

// SessionsDir returns the root under which DefaultTracePath writes per-session
// JSONL traces: ~/.kitsoki/sessions (or $TMPDIR/.kitsoki/sessions when no home
// directory is available, matching DefaultTracePath's fallback). Each app gets
// a subdirectory. Tools that discover or resolve sessions after the fact (e.g.
// `kitsoki trace --app … --latest`) anchor here so the location stays in one
// place.
func SessionsDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".kitsoki", "sessions")
}

// DefaultRunTracePath returns the repo-anchored JSONL trace path for a one-shot
// `kitsoki run` session. It walks upward from cwd looking for an existing
// .kitsoki/ directory or a .kitsoki-root marker file (the same signal
// internal/app/imports.go uses to identify a repo root). If none is found it
// anchors at cwd, creating .kitsoki/sessions/ there.
//
// Path:  <anchor>/.kitsoki/sessions/<UTC>-<appID>.jsonl
//
// The parent directory IS created (MkdirAll) before returning the path so the
// caller can open the file immediately. Returns "" if cwd cannot be determined.
func DefaultRunTracePath(appID string) string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	anchor := cwd
	cur := cwd
	for {
		if fi, statErr := os.Stat(filepath.Join(cur, ".kitsoki")); statErr == nil && fi.IsDir() {
			anchor = cur
			break
		}
		if _, statErr := os.Stat(filepath.Join(cur, ".kitsoki-root")); statErr == nil {
			anchor = cur
			break
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			// No existing anchor found; create .kitsoki/sessions/ at cwd.
			if mkErr := os.MkdirAll(filepath.Join(cwd, ".kitsoki", "sessions"), 0o755); mkErr != nil {
				return ""
			}
			anchor = cwd
			break
		}
		cur = parent
	}
	safeApp := appID
	if safeApp == "" {
		safeApp = "session"
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	sessDir := filepath.Join(anchor, ".kitsoki", "sessions")
	if mkErr := os.MkdirAll(sessDir, 0o755); mkErr != nil {
		return ""
	}
	return filepath.Join(sessDir, stamp+"-"+safeApp+".jsonl")
}
