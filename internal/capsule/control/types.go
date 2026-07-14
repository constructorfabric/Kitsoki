package control

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefinitionSchema is the checked-in definition format understood by the
// control plane. Legacy capsule.yaml files are projected into this format by
// FileDefinitionStore so existing fixtures remain usable.
const DefinitionSchema = "capsule-definition/v1"

// State is the lifecycle state of a mutable workspace instance.
type State string

const (
	StateDeclared      State = "declared"
	StateMaterializing State = "materializing"
	StateReady         State = "ready"
	StateDirty         State = "dirty"
	StateCommitted     State = "committed"
	StateIntegrated    State = "integrated"
	StateConflicted    State = "conflicted"
	StateFailed        State = "failed"
	StateClosed        State = "closed"
)

func (s State) valid() bool {
	switch s {
	case StateDeclared, StateMaterializing, StateReady, StateDirty, StateCommitted, StateIntegrated, StateConflicted, StateFailed, StateClosed:
		return true
	default:
		return false
	}
}

// SourceKind selects the materialization family. Synthetic is fully
// deterministic. Self and pinned sources are intentionally explicit; a pinned
// source may only be materialized at its immutable commit.
type SourceKind string

const (
	SourceSynthetic SourceKind = "synthetic"
	SourceSelf      SourceKind = "self"
	SourcePinned    SourceKind = "pinned"
	// SourceDevWorkspaceScript is a temporary Kitsoki compatibility source.
	// It keeps the protected clone/rebase workflow behind the native manager
	// while generic projects use self or pinned sources directly.
	SourceDevWorkspaceScript SourceKind = "dev-workspace-script"
)

// Definition is a checked-in, immutable Capsule recipe. It contains references
// and policy names, never credential values or machine-local paths.
type Definition struct {
	Schema      string    `yaml:"schema" json:"schema"`
	ID          string    `yaml:"id" json:"id"`
	Description string    `yaml:"description,omitempty" json:"description,omitempty"`
	Source      Source    `yaml:"source" json:"source"`
	Overlays    []Overlay `yaml:"overlays,omitempty" json:"overlays,omitempty"`
	Environment string    `yaml:"environment,omitempty" json:"environment,omitempty"`
	Policy      Policy    `yaml:"policy,omitempty" json:"policy,omitempty"`
	LegacyPath  string    `yaml:"-" json:"-"`
	Digest      string    `yaml:"-" json:"digest"`
}

// Source declares a reproducible source. SyntheticSpec points to a legacy or
// project-local capsule recipe; Ref is a project-relative path for self and a
// remote/cache reference for pinned sources.
type Source struct {
	Kind          SourceKind        `yaml:"kind" json:"kind"`
	Ref           string            `yaml:"ref,omitempty" json:"ref,omitempty"`
	Commit        string            `yaml:"commit,omitempty" json:"commit,omitempty"`
	SyntheticSpec string            `yaml:"synthetic_spec,omitempty" json:"synthetic_spec,omitempty"`
	Development   DevelopmentSource `yaml:"development,omitempty" json:"development,omitempty"`
}

// DevelopmentSource records the protected-workspace policy consumed by the
// migration adapter. It intentionally names refs and hooks, never paths or
// credentials. BranchPrefix may contain no path separators beyond Git refs.
type DevelopmentSource struct {
	Base         string `yaml:"base,omitempty" json:"base,omitempty"`
	Target       string `yaml:"target,omitempty" json:"target,omitempty"`
	BranchPrefix string `yaml:"branch_prefix,omitempty" json:"branch_prefix,omitempty"`
	Root         string `yaml:"root,omitempty" json:"root,omitempty"`
	Bootstrap    bool   `yaml:"bootstrap,omitempty" json:"bootstrap,omitempty"`
}

// Overlay separates coding workspace material from verifier-only material.
// Verifier overlays never become reachable through the agent-facing FS API.
type Overlay struct {
	Path       string `yaml:"path" json:"path"`
	Visibility string `yaml:"visibility" json:"visibility"` // workspace|verifier
}

