package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenScopedBuildsExactImmutableGrant(t *testing.T) {
	root := t.TempDir()
	writeLegacyDefinition(t, root, "one")
	writeLegacyDefinition(t, root, "two")

	manager, err := OpenScoped(root, ScopeOptions{
		Owner:       "agent-a",
		Definitions: []string{"one"},
		Effects:     []string{"fs_write", "workspace_manage", "fs_write"},
		Branches:    []string{"staging/local", "staging/local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	grant := manager.Grant
	if grant.Owner != "agent-a" {
		t.Fatalf("owner = %q", grant.Owner)
	}
	if len(grant.Definitions) != 1 || grant.Definitions[0] != "one" {
		t.Fatalf("definitions = %#v", grant.Definitions)
	}
	if len(grant.Executors) != 1 || grant.Executors[0] != "synthetic" {
		t.Fatalf("executors = %#v", grant.Executors)
	}
	if len(grant.Effects) != 2 || grant.Effects[0] != "fs_write" || grant.Effects[1] != "workspace_manage" {
		t.Fatalf("effects = %#v", grant.Effects)
	}
	if len(grant.Branches) != 1 || grant.Branches[0] != "staging/local" {
		t.Fatalf("branches = %#v", grant.Branches)
	}
	if _, err := manager.Definition(t.Context(), "two"); err == nil {
		t.Fatal("ungranted definition was inspectable")
	}
}

func TestOpenScopedRejectsUnknownAuthority(t *testing.T) {
	root := t.TempDir()
	writeLegacyDefinition(t, root, "one")
	if _, err := OpenScoped(root, ScopeOptions{Definitions: []string{"missing"}}); err == nil {
		t.Fatal("missing definition was accepted")
	}
	if _, err := OpenScoped(root, ScopeOptions{Effects: []string{"filesystem_everywhere"}}); err == nil {
		t.Fatal("unknown effect was accepted")
	}
}

func TestOpenTrustedManagerIncludesAllProductEffects(t *testing.T) {
	root := t.TempDir()
	writeLegacyDefinition(t, root, "one")
	manager, err := Open(root, []string{"main"})
	if err != nil {
		t.Fatal(err)
	}
	for _, effect := range []string{"workspace_manage", "fs_write", "exec", "vcs_commit", "local_reconcile", "ci_run", "env_write", "cleanup"} {
		if !manager.Grant.Allows("effect", effect) {
			t.Fatalf("trusted manager missing %q: %#v", effect, manager.Grant.Effects)
		}
	}
	for _, effect := range []string{"raw_exec", "remote_publish"} {
		if manager.Grant.Allows("effect", effect) {
			t.Fatalf("trusted manager unexpectedly includes high-risk effect %q: %#v", effect, manager.Grant.Effects)
		}
	}
}

func writeLegacyDefinition(t *testing.T, root, id string) {
	t.Helper()
	path := filepath.Join(root, "capsules", id, "capsule.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("name: " + id + "\nsource:\n  synthetic: true\n  steps:\n    - action: write\n      path: seed.txt\n      content: seed\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
