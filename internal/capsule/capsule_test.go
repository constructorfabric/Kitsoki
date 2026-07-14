package capsule

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenVerifySyntheticCleanRepo(t *testing.T) {
	res, err := Open(context.Background(), "clean-repo", OpenOptions{Dest: t.TempDir()})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if res.Manifest.TreeDigest == "" {
		t.Fatal("manifest tree digest is empty")
	}
	if _, err := os.Stat(filepath.Join(res.Manifest.Workspace, "a.txt")); err != nil {
		t.Fatalf("missing fixture file: %v", err)
	}
	vr, err := Verify(context.Background(), res.Manifest.Workspace, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !vr.OK {
		t.Fatalf("Verify failed: %v", vr.Errors)
	}
}

func TestCloseRefusesNonCapsule(t *testing.T) {
	dir := t.TempDir()
	if err := Close(dir); err == nil {
		t.Fatal("Close should refuse a directory without capsule sentinel")
	}
}
