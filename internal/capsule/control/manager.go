package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var instanceIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// Manager owns definition resolution, instance leases, and provider lifecycle.
// It is the only component that knows machine paths behind opaque handles.
type Manager struct {
	Definitions DefinitionStore
	Instances   InstanceStore
	Providers   map[string]WorkspaceProvider
	Grant       ScopeGrant
	Events      EventSink
	Now         func() time.Time
}

type CreateRequest struct{ ID, DefinitionID, Owner, Provider string }

func (m *Manager) Create(ctx context.Context, req CreateRequest) (Handle, error) {
	if err := m.ready(); err != nil {
		return Handle{}, err
	}
	if !instanceIDPattern.MatchString(req.ID) {
		return Handle{}, fmt.Errorf("capsule control: invalid instance id %q", req.ID)
	}
	if strings.TrimSpace(req.Owner) == "" {
		return Handle{}, fmt.Errorf("capsule control: owner is required")
	}
	if m.Grant.Owner != "" && req.Owner != m.Grant.Owner {
		return Handle{}, fmt.Errorf("%w: lease owner", ErrDenied)
	}
	if m.Grant.Owner != "" && !m.Grant.Allows("effect", "workspace_manage") {
		return Handle{}, fmt.Errorf("%w: workspace_manage", ErrDenied)
	}
	if !m.Grant.Allows("definition", req.DefinitionID) {
		return Handle{}, fmt.Errorf("%w: definition %q", ErrDenied, req.DefinitionID)
	}
	def, err := m.Definitions.Get(ctx, req.DefinitionID)
	if err != nil {
		return Handle{}, err
	}
	providerName := req.Provider
	if providerName == "" {
		providerName = string(def.Source.Kind)
	}
	if !m.Grant.Allows("executor", providerName) {
		return Handle{}, fmt.Errorf("%w: executor %q", ErrDenied, providerName)
	}
	provider := m.Providers[providerName]
	if provider == nil {
		return Handle{}, fmt.Errorf("capsule control: provider %q is not configured", providerName)
	}
	if existing, err := m.Instances.Get(ctx, req.ID); err == nil {
		if existing.Lease.Owner != req.Owner || existing.DefinitionID != def.ID {
			return Handle{}, fmt.Errorf("%w: instance %q", ErrLeaseConflict, req.ID)
		}
		if existing.State == StateClosed {
			return Handle{}, fmt.Errorf("%w: instance %q is closed", ErrInvalidState, req.ID)
		}
		// Self-heal: the on-disk workspace can go missing out from under the
		// instance store (a manual `rm -rf`, a caller-side `git worktree
		// prune`, an aborted create) while metadata still records the
		// instance as Ready at its old path. A bare "already exists"
		// short-circuit here would hand back a handle pointing at a path
		// that no longer exists, and every subsequent op against it (sync,
		// agent writes, git commit) fails with an opaque "chdir: no such
		// file or directory" instead of the caller's `create` ever getting a
		// chance to rebuild it. Providers that durably mark their own
		// materialized output (WorkspaceMaterializationVerifier — both
		// git-backed providers write the `.kitsoki-capsule` sentinel) get a
		// verify-before-trust check here; providers that don't implement it
		// (e.g. a synthetic/in-memory provider with no on-disk contract)
		// keep the historical trust-the-store short-circuit untouched, since
		// "no directory" carries no meaning for them.
		if verifier, ok := provider.(WorkspaceMaterializationVerifier); ok && !verifier.WorkspaceMaterialized(existing) {
			_ = m.emit(ctx, "capsule.workspace.materializing", existing)
			materialized, createErr := provider.Create(ctx, def, existing)
			if createErr != nil {
				_, _ = m.Instances.CompareAndSwap(ctx, existing.ID, existing.Generation, func(cur *Instance) error { cur.State = StateFailed; return nil })
				_ = m.emit(ctx, "capsule.workspace.failed", existing)
				return Handle{}, createErr
			}
			existing, err = m.Instances.CompareAndSwap(ctx, existing.ID, existing.Generation, func(cur *Instance) error {
				cur.Path = materialized.Path
				cur.SourceRef = materialized.SourceRef
				cur.Head = materialized.Head
				cur.Branch = materialized.Branch
				cur.VerifierOverlays = append([]OverlayRef(nil), materialized.VerifierOverlays...)
				cur.State = StateReady
				return nil
			})
			if err != nil {
				return Handle{}, err
			}
			_ = m.emit(ctx, "capsule.workspace.ready", existing)
		}
		return Handle{ID: existing.ID, Generation: existing.Generation}, nil
	} else if !strings.Contains(err.Error(), ErrNotFound.Error()) {
		return Handle{}, err
	}
	root, err := m.workspaceRoot(def)
	if err != nil {
		return Handle{}, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return Handle{}, fmt.Errorf("capsule control: create workspace root: %w", err)
	}
	path, err := ResolveWorkspacePath(root, req.ID, false)
	if err != nil {
		return Handle{}, err
	}
	now := m.now()
	in, err := m.Instances.Create(ctx, Instance{ID: req.ID, DefinitionID: def.ID, DefinitionDigest: def.Digest, Provider: provider.Name(), Path: path, State: StateMaterializing, Lease: Lease{Owner: req.Owner, Acquired: now}, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return Handle{}, err
	}
	_ = m.emit(ctx, "capsule.workspace.materializing", in)
	materialized, err := provider.Create(ctx, def, in)
	if err != nil {
		_, _ = m.Instances.CompareAndSwap(ctx, in.ID, in.Generation, func(cur *Instance) error { cur.State = StateFailed; return nil })
		_ = m.emit(ctx, "capsule.workspace.failed", in)
		return Handle{}, err
	}
	in, err = m.Instances.CompareAndSwap(ctx, in.ID, in.Generation, func(cur *Instance) error {
		cur.Path = materialized.Path
		cur.SourceRef = materialized.SourceRef
		cur.Head = materialized.Head
		cur.Branch = materialized.Branch
		cur.VerifierOverlays = append([]OverlayRef(nil), materialized.VerifierOverlays...)
		cur.State = StateReady
		return nil
	})
	if err != nil {
		return Handle{}, err
	}
	_ = m.emit(ctx, "capsule.workspace.ready", in)
	return Handle{ID: in.ID, Generation: in.Generation}, nil
}

func (m *Manager) workspaceRoot(def Definition) (string, error) {
	root := m.Grant.WorkspaceRoots[0]
	if def.Source.Kind != SourceDevWorkspaceScript || strings.TrimSpace(def.Source.Development.Root) == "" {
		return root, nil
	}
	candidate, err := filepath.Abs(filepath.Join(m.Grant.ProjectRoot, def.Source.Development.Root))
	if err != nil {
		return "", err
	}
	for _, allowed := range m.Grant.WorkspaceRoots {
		if filepath.Clean(candidate) == filepath.Clean(allowed) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%w: development workspace root %q", ErrDenied, def.Source.Development.Root)
}

func (m *Manager) Status(ctx context.Context, h Handle) (Instance, error) {
	if err := m.ready(); err != nil {
		return Instance{}, err
	}
	in, err := m.Instances.Get(ctx, h.ID)
	if err != nil {
		return Instance{}, err
	}
	if in.Generation != h.Generation {
		return Instance{}, fmt.Errorf("%w: instance %q", ErrStale, h.ID)
	}
	if m.Grant.Owner != "" && in.Lease.Owner != m.Grant.Owner {
		return Instance{}, fmt.Errorf("%w: instance %q is owned by another lease", ErrDenied, h.ID)
	}
	return in, nil
}

// StatusOwned verifies both opaque-handle freshness and lease ownership. It is
// the common guard for front doors that do not already bind an owner into the
// manager's immutable ScopeGrant.
func (m *Manager) StatusOwned(ctx context.Context, h Handle, owner string) (Instance, error) {
	if strings.TrimSpace(owner) == "" {
		return Instance{}, fmt.Errorf("capsule control: owner is required")
	}
	in, err := m.Status(ctx, h)
	if err != nil {
		return Instance{}, err
	}
	if in.Lease.Owner != owner {
		return Instance{}, fmt.Errorf("%w: instance %q is owned by another lease", ErrDenied, h.ID)
	}
	return in, nil
}

// List returns sanitized instance metadata; Instance.Path is intentionally
// omitted from its JSON representation and remains manager authority.
func (m *Manager) List(ctx context.Context) ([]Instance, error) {
	if err := m.ready(); err != nil {
		return nil, err
	}
	return m.Instances.List(ctx)
}

func (m *Manager) Definition(ctx context.Context, id string) (Definition, error) {
	if err := m.ready(); err != nil {
		return Definition{}, err
	}
	if !m.Grant.Allows("definition", id) {
		return Definition{}, fmt.Errorf("%w: definition %q", ErrDenied, id)
	}
	return m.Definitions.Get(ctx, id)
}

func (m *Manager) Close(ctx context.Context, h Handle, owner string) error {
	in, err := m.Status(ctx, h)
	if err != nil {
		return err
	}
	if in.Lease.Owner != owner {
		return fmt.Errorf("%w: instance %q", ErrLeaseConflict, h.ID)
	}
	if m.Grant.Owner != "" && !m.Grant.Allows("effect", "workspace_manage") {
		return fmt.Errorf("%w: workspace_manage", ErrDenied)
	}
	switch in.State {
	case StateMaterializing, StateFailed:
		if err := m.removeIncompleteWorkspace(in); err != nil {
			return err
		}
	default:
		provider := m.Providers[in.Provider]
		if provider == nil {
			return fmt.Errorf("capsule control: provider %q is not configured", in.Provider)
		}
		if err := provider.Close(ctx, in); err != nil {
			return err
		}
	}
	in, err = m.Instances.CompareAndSwap(ctx, h.ID, h.Generation, func(cur *Instance) error { cur.State = StateClosed; return nil })
	if err != nil {
		return err
	}
	return m.emit(ctx, "capsule.workspace.closed", in)
}

func (m *Manager) removeIncompleteWorkspace(in Instance) error {
	if strings.TrimSpace(in.Path) == "" {
		return nil
	}
	path := in.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	for _, root := range m.Grant.WorkspaceRoots {
		cleanRoot := root
		if real, err := filepath.EvalSymlinks(cleanRoot); err == nil {
			cleanRoot = real
		}
		if rel, err := filepath.Rel(cleanRoot, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return os.RemoveAll(path)
		}
	}
	return fmt.Errorf("%w: incomplete instance path is outside grant", ErrDenied)
}

func (m *Manager) ready() error {
	if m.Definitions == nil || m.Instances == nil {
		return fmt.Errorf("capsule control: definitions and instances are required")
	}
	if err := m.Grant.Validate(); err != nil {
		return err
	}
	return nil
}
func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}
	return time.Now().UTC()
}
func (m *Manager) emit(ctx context.Context, kind string, in Instance) error {
	if m.Events == nil {
		return nil
	}
	fields := map[string]any{"definition_id": in.DefinitionID, "definition_digest": in.DefinitionDigest, "provider": in.Provider, "state": in.State, "lease_owner": in.Lease.Owner, "source_ref": in.SourceRef, "head": in.Head}
	if len(in.VerifierOverlays) > 0 {
		fields["verifier_overlays"] = in.VerifierOverlays
	}
	return m.Events.Emit(ctx, Event{Kind: kind, InstanceID: in.ID, Generation: in.Generation, Fields: fields})
}

