// Package kitlock implements `.kitsoki/kits.lock`, the S2 lockfile pinning
// each resolved kit import to (source, commit, tree-hash) — the piece the
// design doc calls out as absent entirely before this slice
// (docs/proposals/kits.md; internal/app/imports.go survey note: "No
// lockfile exists anywhere").
//
// # Format
//
// YAML, matching every other kitsoki state/config file in the repo
// (.kitsoki.yaml project config, kit/app manifests — all goccy/go-yaml).
// The top-level shape:
//
//	version: 1
//	kits:
//	  dev-story:
//	    source: "@kitsoki/dev-story"
//	    version: "1.0.0"
//	    commit: ""
//	    tree_hash: "9f2c...""
//
// Keyed by kit name (the alias/name the operator gave `kitsoki kit add`, not
// a composite "name@version" string) — a map naturally forbids two entries
// for the same name, which is what "pins name@version" means in practice:
// exactly one locked version per name at a time. `commit` is empty for
// non-git sources (there is no commit to pin); `tree_hash` is always
// populated (git's own tree object hash for the git tier, a generalized
// content hash — see internal/kitgit.DirTreeHash — for every other tier).
package kitlock

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	yaml "github.com/goccy/go-yaml"
)

// FileName is the lockfile's path segment relative to a project root.
const FileName = "kits.lock"

// DirName is the directory the lockfile (and other kit-local state, e.g.
// the kit-dev.yaml override file) lives under.
const DirName = ".kitsoki"

// Entry is one locked kit.
type Entry struct {
	// Source is the import source string exactly as resolved (e.g.
	// "@kitsoki/dev-story" or "git+https://host/org/repo@v1.2.3").
	Source string `yaml:"source"`
	// Version is the resolved manifest's own app.version:, or "" when the
	// resolved manifest declares none.
	Version string `yaml:"version,omitempty"`
	// Commit is the resolved git commit SHA for a git-tier source; empty
	// for every other tier (there is nothing to pin a commit to).
	Commit string `yaml:"commit,omitempty"`
	// TreeHash is a content hash of the resolved kit's manifest directory:
	// git's own tree object hash for the git tier, internal/kitgit.DirTreeHash
	// for every other tier.
	TreeHash string `yaml:"tree_hash,omitempty"`
}

// Lockfile is the parsed `.kitsoki/kits.lock`.
type Lockfile struct {
	Version int              `yaml:"version"`
	Kits    map[string]*Entry `yaml:"kits"`
}

// New returns an empty, ready-to-populate Lockfile.
func New() *Lockfile {
	return &Lockfile{Version: 1, Kits: map[string]*Entry{}}
}

// Path returns the lockfile path for a project root.
func Path(projectRoot string) string {
	return filepath.Join(projectRoot, DirName, FileName)
}

// Load reads and parses the lockfile at path. A missing file is not an
// error — it returns a fresh, empty Lockfile (see New) so callers (kit add
// on a project with no lockfile yet) don't need a special case.
func Load(path string) (*Lockfile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("kitlock: read %q: %w", path, err)
	}
	lf := New()
	if err := yaml.Unmarshal(b, lf); err != nil {
		return nil, fmt.Errorf("kitlock: parse %q: %w", path, err)
	}
	if lf.Kits == nil {
		lf.Kits = map[string]*Entry{}
	}
	if lf.Version == 0 {
		lf.Version = 1
	}
	return lf, nil
}

// Exists reports whether a lockfile is present at path.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Save writes lf to path, creating the parent directory if needed. Map
// iteration order is nondeterministic in Go, but goccy/go-yaml marshals map
// keys sorted, so repeated saves of the same content are byte-identical
// (important for a lockfile meant to be diffed/committed).
func Save(path string, lf *Lockfile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("kitlock: create %q: %w", filepath.Dir(path), err)
	}
	b, err := yaml.MarshalWithOptions(lf, yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return fmt.Errorf("kitlock: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("kitlock: write %q: %w", path, err)
	}
	return nil
}

// SortedNames returns the lockfile's kit names in sorted order, for stable
// CLI output.
func (lf *Lockfile) SortedNames() []string {
	names := make([]string, 0, len(lf.Kits))
	for n := range lf.Kits {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
