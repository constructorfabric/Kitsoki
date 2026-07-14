// registry.go — the installed-kit registry (S3b,
// .context/kits-implementation-plan.md "S3"). S1 shipped the manifest loader
// (Load/LoadDir) and the manifest→instance compiler (BuildKitImporter /
// SynthesizeKit in internal/app), but no runtime notion of "which kits are
// installed in this instance" — that bookkeeping is what a JSON-RPC/MCP
// dispatcher needs to turn a bare kit name (e.g. "synthetic" in
// `kit.synthetic.reporter.announce`) into a loaded *Def. S2 (versioned
// resolution + lockfile + `kitsoki kit add`) will eventually be the thing
// that POPULATES this registry from a real install step; until then,
// Registry is deliberately a thin, dependency-free "load every kit.yaml under
// these directories" scan so S3b has something concrete to dispatch against.
package kit

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Registry holds installed kit manifests, keyed by the kit's short name
// (Def.Kit, e.g. "synthetic" — NOT the "@namespace/kit" identity). v1 assumes
// short names are unique within one installation; namespace-qualified lookup
// is deferred until a real collision forces it (S2's concern).
type Registry struct {
	byName map[string]*Def
}

// NewRegistry returns an empty Registry. Callers typically populate it via
// Add or one of the Discover*/Load* helpers below.
func NewRegistry() *Registry {
	return &Registry{byName: make(map[string]*Def)}
}

// Add registers a loaded manifest under its short kit name. Returns an error
// if a different kit is already registered under that name (or the manifest
// is nil / has an empty name) — a fail-fast collision rather than silently
// shadowing one kit with another.
func (r *Registry) Add(def *Def) error {
	if r == nil {
		return fmt.Errorf("kit: nil registry")
	}
	if def == nil || def.Kit == "" {
		return fmt.Errorf("kit: cannot register a nil manifest or one with an empty kit name")
	}
	if existing, ok := r.byName[def.Kit]; ok && existing.Identity() != def.Identity() {
		return fmt.Errorf("kit: two installed kits both claim the name %q (%s and %s)", def.Kit, existing.Identity(), def.Identity())
	}
	r.byName[def.Kit] = def
	return nil
}

// Get resolves a short kit name to its loaded manifest.
func (r *Registry) Get(name string) (*Def, bool) {
	if r == nil {
		return nil, false
	}
	def, ok := r.byName[name]
	return def, ok
}

// List returns every registered manifest, sorted by short kit name — stable
// order for `runstatus.kits.list` and debug output.
func (r *Registry) List() []*Def {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.byName))
	for name := range r.byName {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*Def, 0, len(names))
	for _, name := range names {
		out = append(out, r.byName[name])
	}
	return out
}

// Len reports how many kits are registered.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.byName)
}

// LoadDirs loads one kit.yaml per directory in dirs (each dir is a kit root,
// i.e. LoadDir(dir)) into a fresh Registry. A load or duplicate-name failure
// on any entry fails the whole call — an installed-kit set that fails to load
// is a configuration error the caller should surface immediately, not paper
// over by skipping the bad one.
func LoadDirs(dirs []string) (*Registry, error) {
	reg := NewRegistry()
	for _, dir := range dirs {
		def, err := LoadDir(dir)
		if err != nil {
			return nil, fmt.Errorf("kit: load %s: %w", dir, err)
		}
		if err := reg.Add(def); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

// DiscoverDir scans the immediate subdirectories of root for a kit.yaml and
// loads each one found into a fresh Registry — the "kits/<name>/kit.yaml"
// layout an in-tree kit collection (or a future S2-managed install dir) uses.
// A missing or unreadable root is NOT an error (yields an empty registry):
// most instances have no kits installed yet, and the kit.* dispatch surface
// should degrade to "no such kit" rather than fail server startup. A kit.yaml
// that fails to load once a subdirectory contains one IS an error (same
// fail-fast reasoning as LoadDirs).
func DiscoverDir(root string) (*Registry, error) {
	reg := NewRegistry()
	if root == "" {
		return reg, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return reg, nil
		}
		return nil, fmt.Errorf("kit: scan %s: %w", root, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		kitDir := filepath.Join(root, entry.Name())
		manifestPath := filepath.Join(kitDir, ManifestFileName)
		if _, statErr := os.Stat(manifestPath); statErr != nil {
			continue // not a kit dir — no kit.yaml here.
		}
		def, err := LoadDir(kitDir)
		if err != nil {
			return nil, fmt.Errorf("kit: load %s: %w", kitDir, err)
		}
		if err := reg.Add(def); err != nil {
			return nil, err
		}
	}
	return reg, nil
}