// WorkspacePath is intentionally package-private authority exposed only to
// same-process providers/front doors after lease and generation verification.
func (m *Manager) WorkspacePath(ctx context.Context, h Handle) (string, error) {
	in, err := m.Status(ctx, h)
	if err != nil {
		return "", err
	}
	path := in.Path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	for _, root := range m.Grant.WorkspaceRoots {
		if real, err := filepath.EvalSymlinks(root); err == nil {
			root = real
		}
		if rel, e := filepath.Rel(root, path); e == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel) {
			return path, nil
		}
	}
	return "", fmt.Errorf("%w: instance path is outside grant", ErrDenied)
}

// VerifierOverlayPaths resolves verifier-only overlay refs to absolute paths
// for same-process verifier execution. It is intentionally not used by the
// agent-facing FS/MCP tools: those surfaces only see OverlayRef digests.
func (m *Manager) VerifierOverlayPaths(ctx context.Context, h Handle) ([]string, error) {
	in, err := m.Status(ctx, h)
	if err != nil {
		return nil, err
	}
	def, err := m.Definitions.Get(ctx, in.DefinitionID)
	if err != nil {
		return nil, err
	}
	root, err := projectRoot(m.Grant.ProjectRoot)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, overlay := range def.Overlays {
		if overlay.Visibility != "verifier" {
			continue
		}
		path, err := projectRelativePath(root, overlay.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, nil
}
