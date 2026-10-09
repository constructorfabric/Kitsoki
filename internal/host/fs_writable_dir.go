// Package host — host.fs.writable_dir — resolve a configured directory to
// itself when it accepts writes, or to a fallback directory otherwise.
//
// Motivation: several story-level "durable path" world vars (e.g. dev-story's
// design_durable_path) default to a location that is appropriate for the
// story's own dogfood checkout (docs/proposals/) but is NOT writable in every
// context that story can run in — most notably Kitsoki's own primary checkout,
// which is intentionally read-only (see the repo's AGENTS.md: "the primary
// checkout is protected as read-mostly"). Without this check, a room that
// mints a workspace folder under the configured durable path hard-fails with
// a raw "mkdir ...: permission denied" the first time it runs from a
// read-only location, rather than degrading to a location that is always
// writable.
//
// This handler makes that fallback decision explicit and story-driven: a room
// calls it once, early, and rebinds its own "durable path" world var to
// whatever it returns — so every downstream step (workspace minting, artifact
// writes, publish) sees a writable root without each of them re-deriving it.
package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// FSWritableDirHandler implements host.fs.writable_dir.
//
// Required args:
//   - path     (string): the configured directory to check. It need not
//     exist yet — writability is probed against the nearest existing
//     ancestor, since that is what actually governs whether a later
//     os.MkdirAll under it would succeed.
//   - fallback (string): the directory to use instead when path is not
//     writable. Not itself re-validated — a caller should pick a fallback
//     it already knows is safe (e.g. ".context/designs", the repo's
//     conventional local-runtime scratch dir).
//
// Returns Result.Data:
//   - path          (string): path, unchanged, if writable; otherwise fallback.
//   - used_fallback (bool):   true when fallback was returned.
func FSWritableDirHandler(_ context.Context, args map[string]any) (Result, error) {
	path, _ := args["path"].(string)
	path = strings.TrimSpace(path)
	fallback, _ := args["fallback"].(string)
	fallback = strings.TrimSpace(fallback)
	if path == "" {
		return Result{Error: "host.fs.writable_dir: path argument is required"}, nil
	}
	if fallback == "" {
		return Result{Error: "host.fs.writable_dir: fallback argument is required"}, nil
	}

	if dirAcceptsWrites(path) {
		return Result{Data: map[string]any{"path": path, "used_fallback": false}}, nil
	}
	return Result{Data: map[string]any{"path": fallback, "used_fallback": true}}, nil
}

// dirAcceptsWrites reports whether dir — or, when dir does not exist yet, its
// nearest existing ancestor — accepts new files. It probes with a real
// create-then-remove of a hidden marker file rather than inspecting
// permission bits, so the answer holds under uid/gid mismatches, ACLs, and a
// read-only bind mount alike (all of which a bit check can miss). Best-effort:
// any stat/open error along the walk (including permission-denied on an
// ancestor) reports not-writable rather than erroring the caller.
func dirAcceptsWrites(dir string) bool {
	dir = filepath.Clean(dir)
	for {
		info, err := os.Stat(dir)
		if err == nil {
			if !info.IsDir() {
				return false
			}
			probe := filepath.Join(dir, ".kitsoki-writable-probe")
			f, werr := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
			if werr != nil {
				return false
			}
			_ = f.Close()
			_ = os.Remove(probe)
			return true
		}
		if !os.IsNotExist(err) {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
