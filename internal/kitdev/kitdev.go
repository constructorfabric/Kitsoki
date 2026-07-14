// Package kitdev implements `kitsoki kit dev <name> --path <checkout>` — the
// S2 generalization of the single repo-wide --kitsoki-repo/$KITSOKI_REPO
// override (cmd/kitsoki/resolver.go) into a per-kit local-path override.
//
// KITSOKI_REPO always points at ONE kitsoki checkout and every
// `@kitsoki/<name>` import resolves against `<repo>/stories/<name>/app.yaml`
// there. That's the right shape for "I'm working inside the kitsoki repo
// itself" but the wrong shape once a kit lives in its own repo (D5 in the
// implementation plan): an operator contributing to, say, `@kitsoki/dev-story`
// or a git-sourced kit wants to point THAT ONE name at a local checkout
// without disturbing how every other kit resolves. kitdev persists that
// mapping per-name, the same way internal/kitrepo persists the single
// repo-wide override under ~/.kitsoki/repo:
//
//	~/.kitsoki/kit-dev/<name>   ->  absolute path to a local checkout
//	                                (a directory containing app.yaml)
//
// Resolve honours a per-name env var override (KITSOKI_KIT_DEV_<NAME>, name
// upper-cased with '-'/'/' turned into '_') before the persisted file, the
// same override-wins-over-persisted precedence kitrepo.Resolve uses for
// $KITSOKI_REPO vs ~/.kitsoki/repo.
package kitdev

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dir returns ~/.kitsoki/kit-dev, or ("", false) when the user home
// directory can't be determined.
func dir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".kitsoki", "kit-dev"), true
}

// envVar names the per-kit override env var for name.
func envVar(name string) string {
	r := strings.NewReplacer("-", "_", "/", "_", "@", "")
	return "KITSOKI_KIT_DEV_" + strings.ToUpper(r.Replace(name))
}

// Resolve returns the local checkout path overriding name, or "" when no
// override is configured (env var unset AND no persisted entry, or a
// persisted entry that no longer points at a directory).
func Resolve(name string) string {
	if env := strings.TrimSpace(os.Getenv(envVar(name))); env != "" {
		return env
	}
	return readSaved(name)
}

func entryPath(name string) (string, bool) {
	base, ok := dir()
	if !ok {
		return "", false
	}
	return filepath.Join(base, name), true
}

func readSaved(name string) string {
	p, ok := entryPath(name)
	if !ok {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return ""
	}
	if info, statErr := os.Stat(v); statErr != nil || !info.IsDir() {
		return "" // stale entry: the checkout moved or was removed
	}
	return v
}

// Set persists path (made absolute) as the dev override for name.
func Set(name, path string) error {
	base, ok := dir()
	if !ok {
		return fmt.Errorf("kitdev: cannot determine user home directory")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("kitdev: resolve %q: %w", path, err)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return fmt.Errorf("kitdev: create %q: %w", base, err)
	}
	if err := os.WriteFile(filepath.Join(base, name), []byte(abs+"\n"), 0o644); err != nil {
		return fmt.Errorf("kitdev: write override for %q: %w", name, err)
	}
	return nil
}

// Clear removes the persisted dev override for name, if any. Not finding one
// is not an error.
func Clear(name string) error {
	p, ok := entryPath(name)
	if !ok {
		return nil
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("kitdev: clear override for %q: %w", name, err)
	}
	return nil
}

// List returns every persisted dev override, keyed by kit name. Env-var-only
// overrides (no persisted file) are not enumerable and so are not included —
// List is a listing of `kit dev` state, not of every possible override
// source.
func List() (map[string]string, error) {
	base, ok := dir()
	if !ok {
		return map[string]string{}, nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("kitdev: list %q: %w", base, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if v := readSaved(e.Name()); v != "" {
			out[e.Name()] = v
		}
	}
	return out, nil
}
