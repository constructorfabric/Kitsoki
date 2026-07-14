package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/capsule/ci"
	"kitsoki/internal/capsule/control"
	"kitsoki/internal/capsule/executor"
	"kitsoki/internal/capsule/hygiene"
)

func TestCapsuleCleanupScopePinsAndHidesOtherOwners(t *testing.T) {
	root := t.TempDir()
	store := control.NewMemoryInstanceStore()
	for _, instance := range []control.Instance{
		{ID: "mine", Lease: control.Lease{Owner: "owner-a"}},
		{ID: "theirs", Lease: control.Lease{Owner: "owner-b"}},
	} {
		if _, err := store.Create(context.Background(), instance); err != nil {
			t.Fatal(err)
		}
	}
	server, err := NewCapsuleServer(CapsuleConfig{
		Manager: &control.Manager{
			Instances: store,
			Grant: control.ScopeGrant{
				ProjectRoot:    root,
				WorkspaceRoots: []string{filepath.Join(root, ".capsules", "workspaces")},
				Effects:        []string{"cleanup"},
			},
		},
		Owner: "owner-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	opts, err := server.cleanupOptions(context.Background(), capsuleCleanupArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.PinnedWorkspaceIDs) != 1 || opts.PinnedWorkspaceIDs[0] != "theirs" {
		t.Fatalf("pinned workspaces = %#v", opts.PinnedWorkspaceIDs)
	}

	projected, err := server.projectCleanupCandidates([]hygiene.Candidate{
		{ID: "mine", Kind: "workspace", Path: ".capsules/workspaces/mine", Owner: "owner-a"},
		{ID: "theirs", Kind: "workspace", Path: ".capsules/workspaces/theirs", Owner: "owner-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 1 || projected[0].ID != "mine" {
		t.Fatalf("projected cleanup candidates = %#v", projected)
	}
}

func TestCapsuleConfiguredExecutorsBundlesExactWorkspaceSource(t *testing.T) {
	workspace := t.TempDir()
	runCapsuleSecurityGit(t, workspace, "init")
	runCapsuleSecurityGit(t, workspace, "config", "user.name", "Capsule Test")
	runCapsuleSecurityGit(t, workspace, "config", "user.email", "capsule@example.test")
	if err := os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("exact source\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCapsuleSecurityGit(t, workspace, "add", "source.txt")
	runCapsuleSecurityGit(t, workspace, "commit", "-m", "source")
	head := runCapsuleSecurityGit(t, workspace, "rev-parse", "HEAD")

	configured := capsuleConfiguredExecutors(ci.Config{}, workspace)
	if configured.Source == nil {
		t.Fatal("MCP configured executors are missing the source bundler")
	}
	bundle, err := configured.Source.Bundle(context.Background(), executor.Envelope{SourceDigest: head})
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Head != head {
		t.Fatalf("bundle head = %q, want %q", bundle.Head, head)
	}
	if err := executor.ValidateSourceBundle(bundle, 0); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dirty.txt"), []byte("not sealed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := configured.Source.Bundle(context.Background(), executor.Envelope{SourceDigest: head}); err == nil {
		t.Fatal("dirty workspace was bundled for remote execution")
	}
}

func runCapsuleSecurityGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
