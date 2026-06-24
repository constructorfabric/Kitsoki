// Package kitrepo resolves (and remembers) the location of the kitsoki
// source repository — the directory the engine-targeting meta modes
// (`/meta kitsoki edit|ask|bug`) and `kitsoki bug create --target kitsoki`
// operate against.
//
// Historically this was *only* the $KITSOKI_REPO environment variable, so
// the engine-targeting features silently vanished whenever the operator
// forgot to export it. This package adds a persisted fallback under
// ~/.kitsoki/repo: once the repo is located (from the env or by
// auto-detecting a dev checkout), the path is written there and every
// later run — from any working directory, with no env var — finds it.
package kitrepo

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvVar is the environment variable that, when set, takes precedence
// over the persisted location. Exported so callers can reference the
// canonical name rather than re-typing the string.
const EnvVar = "KITSOKI_REPO"

// Resolve returns the absolute path of the kitsoki source repository, or
// "" when it cannot be determined. Resolution order:
//
//  1. $KITSOKI_REPO, if set and non-empty.
//  2. the path saved in ~/.kitsoki/repo by a prior run (skipped if it no
//     longer points at a directory).
//  3. auto-detection — the nearest ancestor of the working directory
//     whose go.mod declares `module kitsoki` (i.e. a dev checkout).
//
// A value resolved via (1) or (3) is persisted to ~/.kitsoki/repo so
// later runs from ANY directory resolve it without $KITSOKI_REPO — this
// is the "just save the location so it doesn't need to be specified"
// behaviour. Persistence is best-effort: a write failure is ignored and
// the value is still returned.
func Resolve() string {
	if env := strings.TrimSpace(os.Getenv(EnvVar)); env != "" {
		save(env)
		return env
	}
	if saved := readSaved(); saved != "" {
		return saved
	}
	if detected := detect(); detected != "" {
		save(detected)
		return detected
	}
	return ""
}

// savedFile returns the ~/.kitsoki/repo path, or ("", false) when the
// user home directory can't be determined.
func savedFile() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".kitsoki", "repo"), true
}

// readSaved returns the persisted repo path, or "" if absent, empty, or
// no longer a directory. A stale path is treated as absent: returning it
// would make `--target kitsoki` write into a directory that has moved.
func readSaved() string {
	p, ok := savedFile()
	if !ok {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	if v == "" || !isDir(v) {
		return ""
	}
	return v
}

// save writes repo to ~/.kitsoki/repo, creating the directory if needed.
// Best-effort: all errors are swallowed.
func save(repo string) {
	p, ok := savedFile()
	if !ok {
		return
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(p, []byte(repo+"\n"), 0o644)
}

// detect walks up from the working directory looking for a go.mod whose
// module path is exactly "kitsoki", and returns the directory containing
// it. Returns "" when no such ancestor exists.
func detect() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	for cur := cwd; ; {
		if b, err := os.ReadFile(filepath.Join(cur, "go.mod")); err == nil && moduleIsKitsoki(b) {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
}

// moduleIsKitsoki reports whether a go.mod's module path is "kitsoki".
func moduleIsKitsoki(gomod []byte) bool {
	for _, line := range strings.Split(string(gomod), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest) == "kitsoki"
		}
	}
	return false
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
