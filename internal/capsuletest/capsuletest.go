// Package capsuletest provides test lifecycle helpers for opening deterministic
// synthetic capsules under t.TempDir().
package capsuletest

import (
	"context"
	"testing"

	"kitsoki/internal/capsule"
)

type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	TempDir() string
	Cleanup(func())
}

func Open(t TB, name string) string {
	t.Helper()
	res, err := capsule.Open(context.Background(), name, capsule.OpenOptions{Dest: t.TempDir()})
	if err != nil {
		t.Fatalf("capsuletest.Open(%q): %v", name, err)
	}
	t.Cleanup(func() {
		_ = capsule.Close(res.Manifest.Workspace)
	})
	return res.Manifest.Workspace
}

func Verify(t *testing.T, workspace string) capsule.VerifyResult {
	t.Helper()
	res, err := capsule.Verify(context.Background(), workspace, nil)
	if err != nil {
		t.Fatalf("capsuletest.Verify(%q): %v", workspace, err)
	}
	if !res.OK {
		t.Fatalf("capsuletest.Verify(%q) failed: %v", workspace, res.Errors)
	}
	return res
}
