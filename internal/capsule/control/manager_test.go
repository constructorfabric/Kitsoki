package control

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type defs map[string]Definition

func (d defs) Get(_ context.Context, id string) (Definition, error) {
	v, ok := d[id]
	if !ok {
		return Definition{}, ErrNotFound
	}
	return v, nil
}
func (d defs) List(context.Context) ([]Definition, error) { return nil, nil }

type provider struct {
	name  string
	calls int
}

func (p *provider) Name() string { return p.name }
func (p *provider) Create(_ context.Context, _ Definition, in Instance) (MaterializedWorkspace, error) {
	p.calls++
	return MaterializedWorkspace{Path: in.Path, Head: "abc"}, nil
}
func (p *provider) Close(context.Context, Instance) error { return nil }

func TestManagerLeaseIdempotencyAndStaleHandle(t *testing.T) {
	root := t.TempDir()
	p := &provider{name: "synthetic"}
	store := NewMemoryInstanceStore()
	m := &Manager{Definitions: defs{"clean": {ID: "clean", Schema: DefinitionSchema, Source: Source{Kind: SourceSynthetic, SyntheticSpec: "x"}, Digest: "sha256:x"}}, Instances: store, Providers: map[string]WorkspaceProvider{"synthetic": p}, Grant: ScopeGrant{ProjectRoot: root, WorkspaceRoots: []string{filepath.Join(root, ".capsules")}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}}}
	ctx := context.Background()
	first, err := m.Create(ctx, CreateRequest{ID: "one", DefinitionID: "clean", Owner: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.Create(ctx, CreateRequest{ID: "one", DefinitionID: "clean", Owner: "agent"})
	if err != nil || again != first || p.calls != 1 {
		t.Fatalf("idempotency = %#v %v calls=%d", again, err, p.calls)
	}
	if _, err := m.Create(ctx, CreateRequest{ID: "one", DefinitionID: "clean", Owner: "other"}); err == nil {
		t.Fatal("different lease owner succeeded")
	}
	if _, err := store.CompareAndSwap(ctx, "one", first.Generation, func(*Instance) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Status(ctx, first); err == nil {
		t.Fatal("stale handle succeeded")
	} else if got := fmt.Sprint(err); got == "" {
		t.Fatal("missing stale error")
	}
}

func TestManagerOwnerBoundGrantRejectsGuessedHandle(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, ".capsules", "workspaces")
	p := &provider{name: "synthetic"}
	store := NewMemoryInstanceStore()
	base := &Manager{
		Definitions: defs{"clean": {ID: "clean", Schema: DefinitionSchema, Source: Source{Kind: SourceSynthetic, SyntheticSpec: "x"}, Digest: "sha256:x"}},
		Instances:   store,
		Providers:   map[string]WorkspaceProvider{"synthetic": p},
		Grant:       ScopeGrant{Owner: "owner-a", ProjectRoot: root, WorkspaceRoots: []string{workspaceRoot}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}, Effects: []string{"workspace_manage"}},
	}
	h, err := base.Create(context.Background(), CreateRequest{ID: "owned", DefinitionID: "clean", Owner: "owner-a"})
	if err != nil {
		t.Fatal(err)
	}
	other := *base
	other.Grant = base.Grant.Clone()
	other.Grant.Owner = "owner-b"
	if _, err := other.Status(context.Background(), h); err == nil || !strings.Contains(err.Error(), ErrDenied.Error()) {
		t.Fatalf("other owner status error = %v, want denied", err)
	}
	if _, err := base.StatusOwned(context.Background(), h, "owner-b"); err == nil || !strings.Contains(err.Error(), ErrDenied.Error()) {
		t.Fatalf("explicit other owner status error = %v, want denied", err)
	}
	if _, err := other.Create(context.Background(), CreateRequest{ID: "wrong-owner", DefinitionID: "clean", Owner: "owner-a"}); err == nil || !strings.Contains(err.Error(), ErrDenied.Error()) {
		t.Fatalf("mismatched create owner error = %v, want denied", err)
	}
	readOnly := *base
	readOnly.Grant = base.Grant.Clone()
	readOnly.Grant.Effects = nil
	if _, err := readOnly.Create(context.Background(), CreateRequest{ID: "no-capability", DefinitionID: "clean", Owner: "owner-a"}); err == nil || !strings.Contains(err.Error(), "workspace_manage") {
		t.Fatalf("read-only scoped create error = %v, want workspace_manage denial", err)
	}
}

func TestManagerUsesOnlyGrantedDevelopmentWorkspaceRoot(t *testing.T) {
	project := t.TempDir()
	defaultRoot := filepath.Join(project, ".capsules", "workspaces")
	stagingRoot := filepath.Join(project, ".capsules", "staging")
	m := &Manager{Grant: ScopeGrant{ProjectRoot: project, WorkspaceRoots: []string{defaultRoot, stagingRoot}}}
	got, err := m.workspaceRoot(Definition{Source: Source{Kind: SourceDevWorkspaceScript, Development: DevelopmentSource{Root: ".capsules/staging"}}})
	if err != nil || got != stagingRoot {
		t.Fatalf("workspace root %q: %v", got, err)
	}
	if _, err := m.workspaceRoot(Definition{Source: Source{Kind: SourceDevWorkspaceScript, Development: DevelopmentSource{Root: ".capsules/not-granted"}}}); err == nil {
		t.Fatal("ungranted development root was accepted")
	}
}

func TestManagerCloseRemovesIncompleteFailedWorkspaceWithoutProviderSentinel(t *testing.T) {
	root := t.TempDir()
	workspaceRoot := filepath.Join(root, ".capsules", "workspaces")
	path := filepath.Join(workspaceRoot, "partial")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "partial.txt"), []byte("leftover"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := NewMemoryInstanceStore()
	created, err := store.Create(context.Background(), Instance{
		ID:               "partial",
		DefinitionID:     "clean",
		DefinitionDigest: "sha256:def",
		Provider:         "synthetic",
		Path:             path,
		State:            StateFailed,
		Generation:       1,
		Lease:            Lease{Owner: "agent"},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &provider{name: "synthetic"}
	m := &Manager{
		Definitions: defs{"clean": {ID: "clean", Schema: DefinitionSchema, Source: Source{Kind: SourceSynthetic, SyntheticSpec: "x"}, Digest: "sha256:def"}},
		Instances:   store,
		Providers:   map[string]WorkspaceProvider{"synthetic": p},
		Grant:       ScopeGrant{ProjectRoot: root, WorkspaceRoots: []string{workspaceRoot}, Definitions: []string{"clean"}, Executors: []string{"synthetic"}},
	}
	if err := m.Close(context.Background(), Handle{ID: created.ID, Generation: created.Generation}, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial workspace still exists or unexpected stat error: %v", err)
	}
	if p.calls != 0 {
		t.Fatalf("provider close/create should not be called for incomplete cleanup, calls=%d", p.calls)
	}
	closed, err := store.Get(context.Background(), "partial")
	if err != nil {
		t.Fatal(err)
	}
	if closed.State != StateClosed {
		t.Fatalf("state=%s", closed.State)
	}
}
