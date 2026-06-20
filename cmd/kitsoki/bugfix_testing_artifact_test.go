// bugfix_testing_artifact_test.go — story-level integration test for the
// implement_review_artifact bind-to-disk round-trip in the bugfix testing room.
//
// The existing flow fixtures (happy_human, happy_quick_fix, etc.) all stub
// the transport iface (host.append_to_file), so they never exercise the chain:
//
//   host.agent.task returns ok with Data["submitted"] = full testing artifact
//     → orchestrator binds `implement_review_artifact: submitted` into world
//     → on accept: iface.transport.post body: "{{ world.implement_review_artifact.summary_markdown }}"
//     → host.artifacts_dir pretty-prints the map to a file on disk
//
// This test stubs host.local (ci) and host.agent.task inline (returning a
// fully-shaped testing artifact with a recognizable sentinel string), rebinds
// the transport iface to the real host.artifacts_dir handler, then asserts
// the file on disk contains the sentinel AND does NOT contain the two known
// silent-failure signatures (`{}` literal or `<map[` Go stringification).

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

func TestBugfix_TestingArtifactRoundTrip(t *testing.T) {
	// Real host.artifacts_dir writes under $KITSOKI_ARTIFACTS_ROOT.
	root := t.TempDir()
	t.Setenv("KITSOKI_ARTIFACTS_ROOT", root)

	_, here, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(here), "..", ".."))
	appPath := filepath.Join(repoRoot, "stories", "bugfix", "app.yaml")
	flowGlob := filepath.Join(repoRoot, "stories", "bugfix", "flows", "testing_artifact_round_trip.yaml")

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
		t.FailNow()
	}

	// Confirm the artifact landed at $KITSOKI_ARTIFACTS_ROOT/TKT-999.md.
	// The filename is derived from the thread arg ("TKT-999") by
	// host.artifacts_dir appending ".md".
	artifact := filepath.Join(root, "TKT-999.md")
	body, readErr := os.ReadFile(artifact)
	if readErr != nil {
		t.Fatalf("artifact not written at %s: %v", artifact, readErr)
	}

	// The sentinel string must survive the full bind→template→bodyArg→file
	// chain intact.
	const sentinel = "BUGFIX-ROUND-TRIP-SENTINEL"
	if !strings.Contains(string(body), sentinel) {
		t.Errorf("artifact missing sentinel %q\n--- artifact body ---\n%s", sentinel, body)
	}

	// Guard against the two known silent-failure signatures.
	trimmed := strings.TrimSpace(stripBugfixHeader(string(body)))
	if trimmed == "{}" {
		t.Errorf("artifact body is the silent-failure signature `{}`\n--- artifact body ---\n%s", body)
	}
	if strings.Contains(trimmed, "<map[") {
		t.Errorf("artifact body looks like a Go map stringification (no JSON pretty-print)\n--- artifact body ---\n%s", body)
	}
}

// stripBugfixHeader trims the renderArtifact prefix ("### …\n\n") and
// footer ("\n\n_phase: …_") so shape checks operate on the body proper.
func stripBugfixHeader(s string) string {
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[i+2:]
	}
	if j := strings.LastIndex(s, "\n\n_phase:"); j >= 0 {
		s = s[:j]
	}
	return s
}
