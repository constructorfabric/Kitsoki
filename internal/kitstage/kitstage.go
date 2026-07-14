// Package kitstage implements `.kitsoki/kits.staged.lock` — the S7 staging
// area holding kit-update candidates that have been resolved but not yet
// accepted into `.kitsoki/kits.lock` (docs/proposals/kits.md, lifecycle).
//
// # Why a sibling file, not a field inside kits.lock
//
// The accepted lockfile is committed, diffed, and byte-identity-sensitive
// (see internal/kitlock's package doc); a trial candidate is transient state
// that must never dirty it. A sibling file makes "zero effect on normal
// runs" structural — nothing outside this package reads it — and makes the
// lifecycle transitions trivially safe:
//
//   - reject  = remove the entry (and the per-kit update workdir); no other
//     file was ever touched, so there is provably no residue.
//   - accept  = write the promoted entry into kits.lock, THEN remove the
//     staged entry. The crash window between those two writes leaves a
//     staged entry identical to the accepted one, which readers detect
//     (staged == accepted → stale) and clean up.
//
// # Format
//
//	version: 1
//	kits:
//	  dev-story:
//	    source: "@kitsoki/dev-story"
//	    version: "0.2.0"
//	    commit: ""
//	    tree_hash: "b81a..."
//	    from:
//	      version: "0.1.0"
//	      tree_hash: "9f2c..."
//	    staged_at: "2026-07-11T18:22:00Z"
//
// `from` snapshots the accepted entry at stage time so trial reports and
// upgrade receipts can state old→new without re-reading a lockfile that may
// have moved on. Like kits.lock, repeated saves are byte-identical (goccy
// marshals map keys sorted).
//
// # Where the staged bytes live
//
// A staged candidate must pin real bytes, not a re-resolvable source (a
// branch ref or a local checkout can change between `kit update` and
// `kit trial`/`kit accept`). Git-tier candidates are already content-addressed
// in kitgit's commit cache; every other tier is snapshotted into the sibling
// tree cache (kitgit.MaterializeTree) keyed by tree hash. ResolveTree is the
// read side of both.
package kitstage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	yaml "github.com/goccy/go-yaml"

	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
)

// FileName is the staged lockfile's path segment under kitlock.DirName.
const FileName = "kits.staged.lock"

// UpdateDirName is the per-project directory holding one workdir per staged
// kit (`.kitsoki/kit-update/<name>/` — upgrade plan, migration worklist).
const UpdateDirName = "kit-update"

// Snapshot identifies one resolution of a kit: the manifest's own version,
// the git commit for git-tier sources (empty otherwise), and the content
// tree hash (always populated; see kitlock.Entry for the hash discipline).
type Snapshot struct {
	Version  string `yaml:"version,omitempty"`
	Commit   string `yaml:"commit,omitempty"`
	TreeHash string `yaml:"tree_hash,omitempty"`
}

// Equal reports whether two snapshots pin the same content.
func (s Snapshot) Equal(o Snapshot) bool {
	return s.Commit == o.Commit && s.TreeHash == o.TreeHash
}

// SnapshotOfLock adapts an accepted lockfile entry to a Snapshot (the "from"
// side of a staged update, and the accepted side of the staged==accepted
// stale-detection comparison).
func SnapshotOfLock(e *kitlock.Entry) Snapshot {
	if e == nil {
		return Snapshot{}
	}
	return Snapshot{Version: e.Version, Commit: e.Commit, TreeHash: e.TreeHash}
}

// Entry is one staged (resolved but not accepted) kit candidate.
type Entry struct {
	// Source is the candidate's import source string exactly as re-resolved
	// by `kit update` (it may carry a new @ref for a git-tier source).
	Source string `yaml:"source"`
	// Version/Commit/TreeHash pin the CANDIDATE resolution.
	Version  string `yaml:"version,omitempty"`
	Commit   string `yaml:"commit,omitempty"`
	TreeHash string `yaml:"tree_hash,omitempty"`
	// From snapshots the accepted lock entry at stage time (zero value when
	// the kit was not locked — `kit update` refuses that today, but the
	// format doesn't).
	From Snapshot `yaml:"from"`
	// StagedAt is the RFC3339 UTC stage time (provenance for receipts).
	StagedAt string `yaml:"staged_at,omitempty"`
}

// Snapshot returns the candidate's own snapshot (the "to" side of a trial).
func (e *Entry) Snapshot() Snapshot {
	return Snapshot{Version: e.Version, Commit: e.Commit, TreeHash: e.TreeHash}
}

// File is the parsed kits.staged.lock.
type File struct {
	Version int               `yaml:"version"`
	Kits    map[string]*Entry `yaml:"kits"`
}

