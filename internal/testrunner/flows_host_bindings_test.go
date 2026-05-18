// Package testrunner — integration test for the fixture-level
// host_bindings: override seam (FlowFixture.HostBindings) used to swap
// an iface's concrete handler without forking the production app.yaml.
//
// What this test proves end-to-end:
//
//	1. The runner reloads the app via app.LoadWithOverrides when a
//	   fixture declares host_bindings:, so iface.<x> resolves to the
//	   overridden binding for THAT fixture only.
//	2. With cyp__transport: host.artifacts_dir, every cypilot
//	   _awaiting_reply.on_enter that calls iface.transport.post lands
//	   real bytes on disk via the production host.artifacts_dir
//	   handler (not a stub).
//	3. The expect_files: assertion fires against those real files.
//
// The handler reads its target directory from KITSOKI_ARTIFACTS_ROOT
// (with cwd/.artifacts as fallback); the test sets it to t.TempDir()
// so we don't pollute the repo's .artifacts/ directory and so the
// expect_files: regex matches against an isolated path.
//
// Why we run this from Go rather than as a checked-in .yaml fixture:
// CLI `kitsoki test flows` has no env-var seam, so a YAML-only
// version of this test would either need a new env: field on
// FlowFixture (broader than the cake proposal calls for) or would
// have to default the artifact directory to a non-tempdir location.
// Keeping the env setup in Go scope localises the side effect and
// keeps the testrunner's public surface small.
package testrunner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

func TestRunFlows_HostBindings_ArtifactsDirEndToEnd(t *testing.T) {
	artifactsRoot := t.TempDir()
	t.Setenv("KITSOKI_ARTIFACTS_ROOT", artifactsRoot)

	dir := t.TempDir()
	flowPath := filepath.Join(dir, "epic_artifacts.yaml")

	// Use a fully cake-shaped epic walk. host_bindings rebinds
	// cyp__transport to host.artifacts_dir; the fixture does NOT stub
	// host.artifacts_dir so the real handler runs.
	fixture := `test_kind: flow
app: ` + repoStoriesDevStoryAppPath(t) + `
initial_state: main
initial_world:
  ticket_id:      "E-CAKE-001"
  ticket_title:   "End-to-end artifacts_dir demo"
  ticket_type:    "epic"
  thread:         "E-CAKE-001"
  workdir:        "testdata/projects/cake"
  judge_mode:     "human"

host_bindings:
  cyp__transport: host.artifacts_dir

host_handlers:
  host.inbox.add:
    data: { ok: true, id: "ib-1" }
  host.cypilot_artifacts:
    data:
      ok: true
      id:          "art-e2e"
      path:        "cypilot/artifacts/e2e.md"
      report:      "PASS"
      findings:    []
      plan_path:   ".plans/e2e"
      phase_count: 1
      artifact:
        id:               "art-e2e"
        path:             "cypilot/artifacts/e2e.md"
        kind:             "code"
        summary_title:    "End-to-end demo code artifact"
        summary_markdown: "Stub code body for the artifacts_dir e2e test."
        plan_path:        ".plans/e2e"
        phase_count:      1
        pr_title:         "End-to-end artifacts_dir demo"
        pr_body:          "Demonstrates host_bindings overriding cyp__transport."
        tests_passed:     1
        tests_failed:     0
  host.local:
    data: { ok: true, log: "ok", passed: 1, failed: 0, state: "success" }
  host.local_files.ticket:
    data: { ok: true, tickets: [] }
  host.oracle.ask_with_mcp:
    data:
      ok: true
      submitted:
        verdict:    "uncertain"
        intent:     "uncertain"
        reason:     "n/a"
        confidence: 0.0

turns:
  - intent: { name: go_cypilot, slots: {} }
    expect_state: cyp.idle
  - intent: { name: cyp__begin, slots: {} }
    expect_state: cyp.prd_executing
  - intent: { name: cyp__proceed, slots: {} }
    expect_state: cyp.prd_awaiting_reply
  - intent: { name: cyp__accept, slots: {} }
    expect_state: cyp.adr_executing
  - intent: { name: cyp__proceed, slots: {} }
    expect_state: cyp.adr_awaiting_reply
  - intent: { name: cyp__accept, slots: {} }
    expect_state: cyp.design_executing
  - intent: { name: cyp__proceed, slots: {} }
    expect_state: cyp.design_awaiting_reply
  - intent: { name: cyp__accept, slots: {} }
    expect_state: cyp.decomposition_executing
  - intent: { name: cyp__proceed, slots: {} }
    expect_state: cyp.decomposition_awaiting_reply
  - intent: { name: cyp__accept, slots: {} }
    expect_state: cyp.feature_executing
  - intent: { name: cyp__proceed, slots: {} }
    expect_state: cyp.feature_awaiting_reply
  - intent: { name: cyp__accept, slots: {} }
    expect_state: cyp.code_executing
  - intent: { name: cyp__proceed, slots: {} }
    expect_state: cyp.code_awaiting_reply
  - intent: { name: cyp__accept, slots: {} }
    expect_state: main

expect_no_errors: true

# host.artifacts_dir writes thread="E-CAKE-001" to <root>/E-CAKE-001.md
# in append mode. Each cypilot _awaiting_reply.on_enter fires one post
# (body = world.<artifact>.summary_markdown), so the file ends up
# holding several chunks separated by the canonical "---" line. The
# regex pins both: the body text appears AND the chunks are
# separator-delimited (i.e. there's at least one append).
expect_files:
  - path: ` + filepath.Join(artifactsRoot, "E-CAKE-001.md") + `
    content_matches: "(?s)Stub code body for the artifacts_dir e2e test\\..*\n---\n"
`
	require.NoError(t, os.WriteFile(flowPath, []byte(fixture), 0o644))

	report, err := testrunner.RunFlows(t.Context(), repoStoriesDevStoryAppPath(t), flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "failures: %+v", report.Results[0].Turns)

	// File should physically exist with multi-chunk content — separate
	// assertion so a regression of expect_files (the matcher) doesn't
	// silently pass.
	data, readErr := os.ReadFile(filepath.Join(artifactsRoot, "E-CAKE-001.md"))
	require.NoError(t, readErr, "artifacts_dir transport should have written the file")
	require.Contains(t, string(data), "---", "multi-phase appends should be separator-delimited")
}

// repoStoriesDevStoryAppPath returns the absolute path to the
// dev-story app from inside the testrunner package's test cwd.
func repoStoriesDevStoryAppPath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../../stories/dev-story/app.yaml")
	require.NoError(t, err)
	return abs
}
