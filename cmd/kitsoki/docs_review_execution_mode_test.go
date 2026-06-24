package main

// End-to-end verification for the execution-modes engine change
// (docs/proposals/execution-modes-and-gate-deciders.md) against the REAL
// docs-review story driven through the needs_update host cassette.
//
//   - one-shot (happy_needs_update.yaml): idle → reviewing → reviewed →
//     fixing → fixed collapses into a single turn (historical behaviour).
//   - staged   (staged_needs_update.yaml): the chain STOPS at `reviewed`
//     (a decision gate); the operator then picks fix_docs to advance.
//
// Both fixtures share cassettes/needs_update.cassette.yaml; the only
// difference is the fixture's `mode:` field.

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kitsoki/internal/testrunner"
)

func runDocsReviewFlow(t *testing.T, fixture string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("KITSOKI_ARTIFACTS_ROOT", root)

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	appPath := filepath.Join(repoRoot, "stories", "docs-review", "app.yaml")
	flowGlob := filepath.Join(repoRoot, "stories", "docs-review", "flows", fixture)

	report, err := testrunner.RunFlows(context.Background(), appPath, flowGlob, testrunner.FlowOptions{})
	if err != nil {
		t.Fatalf("RunFlows(%s): %v", fixture, err)
	}
	if report.Failed > 0 {
		for _, fr := range report.Results {
			if fr.Passed {
				continue
			}
			for _, tr := range fr.Turns {
				if len(tr.Failures) == 0 {
					continue
				}
				t.Errorf("flow %s turn %d:\n  %s", fr.File, tr.TurnIndex, strings.Join(tr.Failures, "\n  "))
			}
		}
	}
	if report.Passed == 0 {
		t.Fatalf("flow %s did not run", fixture)
	}
}

// TestDocsReview_OneShotWalksToFixed is the control: one-shot mode
// auto-advances the whole pipeline to `fixed` in one turn.
func TestDocsReview_OneShotWalksToFixed(t *testing.T) {
	runDocsReviewFlow(t, "happy_needs_update.yaml")
}

// TestDocsReview_StagedStopsAtReviewed is the behaviour under test:
// staged mode ends the turn at the `reviewed` decision gate, then the
// operator's explicit fix_docs advances to `fixed`.
func TestDocsReview_StagedStopsAtReviewed(t *testing.T) {
	runDocsReviewFlow(t, "staged_needs_update.yaml")
}
