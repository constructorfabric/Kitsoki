// docs_review_artifact_test.go — story-level integration test for the
// recurring "submitted-but-empty-artifact" bug class.
//
// The cassette-driven fixtures (happy_needs_update, empty_submitted,
// agent_abandoned, happy_up_to_date) all stub host.artifacts_dir, so
// they never exercise the chain:
//
//   agent.decide returns ok with Data["submitted"] = verdict
//     → orchestrator binds `verdict: submitted` into world
//     → host.artifacts_dir's `body: "{{ world.verdict }}"` re-renders
//     → bodyArg pretty-prints map[string]any as JSON
//     → file on disk
//
// In a live run we observed the artifact landing as literal `{}` even
// though the agent had submitted a valid verdict. That means somewhere
// in the chain above, the map got lost or stringified to an empty
// representation. None of the cassette flows would have noticed —
// they assert state + world but never read the artifact file.
//
// This test stubs host.run and host.agent.decide inline (returning a
// fully-shaped verdict) but lets the real host.artifacts_dir handler
// run, then asserts the file on disk contains the verdict fields.

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kitsoki/internal/testrunner"
)

func TestDocsReview_ArtifactRoundTrip(t *testing.T) {
	// Real artifacts_dir writes under $KITSOKI_ARTIFACTS_ROOT.
	root := t.TempDir()
	t.Setenv("KITSOKI_ARTIFACTS_ROOT", root)

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	appPath := filepath.Join(repoRoot, "stories", "docs-review", "app.yaml")
	flowGlob := filepath.Join(repoRoot, "stories", "docs-review", "flows", "artifact_round_trip.yaml")

	report, err := testrunner.RunFlows(context.Background(), appPath, flowGlob, testrunner.FlowOptions{})
	if err != nil {
		t.Fatalf("RunFlows: %v", err)
	}
	if report.Failed > 0 {
		// Surface per-turn failures inline so a regression shows up
		// pointing at the specific assertion (state mismatch vs.
		// content_matches vs. file-not-exist).
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

	// Belt-and-braces: confirm the artifact landed and is the expected
	// shape, on top of the YAML content_matches assertions. This pins
	// the JSON pretty-print path used by transport_post.go's bodyArg —
	// a previous regression had it stringifying the map as
	// `<map[string]interface {} Value>`, which content_matches would
	// also catch, but the explicit check makes the failure cause
	// obvious in test output.
	// Filename now carries the review mode + .json extension (pure-JSON
	// artifact, not markdown-wrapped). Matches the thread template in
	// stories/docs-review/rooms/reviewing.yaml.
	artifact := filepath.Join(root, "docs-review-recent-abc1234.json")
	body, readErr := os.ReadFile(artifact)
	if readErr != nil {
		// List what IS in the artifacts root to make the failure
		// diagnosable when the filename template drifts.
		entries, _ := os.ReadDir(root)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("artifact not written at %s: %v\nroot contents: %v", artifact, readErr, names)
	}
	for _, want := range []string{
		`"decision": "needs_update"`,
		`"lines": "10-20"`,
		`"invalidations":`,
		`"commits":`,
		`"needs_update"`,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("artifact missing %q\n--- artifact body ---\n%s", want, body)
		}
	}
	// The smoking-gun shape we're guarding against: artifact body that
	// is literally `{}` or starts with `<map[`. The artifact is now
	// pure JSON (no markdown wrapper), so we can json.Unmarshal it
	// directly — anything other than a valid object is the failure.
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "{}" {
		t.Errorf("artifact body is the silent-failure signature `{}`\n--- artifact body ---\n%s", body)
	}
	if strings.Contains(trimmed, "<map[") {
		t.Errorf("artifact body looks like a Go map stringification (no JSON pretty-print)\n--- artifact body ---\n%s", body)
	}
	// Stronger contract: the file must parse as JSON and have the
	// required top-level fields. This is the load-bearing guarantee
	// for any downstream tool that consumes the artifact.
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("artifact body is not valid JSON: %v\n--- body ---\n%s", err, body)
	}
	for _, key := range []string{"decision", "summary", "confidence", "commits", "stale_docs"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("parsed artifact missing required key %q", key)
		}
	}
}
