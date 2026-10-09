package executor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/capsuletest"
)

func TestGitBundleCarriesExactCleanCapsuleHead(t *testing.T) {
	root := capsuletest.Open(t, "clean-repo")
	head := strings.TrimSpace(commandOutput(t, root, "rev-parse", "HEAD"))
	bundle, err := GitBundle(context.Background(), root, head, 0)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Head != head || bundle.Size == 0 || len(bundle.Data) == 0 {
		t.Fatalf("bundle = %+v", bundle)
	}
	if err := ValidateSourceBundle(bundle, 0); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "source.bundle")
	if err := os.WriteFile(path, bundle.Data, 0o600); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(dir, "clone")
	cmd := exec.Command("git", "clone", "--quiet", path, clone)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone bundle: %v: %s", err, out)
	}
	if got := strings.TrimSpace(commandOutput(t, clone, "rev-parse", "HEAD")); got != head {
		t.Fatalf("materialized HEAD = %q, want %q", got, head)
	}
}

func TestGitBundleRejectsUnsealedWorkspaceBytes(t *testing.T) {
	root := capsuletest.Open(t, "clean-repo")
	head := strings.TrimSpace(commandOutput(t, root, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("not sealed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := GitBundle(context.Background(), root, head, 0)
	if err == nil || !strings.Contains(err.Error(), "uncommitted or untracked") {
		t.Fatalf("GitBundle error = %v", err)
	}
}

func TestValidateSourceBundleRejectsTampering(t *testing.T) {
	bundle := SourceBundle{Schema: SourceBundleSchema, Format: SourceBundleFormat, Head: strings.Repeat("a", 40), Digest: "sha256:bad", Size: 1, Data: []byte("x")}
	if err := ValidateSourceBundle(bundle, 0); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("ValidateSourceBundle error = %v", err)
	}
}

func commandOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	argv := append([]string{"-C", root}, args...)
	out, err := exec.Command("git", argv...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}
