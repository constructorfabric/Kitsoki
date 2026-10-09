package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	legacy "kitsoki/internal/capsule"
)

// FileDefinitionStore discovers project-local definitions before compatible
// root capsules. It never follows a definition path outside its project root.
type FileDefinitionStore struct{ ProjectRoot string }

func (s FileDefinitionStore) Get(_ context.Context, id string) (Definition, error) {
	if strings.TrimSpace(id) == "" {
		return Definition{}, fmt.Errorf("capsule definition: id is required")
	}
	root, err := projectRoot(s.ProjectRoot)
	if err != nil {
		return Definition{}, err
	}
	for _, path := range s.paths(id) {
		if _, err := os.Stat(path); err == nil {
			return loadDefinition(root, path)
		} else if !os.IsNotExist(err) {
			return Definition{}, fmt.Errorf("capsule definition: inspect %s: %w", path, err)
		}
	}
	return Definition{}, fmt.Errorf("%w: definition %q", ErrNotFound, id)
}

func (s FileDefinitionStore) List(ctx context.Context) ([]Definition, error) {
	root, err := projectRoot(s.ProjectRoot)
	if err != nil {
		return nil, err
	}
	patterns := []string{
		filepath.Join(root, ".kitsoki", "capsules", "*", "capsule.yaml"),
		filepath.Join(root, ".kitsoki", "capsules", "*.yaml"),
		filepath.Join(root, "capsules", "*", "capsule.yaml"),
	}
	seen := map[string]bool{}
	var out []Definition
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, path := range matches {
			def, err := loadDefinition(root, path)
			if err != nil {
				return nil, err
			}
			if seen[def.ID] {
				continue
			}
			if _, err := s.Get(ctx, def.ID); err != nil {
				return nil, err
			}
			seen[def.ID] = true
			out = append(out, def)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s FileDefinitionStore) paths(id string) []string {
	root, _ := projectRoot(s.ProjectRoot)
	return []string{
		filepath.Join(root, ".kitsoki", "capsules", id, "capsule.yaml"),
		filepath.Join(root, ".kitsoki", "capsules", id+".yaml"),
		filepath.Join(root, "capsules", id, "capsule.yaml"),
	}
}

func projectRoot(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("capsule definition: project root is required")
	}
	root, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	return root, nil
}