// Policy is the least-authority portion of a definition. Command IDs rather
// than shell strings are the normal execution contract.
type Policy struct {
	Commands map[string]Command `yaml:"commands,omitempty" json:"commands,omitempty"`
	Network  string             `yaml:"network,omitempty" json:"network,omitempty"`
	RawArgv  bool               `yaml:"raw_argv,omitempty" json:"raw_argv,omitempty"`
}

// Command is a named argv declaration. Shell interpretation is never implicit.
type Command struct {
	Argv    []string `yaml:"argv" json:"argv"`
	Timeout string   `yaml:"timeout,omitempty" json:"timeout,omitempty"`
}

// Lease binds an instance to a single owner. Generation changes invalidate
// stale opaque handles and is required on every mutation.
type Lease struct {
	Owner     string    `json:"owner"`
	Acquired  time.Time `json:"acquired_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// Instance is the durable internal record. Path is intentionally excluded from
// JSON front-door results; callers receive Handle instead.
type Instance struct {
	ID               string       `json:"id"`
	DefinitionID     string       `json:"definition_id"`
	DefinitionDigest string       `json:"definition_digest"`
	Provider         string       `json:"provider"`
	Path             string       `json:"-"`
	SourceRef        string       `json:"source_ref,omitempty"`
	Head             string       `json:"head,omitempty"`
	Branch           string       `json:"branch,omitempty"`
	VerifierOverlays []OverlayRef `json:"verifier_overlays,omitempty"`
	State            State        `json:"state"`
	Generation       uint64       `json:"generation"`
	Lease            Lease        `json:"lease"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
}

// Handle is the sole workspace authority exposed outside the manager.
type Handle struct {
	ID         string `json:"id"`
	Generation uint64 `json:"generation"`
}

// ScopeGrant is fixed when a Capsule server starts. Individual calls can
// narrow authority but can never add a definition, executor, effect, or root.
type ScopeGrant struct {
	Owner          string   `json:"owner,omitempty"`
	ProjectRoot    string   `json:"project_root"`
	WorkspaceRoots []string `json:"workspace_roots"`
	Definitions    []string `json:"definitions,omitempty"`
	Executors      []string `json:"executors,omitempty"`
	Effects        []string `json:"effects,omitempty"`
	Remotes        []string `json:"remotes,omitempty"`
	Branches       []string `json:"branches,omitempty"`
}

// Clone returns an authority snapshot whose slices cannot be widened by a
// caller mutating the grant used to construct a front door. Interface and
// provider values live on Manager; only the grant's scalar and slice values
// define authority.
func (g ScopeGrant) Clone() ScopeGrant {
	g.WorkspaceRoots = append([]string(nil), g.WorkspaceRoots...)
	g.Definitions = append([]string(nil), g.Definitions...)
	g.Executors = append([]string(nil), g.Executors...)
	g.Effects = append([]string(nil), g.Effects...)
	g.Remotes = append([]string(nil), g.Remotes...)
	g.Branches = append([]string(nil), g.Branches...)
	return g
}

