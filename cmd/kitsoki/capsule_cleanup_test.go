package main

import (
	"testing"
	"time"
)

func TestCapsuleCleanupClearUsesFixedInactiveMergedPolicy(t *testing.T) {
	opts := capsuleClearInactiveOptions("fixture")
	if opts.ProjectRoot != "fixture" || !opts.ClearInactiveMerged {
		t.Fatalf("options=%#v", opts)
	}
	if opts.KeepRuns != -1 || opts.KeepWorkspaces != -1 || opts.MinWorkspaceAge != 5*time.Minute {
		t.Fatalf("clear retention/cooloff policy=%#v", opts)
	}
	if opts.IncludeCapsuleCache || opts.IncludeGoBuildCache || opts.MeasureWorkspaceBytes {
		t.Fatalf("clear must not touch caches or walk workspace bytes: %#v", opts)
	}
}
