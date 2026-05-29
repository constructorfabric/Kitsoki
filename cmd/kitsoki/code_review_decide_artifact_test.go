// code_review_decide_artifact_test.go — story-level integration test for
// the recurring "submitted-but-empty-artifact" bug class in code-review.
//
// The existing code-review flow fixtures (happy_human_approve, etc.) all
// stub iface.transport (host.append_to_file), so they never exercise the
// chain:
//
//   oracle.decide returns ok with Data["submitted"] = decision_artifact
//     → orchestrator binds `decision_artifact: submitted` into world
//     → decide_awaiting_reply.on_enter templates
//       `body: "{{ world.decision_artifact.summary_markdown }}"` into
//       iface.transport.post
//     → bodyArg pretty-prints map[string]any as JSON
//     → file on disk
//
// This test stubs host.oracle.decide inline (returning a fully-shaped
// decision_artifact with a sentinel string) but lets the real
// host.artifacts_dir handler run (bound to iface.transport via
// host_bindings:), then asserts the file on disk contains the sentinel.

package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kitsoki/internal/testrunner"
)

func TestCodeReviewDecide_ArtifactRoundTrip(t *testing.T) {
	// Real artifacts_dir writes under $KITSOKI_ARTIFACTS_ROOT.
	root := t.TempDir()
	t.Setenv("KITSOKI_ARTIFACTS_ROOT", root)

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	appPath := filepath.Join(repoRoot, "stories", "code-review", "app.yaml")
	flowGlob := filepath.Join(repoRoot, "stories", "code-review", "flows", "decide_artifact_round_trip.yaml")

	report, err := testrunner.RunFlows(context.Background(), appPath, flowGlob, testrunner.FlowOptions{})
	if err != nil {
		t.Fatalf("RunFlows: %v", err)
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

	// The thread value is "PR-99", so artifacts_dir writes PR-99.md.
	artifact := filepath.Join(root, "PR-99.md")
	body, readErr := os.ReadFile(artifact)
	if readErr != nil {
		t.Fatalf("artifact not written at %s: %v", artifact, readErr)
	}

	// Assert sentinel present — proves the bind→template→bodyArg chain
	// delivered the structured artifact content to disk.
	const sentinel = "CODEREVIEW-ROUND-TRIP-SENTINEL"
	if !strings.Contains(string(body), sentinel) {
		t.Errorf("artifact missing sentinel %q\n--- artifact body ---\n%s", sentinel, body)
	}

	// Guard against the two known silent-failure signatures.
	trimmed := strings.TrimSpace(codeReviewStripHeader(string(body)))
	if trimmed == "{}" {
		t.Errorf("artifact body is the silent-failure signature `{}`\n--- artifact body ---\n%s", body)
	}
	if strings.Contains(trimmed, "<map[") {
		t.Errorf("artifact body looks like a Go map stringification (no JSON pretty-print)\n--- artifact body ---\n%s", body)
	}
}

// codeReviewStripHeader trims the renderArtifact prefix ("### …\n\n") and
// footer ("\n\n_phase: …_") so the shape checks above operate on the body
// proper. Mirrors the helper in docs_review_artifact_test.go.
func codeReviewStripHeader(s string) string {
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[i+2:]
	}
	if j := strings.LastIndex(s, "\n\n_phase:"); j >= 0 {
		s = s[:j]
	}
	return s
}
