package userfacing_test

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"kitsoki/internal/userfacing"
)

// These tests encode the user-facing contract independent of any story, so only
// a real implementation of userfacing.Error (an engine change) satisfies them.

func TestError_Nil(t *testing.T) {
	if got := userfacing.Error(nil); got != "" {
		t.Fatalf("Error(nil) = %q, want \"\"", got)
	}
}

func TestError_StripsAbsolutePaths(t *testing.T) {
	err := fmt.Errorf("write %s: %w", "/Users/brad/.cache/kitsoki/har.json", os.ErrPermission)
	got := userfacing.Error(err)
	for _, bad := range []string{"/Users/brad", "/Users/", "/var/folders", "/.cache/"} {
		if strings.Contains(got, bad) {
			t.Errorf("Error(...) = %q, must not leak absolute path fragment %q", got, bad)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(got), "/") {
		t.Errorf("Error(...) = %q, must not start with an absolute path", got)
	}
}

func TestError_NoFormattingArtifacts(t *testing.T) {
	// A chain built with %w/%v and a nil-ish inner must never surface raw verbs.
	err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", errors.New("the cause")))
	got := userfacing.Error(err)
	for _, bad := range []string{"%w", "%!", "%v", "<nil>"} {
		if strings.Contains(got, bad) {
			t.Errorf("Error(...) = %q, must not contain formatting artifact %q", got, bad)
		}
	}
}

func TestError_PreservesActionableLeaf(t *testing.T) {
	err := fmt.Errorf("write har.json: %w", os.ErrPermission)
	got := userfacing.Error(err)
	if !strings.Contains(strings.ToLower(got), "permission denied") {
		t.Errorf("Error(...) = %q, want it to preserve the leaf cause %q", got, "permission denied")
	}
}

func TestError_StripsInternalWrapperPrefixes(t *testing.T) {
	err := fmt.Errorf("orchestrator: SubmitDirect: machine.Turn: %w", errors.New("boom"))
	got := userfacing.Error(err)
	for _, bad := range []string{"machine.Turn", "SubmitDirect", "orchestrator:"} {
		if strings.Contains(got, bad) {
			t.Errorf("Error(...) = %q, must not leak internal wrapper %q", got, bad)
		}
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("Error(...) = %q, want it to preserve the leaf cause %q", got, "boom")
	}
}

func TestError_Deterministic(t *testing.T) {
	mk := func() error {
		return fmt.Errorf("load %s: %w", "/tmp/x/story.yaml", errors.New("not found"))
	}
	if a, b := userfacing.Error(mk()), userfacing.Error(mk()); a != b {
		t.Errorf("Error not deterministic: %q vs %q", a, b)
	}
}
