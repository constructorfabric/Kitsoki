package control

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveWorkspacePathConfinesSymlinksAndTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveWorkspacePath(root, "inside.txt", true); err != nil || got != filepath.Join(realRoot, "inside.txt") {
		t.Fatalf("inside = %q, %v", got, err)
	}
	for _, path := range []string{"../outside", "/tmp/outside", "escape/file.txt"} {
		if _, err := ResolveWorkspacePath(root, path, false); err == nil {
			t.Fatalf("%q unexpectedly escaped", path)
		}
	}
}

func TestScopeGrantDeniesByDefault(t *testing.T) {
	root := t.TempDir()
	grant := ScopeGrant{ProjectRoot: root, WorkspaceRoots: []string{filepath.Join(root, ".capsules", "workspaces")}, Definitions: []string{"clean"}}
	if err := grant.Validate(); err != nil {
		t.Fatal(err)
	}
	if !grant.Allows("definition", "clean") || grant.Allows("effect", "remote_write") {
		t.Fatalf("grant unexpectedly widened: %#v", grant)
	}
}
