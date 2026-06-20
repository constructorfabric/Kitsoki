// pr_refinement_diagnose_artifact_test.go — story-level integration test for
// the recurring "submitted-but-empty-artifact" bug class in pr-refinement.
//
// The existing pr-refinement flow fixtures (ci_fails_diagnose, happy_human,
// happy_llm_then_human, comments_round_trip) all stub iface.transport.post
// via host.append_to_file, so they never exercise the full chain:
//
//   agent.decide returns ok with Data["submitted"] = diagnose_artifact
//     → orchestrator binds `diagnose_artifact: submitted` into world
//     → iface.transport.post's `body: "{{ world.diagnose_artifact.summary_markdown }}"` re-renders
//     → bodyArg in artifacts_dir pretty-prints map[string]any as JSON
//     → file on disk
//
// This test rebinds iface.transport → host.artifacts_dir and stubs only
// host.agent.decide and host.inbox.add inline (returning a fully-shaped
// diagnose_artifact with a sentinel string), then asserts the file on disk
// contains the sentinel and is not the silent-failure signatures `{}` or
// `<map[`.

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

func TestPrRefinement_DiagnoseArtifactRoundTrip(t *testing.T) {
	// Real artifacts_dir writes under $KITSOKI_ARTIFACTS_ROOT.
	root := t.TempDir()
	t.Setenv("KITSOKI_ARTIFACTS_ROOT", root)

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	appPath := filepath.Join(repoRoot, "stories", "pr-refinement", "app.yaml")
	flowGlob := filepath.Join(repoRoot, "stories", "pr-refinement", "flows", "diagnose_artifact_round_trip.yaml")

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

	// Belt-and-braces: confirm the artifact landed and contains the sentinel,
	// on top of the YAML expect_world assertion. This pins the JSON
	// pretty-print path used by bodyArg in artifacts_dir_transport.go —
	// a previous regression had it stringifying the map as
	// `<map[string]interface {} Value>`, which expect_world would miss
	// (world stores the raw map fine; the failure only shows up on disk).
	//
	// thread: "pr-refinement-round-trip" → file: pr-refinement-round-trip.md
	artifact := filepath.Join(root, "pr-refinement-round-trip.md")
	body, readErr := os.ReadFile(artifact)
	if readErr != nil {
		t.Fatalf("artifact not written at %s: %v", artifact, readErr)
	}

	const sentinel = "PR-REFINEMENT-ROUND-TRIP-SENTINEL"
	if !strings.Contains(string(body), sentinel) {
		t.Errorf("artifact missing sentinel %q\n--- artifact body ---\n%s", sentinel, body)
	}

	for _, want := range []string{
		"Nil pointer dereference",
		"foo.go",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("artifact missing expected content %q\n--- artifact body ---\n%s", want, body)
		}
	}

	// Guard against the two silent-failure signatures.
	trimmed := strings.TrimSpace(prRefinementStripHeader(string(body)))
	if trimmed == "{}" {
		t.Errorf("artifact body is the silent-failure signature `{}`\n--- artifact body ---\n%s", body)
	}
	if strings.Contains(trimmed, "<map[") {
		t.Errorf("artifact body looks like a Go map stringification (no JSON pretty-print)\n--- artifact body ---\n%s", body)
	}
}

// prRefinementStripHeader trims the renderArtifact prefix ("### …\n\n") and
// footer ("_phase: …_") so the shape checks operate on the body proper.
func prRefinementStripHeader(s string) string {
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[i+2:]
	}
	if j := strings.LastIndex(s, "\n\n_phase:"); j >= 0 {
		s = s[:j]
	}
	return s
}