// New returns an empty, ready-to-populate File.
func New() *File {
	return &File{Version: 1, Kits: map[string]*Entry{}}
}

// Path returns the staged lockfile path for a project root.
func Path(projectRoot string) string {
	return filepath.Join(projectRoot, kitlock.DirName, FileName)
}

// WorkDir returns the per-kit update workdir for a project root
// (`.kitsoki/kit-update/<name>/`).
func WorkDir(projectRoot, name string) string {
	return filepath.Join(projectRoot, kitlock.DirName, UpdateDirName, name)
}

// Load reads and parses the staged lockfile at path. A missing file returns
// a fresh empty File, mirroring kitlock.Load, so "nothing staged" needs no
// special case.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("kitstage: read %q: %w", path, err)
	}
	f := New()
	if err := yaml.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("kitstage: parse %q: %w", path, err)
	}
	if f.Kits == nil {
		f.Kits = map[string]*Entry{}
	}
	if f.Version == 0 {
		f.Version = 1
	}
	return f, nil
}

// Exists reports whether a staged lockfile is present at path.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Save writes f to path (byte-identical for identical content, like
// kitlock.Save). Saving a File with no entries removes the file instead —
// an empty staging area should leave no trace in the project.
func Save(path string, f *File) error {
	if len(f.Kits) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("kitstage: remove empty %q: %w", path, err)
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("kitstage: create %q: %w", filepath.Dir(path), err)
	}
	b, err := yaml.MarshalWithOptions(f, yaml.UseLiteralStyleIfMultiline(true))
	if err != nil {
		return fmt.Errorf("kitstage: marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("kitstage: write %q: %w", path, err)
	}
	return nil
}

// SortedNames returns the staged kit names in sorted order.
func (f *File) SortedNames() []string {
	names := make([]string, 0, len(f.Kits))
	for n := range f.Kits {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Stage records entry as the staged candidate for name under projectRoot,
// replacing any previous candidate for that name.
func Stage(projectRoot, name string, entry *Entry) error {
	p := Path(projectRoot)
	f, err := Load(p)
	if err != nil {
		return err
	}
	f.Kits[name] = entry
	return Save(p, f)
}

// Remove drops the staged candidate for name (if any) and its update
// workdir. It is the shared teardown for both reject and the tail of
// accept; removing a name that isn't staged is not an error.
func Remove(projectRoot, name string) error {
	p := Path(projectRoot)
	f, err := Load(p)
	if err != nil {
		return err
	}
	delete(f.Kits, name)
	if err := Save(p, f); err != nil {
		return err
	}
	if err := os.RemoveAll(WorkDir(projectRoot, name)); err != nil {
		return fmt.Errorf("kitstage: remove workdir for %q: %w", name, err)
	}
	// Drop .kitsoki/kit-update itself once the last workdir is gone so an
	// empty staging area leaves no trace (Save already removed the file).
	updateRoot := filepath.Dir(WorkDir(projectRoot, name))
	if entries, readErr := os.ReadDir(updateRoot); readErr == nil && len(entries) == 0 {
		_ = os.Remove(updateRoot)
	}
	return nil
}

// ResolveTree returns the staged candidate's on-disk tree, offline:
// git-tier entries come from kitgit's commit cache, every other tier from
// the tree cache MaterializeTree populated at stage time. A cache miss is a
// hard error telling the operator to re-run `kit update` — a trial must
// never silently re-resolve a moving source in place of the pinned bytes.
func ResolveTree(e *Entry) (string, error) {
	if e.Commit != "" {
		res, ok, err := kitgit.CachedResult(e.Commit)
		if err != nil {
			return "", fmt.Errorf("kitstage: look up staged commit %s: %w", e.Commit, err)
		}
		if !ok {
			return "", fmt.Errorf("kitstage: staged commit %s is no longer in the kit cache — re-run `kitsoki kit update`", e.Commit)
		}
		return res.Root, nil
	}
	root, ok, err := kitgit.CachedTree(e.TreeHash)
	if err != nil {
		return "", fmt.Errorf("kitstage: look up staged tree %s: %w", e.TreeHash, err)
	}
	if !ok {
		return "", fmt.Errorf("kitstage: staged tree %s is no longer in the kit cache — re-run `kitsoki kit update`", e.TreeHash)
	}
	return root, nil
}

// FindProjectRoot walks up from dir to the nearest directory containing a
// staged lockfile (`.kitsoki/kits.staged.lock`). ok is false when no
// ancestor has one. This is how the import-resolver hook scopes staging to
// the right project without any new plumbing: the resolver already receives
// the importing manifest's directory.
func FindProjectRoot(dir string) (string, bool) {
	cur, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for {
		if Exists(Path(cur)) {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}