func loadDefinition(root, path string) (Definition, error) {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return Definition{}, fmt.Errorf("capsule definition: %s escapes project root", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("capsule definition: read %s: %w", path, err)
	}
	var probe struct {
		Schema string `yaml:"schema"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return Definition{}, fmt.Errorf("capsule definition: parse %s: %w", path, err)
	}
	if probe.Schema == "" {
		// Existing capsules are synthetic by construction. Preserve their
		// contents and delegate detailed legacy validation to its package.
		spec, _, err := legacy.Load(path)
		if err != nil {
			return Definition{}, err
		}
		def := Definition{Schema: DefinitionSchema, ID: spec.Name, Source: Source{Kind: SourceSynthetic, SyntheticSpec: filepath.ToSlash(rel)}, LegacyPath: path}
		def.Digest = digestBytes(raw)
		return def, validateDefinition(def)
	}
	var def Definition
	if err := yaml.Unmarshal(raw, &def); err != nil {
		return Definition{}, fmt.Errorf("capsule definition: parse %s: %w", path, err)
	}
	def.LegacyPath = path
	def.Digest = digestBytes(raw)
	return def, validateDefinition(def)
}

func validateDefinition(def Definition) error {
	if def.Schema != DefinitionSchema {
		return fmt.Errorf("capsule definition: schema %q, want %q", def.Schema, DefinitionSchema)
	}
	if strings.TrimSpace(def.ID) == "" {
		return fmt.Errorf("capsule definition: id is required")
	}
	switch def.Source.Kind {
	case SourceSynthetic:
		if strings.TrimSpace(def.Source.SyntheticSpec) == "" {
			return fmt.Errorf("capsule definition %q: synthetic_spec is required", def.ID)
		}
	case SourceSelf:
		if def.Source.Commit != "" {
			return fmt.Errorf("capsule definition %q: self source cannot pin commit", def.ID)
		}
	case SourcePinned:
		if strings.TrimSpace(def.Source.Ref) == "" || strings.TrimSpace(def.Source.Commit) == "" {
			return fmt.Errorf("capsule definition %q: pinned source requires ref and commit", def.ID)
		}
	case SourceDevWorkspaceScript:
		if strings.TrimSpace(def.Source.Development.Target) == "" {
			return fmt.Errorf("capsule definition %q: development target is required", def.ID)
		}
		if root := def.Source.Development.Root; filepath.IsAbs(root) || strings.HasPrefix(filepath.Clean(root), "..") {
			return fmt.Errorf("capsule definition %q: development root escapes project", def.ID)
		}
	default:
		return fmt.Errorf("capsule definition %q: unknown source kind %q", def.ID, def.Source.Kind)
	}
	for _, o := range def.Overlays {
		if o.Visibility != "workspace" && o.Visibility != "verifier" {
			return fmt.Errorf("capsule definition %q: invalid overlay visibility %q", def.ID, o.Visibility)
		}
		if filepath.IsAbs(o.Path) || strings.HasPrefix(filepath.Clean(o.Path), "..") {
			return fmt.Errorf("capsule definition %q: overlay path escapes project", def.ID)
		}
	}
	for id, cmd := range def.Policy.Commands {
		if strings.TrimSpace(id) == "" || len(cmd.Argv) == 0 || strings.TrimSpace(cmd.Argv[0]) == "" {
			return fmt.Errorf("capsule definition %q: command %q requires argv", def.ID, id)
		}
	}
	return nil
}

func digestBytes(raw []byte) string {
	h := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(h[:])
}

// MemoryInstanceStore is a deterministic test and embedded-runtime store. The
// manager's CAS semantics are identical to future SQLite/manifest stores.
type MemoryInstanceStore struct {
	mu     sync.Mutex
	values map[string]Instance
}

func NewMemoryInstanceStore() *MemoryInstanceStore {
	return &MemoryInstanceStore{values: map[string]Instance{}}
}
func (s *MemoryInstanceStore) Create(_ context.Context, in Instance) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[in.ID]; ok {
		return Instance{}, fmt.Errorf("capsule control: instance %q already exists", in.ID)
	}
	if in.Generation == 0 {
		in.Generation = 1
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = in.CreatedAt
	}
	s.values[in.ID] = in
	return in, nil
}
func (s *MemoryInstanceStore) Get(_ context.Context, id string) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.values[id]
	if !ok {
		return Instance{}, fmt.Errorf("%w: instance %q", ErrNotFound, id)
	}
	return in, nil
}
func (s *MemoryInstanceStore) List(_ context.Context) ([]Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Instance, 0, len(s.values))
	for _, in := range s.values {
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *MemoryInstanceStore) CompareAndSwap(_ context.Context, id string, generation uint64, mutate func(*Instance) error) (Instance, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	in, ok := s.values[id]
	if !ok {
		return Instance{}, fmt.Errorf("%w: instance %q", ErrNotFound, id)
	}
	if in.Generation != generation {
		return Instance{}, fmt.Errorf("%w: instance %q generation %d, got %d", ErrStale, id, in.Generation, generation)
	}
	if err := mutate(&in); err != nil {
		return Instance{}, err
	}
	in.Generation++
	in.UpdatedAt = time.Now().UTC()
	s.values[id] = in
	return in, nil
}

// CanonicalDefinitionJSON is useful in receipts and tests. It excludes
// machine-local LegacyPath while retaining the immutable definition digest.
func CanonicalDefinitionJSON(def Definition) ([]byte, error) {
	def.LegacyPath = ""
	return json.Marshal(def)
}

// FileInstanceStore is a compact, recoverable persistent store. Its index is
// deliberately outside individual workspaces so materialization can reserve an
// instance before a provider creates the capsule sentinel. Every record is also
// copied into a native workspace manifest by providers as they migrate; the
// index remains rebuildable from those manifests.
type FileInstanceStore struct{ Root string }

func (s FileInstanceStore) Create(ctx context.Context, in Instance) (Instance, error) {
	if _, err := s.Get(ctx, in.ID); err == nil {
		return Instance{}, fmt.Errorf("capsule control: instance %q already exists", in.ID)
	} else if !errors.Is(err, ErrNotFound) {
		return Instance{}, err
	}
	if in.Generation == 0 {
		in.Generation = 1
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	if in.UpdatedAt.IsZero() {
		in.UpdatedAt = in.CreatedAt
	}
	if err := s.write(in); err != nil {
		return Instance{}, err
	}
	return in, nil
}
func (s FileInstanceStore) Get(_ context.Context, id string) (Instance, error) {
	raw, err := os.ReadFile(s.path(id))
	if os.IsNotExist(err) {
		return Instance{}, fmt.Errorf("%w: instance %q", ErrNotFound, id)
	}
	if err != nil {
		return Instance{}, err
	}
	var disk diskInstance
	if err := json.Unmarshal(raw, &disk); err != nil {
		return Instance{}, fmt.Errorf("capsule control: parse instance %q: %w", id, err)
	}
	disk.Instance.Path = disk.Path
	return disk.Instance, nil
}
func (s FileInstanceStore) List(ctx context.Context) ([]Instance, error) {
	dir := s.dir()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Instance
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		in, err := s.Get(ctx, strings.TrimSuffix(entry.Name(), ".json"))
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s FileInstanceStore) CompareAndSwap(ctx context.Context, id string, generation uint64, mutate func(*Instance) error) (Instance, error) {
	in, err := s.Get(ctx, id)
	if err != nil {
		return Instance{}, err
	}
	if in.Generation != generation {
		return Instance{}, fmt.Errorf("%w: instance %q generation %d, got %d", ErrStale, id, in.Generation, generation)
	}
	if err := mutate(&in); err != nil {
		return Instance{}, err
	}
	in.Generation++
	in.UpdatedAt = time.Now().UTC()
	if err := s.write(in); err != nil {
		return Instance{}, err
	}
	return in, nil
}
func (s FileInstanceStore) dir() string           { return filepath.Join(s.Root, ".kitsoki-capsule-instances") }
func (s FileInstanceStore) path(id string) string { return filepath.Join(s.dir(), id+".json") }
func (s FileInstanceStore) write(in Instance) error {
	if !instanceIDPattern.MatchString(in.ID) {
		return fmt.Errorf("capsule control: invalid instance id %q", in.ID)
	}
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(diskInstance{Instance: in, Path: in.Path}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path(in.ID) + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(in.ID))
}

// diskInstance carries the machine-local path in the manager-owned index. The
// public Instance JSON deliberately omits it; the disk index is not an MCP/API
// response and must retain it to survive a process restart.
type diskInstance struct {
	Instance
	Path string `json:"path"`
}
