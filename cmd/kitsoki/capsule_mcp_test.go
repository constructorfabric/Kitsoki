package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewCapsuleMCPManagerCompilesRequestedGrant(t *testing.T) {
	root := t.TempDir()
	writeCapsuleMCPDefinition(t, root)
	manager, projectID, err := newCapsuleMCPManager(root, "agent-a", []string{"staging/local"}, []string{"workspace_manage", "fs_write", "cleanup"})
	if err != nil {
		t.Fatal(err)
	}
	if projectID != filepath.Base(root) {
		t.Fatalf("project id = %q", projectID)
	}
	grant := manager.Grant
	if grant.Owner != "agent-a" {
		t.Fatalf("owner = %q", grant.Owner)
	}
	if len(grant.Branches) != 1 || grant.Branches[0] != "staging/local" {
		t.Fatalf("branches = %#v", grant.Branches)
	}
	for _, effect := range []string{"workspace_manage", "fs_write", "cleanup"} {
		if !grant.Allows("effect", effect) {
			t.Fatalf("missing effect %q in %#v", effect, grant.Effects)
		}
	}
	if grant.Allows("effect", "exec") || grant.Allows("effect", "env_write") {
		t.Fatalf("unrequested effects were granted: %#v", grant.Effects)
	}
}

func TestNewCapsuleMCPManagerRejectsInvalidOwnerAndEffect(t *testing.T) {
	root := t.TempDir()
	writeCapsuleMCPDefinition(t, root)
	if _, _, err := newCapsuleMCPManager(root, "", nil, []string{"workspace_manage"}); err == nil {
		t.Fatal("empty owner was accepted")
	}
	if _, _, err := newCapsuleMCPManager(root, "agent", nil, []string{"host_filesystem"}); err == nil {
		t.Fatal("unknown effect was accepted")
	}
}

func TestDefaultCapsuleMCPEffectsSupportLeastAuthorityCoding(t *testing.T) {
	want := map[string]bool{
		"workspace_manage": true,
		"fs_write":         true,
		"exec":             true,
		"vcs_commit":       true,
		"ci_run":           true,
	}
	for _, effect := range defaultCapsuleMCPEffects() {
		delete(want, effect)
	}
	if len(want) != 0 {
		t.Fatalf("default profile missing effects: %#v", want)
	}
}

func writeCapsuleMCPDefinition(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "capsules", "clean", "capsule.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte("name: clean\nsource:\n  synthetic: true\n  steps:\n    - action: write\n      path: initial.txt\n      content: initial\n")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