func (g ScopeGrant) Validate() error {
	if strings.TrimSpace(g.ProjectRoot) == "" {
		return errors.New("capsule grant: project_root is required")
	}
	root, err := filepath.Abs(g.ProjectRoot)
	if err != nil {
		return fmt.Errorf("capsule grant: project_root: %w", err)
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	if len(g.WorkspaceRoots) == 0 {
		return errors.New("capsule grant: at least one workspace root is required")
	}
	for _, raw := range g.WorkspaceRoots {
		if !filepath.IsAbs(raw) {
			return fmt.Errorf("capsule grant: workspace root %q must be absolute", raw)
		}
		candidate, err := filepath.Abs(raw)
		if err != nil {
			return err
		}
		if real, err := filepath.EvalSymlinks(candidate); err == nil {
			candidate = real
		}
		if candidate == root {
			return errors.New("capsule grant: project root cannot also be a workspace root")
		}
	}
	return nil
}

// Allows returns whether a named authority is present. An empty allow-list is
// intentionally deny-by-default; callers that need a broad grant must name it.
func (g ScopeGrant) Allows(kind, value string) bool {
	var values []string
	switch kind {
	case "definition":
		values = g.Definitions
	case "executor":
		values = g.Executors
	case "effect":
		values = g.Effects
	case "remote":
		values = g.Remotes
	case "branch":
		values = g.Branches
	default:
		return false
	}
	for _, allowed := range values {
		if allowed == value || allowed == "*" {
			return true
		}
	}
	return false
}

// ValidEffect reports whether an effect is part of the Capsule capability
// vocabulary. Keeping this list centralized prevents startup adapters from
// silently accepting a misspelled or future capability.
func ValidEffect(effect string) bool {
	switch effect {
	case "workspace_manage", "fs_write", "exec", "raw_exec", "vcs_commit", "local_reconcile", "remote_publish", "ci_run", "cleanup", "env_write":
		return true
	default:
		return false
	}
}

// DefinitionStore resolves checked-in immutable definitions.
type DefinitionStore interface {
	Get(context.Context, string) (Definition, error)
	List(context.Context) ([]Definition, error)
}

// InstanceStore persists mutable instance records. Implementations must enforce
// CompareAndSwap generation semantics rather than last-writer-wins updates.
type InstanceStore interface {
	Create(context.Context, Instance) (Instance, error)
	Get(context.Context, string) (Instance, error)
	List(context.Context) ([]Instance, error)
	CompareAndSwap(context.Context, string, uint64, func(*Instance) error) (Instance, error)
}

// WorkspaceProvider materializes a definition into a manager-owned path.
type WorkspaceProvider interface {
	Name() string
	Create(context.Context, Definition, Instance) (MaterializedWorkspace, error)
	Close(context.Context, Instance) error
}

// WorkspaceMaterializationVerifier is implemented by providers whose Create
// leaves durable, provider-owned evidence at Instance.Path (both git-backed
// providers write the `.kitsoki-capsule` sentinel file). Manager.Create uses
// it to self-heal an instance record whose on-disk workspace disappeared out
// from under the instance store — a manual `rm -rf`, an external `git
// worktree prune`, an aborted create — instead of trusting stale Ready
// metadata and handing back a handle to a path that no longer exists.
// Providers that don't materialize durable on-disk state (e.g. a
// synthetic/in-memory provider used in tests) simply don't implement this,
// and Manager.Create falls back to its historical trust-the-store behavior
// for them, since "no directory" carries no meaning for a provider with no
// on-disk contract in the first place.
type WorkspaceMaterializationVerifier interface {
	WorkspaceMaterialized(in Instance) bool
}

// WorkspaceIntegrator is implemented only by providers that own a complete
// protected integration lifecycle. Generic local ref reconciliation continues
// through reconcile.Plan/Apply; this seam preserves the historical script's
// rebase and primary-checkout protection during migration.
type WorkspaceIntegrator interface {
	Integrate(context.Context, Definition, Instance, string) error
}

// MaterializedWorkspace contains facts discovered by the provider, never a
// caller-chosen authority expansion.
type MaterializedWorkspace struct {
	Path             string
	SourceRef        string
	Head             string
	Branch           string
	VerifierOverlays []OverlayRef
}

// OverlayRef is a sanitized, project-relative reference to verifier-only
// material. It carries integrity evidence without granting agent-visible FS
// access to the underlying path.
type OverlayRef struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

// Event is the deterministic trace fact emitted by the manager. EventSink is
// deliberately tiny so the current trace runtime and tests can adapt it.
type Event struct {
	Kind       string         `json:"kind"`
	InstanceID string         `json:"instance_id"`
	Generation uint64         `json:"generation"`
	Fields     map[string]any `json:"fields,omitempty"`
}

type EventSink interface {
	Emit(context.Context, Event) error
}
type EventSinkFunc func(context.Context, Event) error

func (f EventSinkFunc) Emit(ctx context.Context, e Event) error { return f(ctx, e) }

var (
	ErrNotFound      = errors.New("capsule control: not found")
	ErrDenied        = errors.New("capsule control: denied")
	ErrStale         = errors.New("capsule control: stale handle")
	ErrLeaseConflict = errors.New("capsule control: lease conflict")
	ErrInvalidState  = errors.New("capsule control: invalid state")
)

func normalizeStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
