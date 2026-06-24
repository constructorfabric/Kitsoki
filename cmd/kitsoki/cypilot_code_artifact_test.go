// cypilot_code_artifact_test.go — story-level regression test for the
// bind-to-disk chain in cypilot's code_awaiting_reply room.
//
// The existing cypilot flow fixtures all stub host.append_to_file (the
// transport default), so they never exercise the chain:
//
//   iface.artifact.create returns ok with Data["artifact"] = code map
//     → orchestrator binds `code_artifact: artifact` into world
//     → iface.transport.post body: "{{ world.code_artifact.summary_markdown }}"
//     → host.artifacts_dir bodyArg pretty-prints map[string]any as JSON
//     → file on disk
//
// This test stubs host.cypilot_artifacts and host.local inline (returning
// a fully-shaped code artifact with the sentinel) but lets the real
// host.artifacts_dir handler run via host_bindings: transport → artifacts_dir,
// then asserts the file on disk contains the sentinel.

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

func TestCypilot_CodeArtifactRoundTrip(t *testing.T) {
	// Real artifacts_dir writes under $KITSOKI_ARTIFACTS_ROOT.
	root := t.TempDir()
	t.Setenv("KITSOKI_ARTIFACTS_ROOT", root)

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	appPath := filepath.Join(repoRoot, "stories", "cypilot", "app.yaml")
	flowGlob := filepath.Join(repoRoot, "stories", "cypilot", "flows", "code_artifact_round_trip.yaml")

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

	// Assert the artifact landed on disk and carries the sentinel.
	// host.artifacts_dir writes thread="CYPILOT-RT-001" →
	// <root>/CYPILOT-RT-001.md.
	artifact := filepath.Join(root, "CYPILOT-RT-001.md")
	body, readErr := os.ReadFile(artifact)
	if readErr != nil {
		t.Fatalf("artifact not written at %s: %v", artifact, readErr)
	}

	// Primary sentinel: must appear verbatim in the file body.
	const sentinel = "CYPILOT-ROUND-TRIP-SENTINEL"
	if !strings.Contains(string(body), sentinel) {
		t.Errorf("artifact missing sentinel %q — bind or template chain dropped summary_markdown\n--- artifact body ---\n%s", sentinel, body)
	}

	// Guard against the silent-failure signature: an empty JSON map.
	trimmed := strings.TrimSpace(stripCypilotHeader(string(body)))
	if trimmed == "{}" {
		t.Errorf("artifact body is the silent-failure signature `{}` — map was dropped before disk write\n--- artifact body ---\n%s", body)
	}

	// Guard against Go map stringification (no JSON pretty-print path).
	if strings.Contains(trimmed, "<map[") {
		t.Errorf("artifact body looks like a Go map stringification (bodyArg lost its JSON path)\n--- artifact body ---\n%s", body)
	}
}

// stripCypilotHeader trims the renderArtifact prefix ("### …\n\n") and
// footer ("\n\n_phase: …_") so the shape checks operate on the body proper.
func stripCypilotHeader(s string) string {
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[i+2:]
	}
	if j := strings.LastIndex(s, "\n\n_phase:"); j >= 0 {
		s = s[:j]
	}
	return s
}
