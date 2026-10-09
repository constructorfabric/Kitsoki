// Package testrunner — tests for the session-level `acceptance:` block on
// flow fixtures (the version-portable outcome contract):
//
//   - final_state_in — glob match against the FINAL turn's resulting state
//   - world           — subset match; scalar = exact, {matches: re} = regex
//   - host_calls      — session-wide required (at-least-N) / forbidden sets
//   - files           — the expect_files item shape, evaluated post-run
//   - legacy path     — host_calls.required fails loudly, the rest still runs
//
// Each test is self-contained — it writes a small app.yaml + flow fixture to
// t.TempDir() so the assertions exercise the actual flow runner end-to-end,
// not the helper functions in isolation (the same discipline as
// flows_expectations_test.go).
package testrunner_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// acceptanceAppYAML is a tiny orchestrator-path app: `go` moves idle →
// core.landing, whose on_enter fires host.local_github.ticket twice (two
// invokes) and host.write_ledger once, and sets the world vars the
// acceptance tests assert on.
const acceptanceAppYAML = `
app:
  id: acceptance_test
  version: 0.1.0
  title: "acceptance block test"
  author: a
  license: CC0
hosts:
  - host.local_github.ticket
  - host.write_ledger
world:
  core__judge_mode: { type: string, default: "" }
  core__prd_path:   { type: string, default: "" }
  t1: { type: string, default: "" }
  t2: { type: string, default: "" }
  ok: { type: string, default: "" }
root: idle
intents:
  go:
    description: "Enter landing."
    examples: ["go"]
states:
  idle:
    on:
      go:
        - target: core.landing
  core:
    states:
      landing:
        on_enter:
          - set:
              core__judge_mode: "human"
              core__prd_path: "docs/gears/PRD.md"
          - invoke: host.local_github.ticket
            with:
              repo: "constructorfabric/gears-rust"
            bind:
              t1: ok
          - invoke: host.local_github.ticket
            with:
              repo: "constructorfabric/gears-rust"
            bind:
              t2: ok
          - invoke: host.write_ledger
            with:
              channel: "alpha"
            bind:
              ok: ok
        on:
          go:
            - target: core.landing
`

// acceptanceStubsYAML declares the stub handlers (opting the fixture into the
// orchestrator path, which records HostDispatched events).
const acceptanceStubsYAML = `
host_handlers:
  host.local_github.ticket:
    data: { ok: "yes" }
  host.write_ledger:
    data: { ok: "yes" }
`

// requireSessionFailure asserts the report failed and that some session-level
// failure message contains every given fragment.
func requireSessionFailure(t *testing.T, report *testrunner.FlowReport, fragments ...string) {
	t.Helper()
	require.Equal(t, 1, report.Failed, "report: %+v", report.Results)
	for _, r := range report.Results {
		for _, tr := range r.Turns {
			for _, f := range tr.Failures {
				matchesAll := true
				for _, frag := range fragments {
					if !strings.Contains(f, frag) {
						matchesAll = false
						break
					}
				}
				if matchesAll {
					return
				}
			}
		}
	}
	t.Fatalf("no failure contains all of %v; report: %+v", fragments, report.Results)
}

// TestAcceptance_PassesFullContract drives every acceptance sub-check at once
// on the orchestrator path: a final_state_in glob, exact + regex world values,
// a required host call with partial args, at-least-N times semantics (the
// handler fires twice; times: 1 and times: 2 both pass — the per-turn EXACT
// count would fail the former), a forbidden handler that never fires, and a
// files must_not_exist check.
func TestAcceptance_PassesFullContract(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle` + acceptanceStubsYAML + `
turns:
  - intent: { name: go }
    expect_state: core.landing
expect_no_errors: true
acceptance:
  final_state_in: ["core.land*", "core.idle"]
  world:
    core__judge_mode: "human"
    core__prd_path: { matches: "docs/.*PRD" }
  host_calls:
    required:
      - { handler: host.local_github.ticket, args: { repo: "constructorfabric/gears-rust" } }
      - { handler: host.local_github.ticket, times: 2 }
      - { handler: host.write_ledger, times: 1 }
    forbidden: [host.transport.post]
  files:
    - { path: never-written.md, must_not_exist: true }
`
	appPath, flowPath := writeFixture(t, dir, acceptanceAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results)
	require.Equal(t, 1, report.Passed)
}

// TestAcceptance_FinalStateGlobMismatch verifies a final state matching none
// of the globs fails the session with an acceptance-prefixed message.
func TestAcceptance_FinalStateGlobMismatch(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle` + acceptanceStubsYAML + `
turns:
  - intent: { name: go }
acceptance:
  final_state_in: ["other.*", "idle"]
`
	appPath, flowPath := writeFixture(t, dir, acceptanceAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	requireSessionFailure(t, report, "acceptance: final_state_in", "core.landing")
}

// TestAcceptance_WorldRegexMismatch verifies the {matches: regex} world form
// fails when the final value does not match the pattern.
func TestAcceptance_WorldRegexMismatch(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle` + acceptanceStubsYAML + `
turns:
  - intent: { name: go }
acceptance:
  world:
    core__prd_path: { matches: "wrong/place/.*DESIGN" }
`
	appPath, flowPath := writeFixture(t, dir, acceptanceAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	requireSessionFailure(t, report, `acceptance: world["core__prd_path"]`, "does not match")
}

// TestAcceptance_WorldExactMismatch verifies the scalar world form uses the
// exact (JSON-normalized) comparator, like expect_world_final.
func TestAcceptance_WorldExactMismatch(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle` + acceptanceStubsYAML + `
turns:
  - intent: { name: go }
acceptance:
  world:
    core__judge_mode: "robot"
`
	appPath, flowPath := writeFixture(t, dir, acceptanceAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	requireSessionFailure(t, report, `acceptance: world["core__judge_mode"]`, "robot")
}

// TestAcceptance_RequiredHostCallMissing verifies a required entry that no
// session host call matches (here: right handler, wrong args) fails.
func TestAcceptance_RequiredHostCallMissing(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle` + acceptanceStubsYAML + `
turns:
  - intent: { name: go }
acceptance:
  host_calls:
    required:
      - { handler: host.local_github.ticket, args: { repo: "some/other-repo" } }
`
	appPath, flowPath := writeFixture(t, dir, acceptanceAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	requireSessionFailure(t, report, "acceptance: host_calls.required", "host.local_github.ticket", "want at least 1")
}

// TestAcceptance_ForbiddenHostCallPresent verifies a forbidden handler that
// fired anywhere in the session fails the contract.
func TestAcceptance_ForbiddenHostCallPresent(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle` + acceptanceStubsYAML + `
turns:
  - intent: { name: go }
acceptance:
  host_calls:
    forbidden: [host.write_ledger]
`
	appPath, flowPath := writeFixture(t, dir, acceptanceAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	requireSessionFailure(t, report, "acceptance: host_calls.forbidden", "host.write_ledger")
}

// TestAcceptance_FilesCheck verifies the files list reuses the expect_files
// evaluator: an existing file with matching content passes; a missing file
// fails with the acceptance-prefixed message.
func TestAcceptance_FilesCheck(t *testing.T) {
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, ".artifacts", "report.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(artifactPath), 0o755))
	require.NoError(t, os.WriteFile(artifactPath, []byte("### Outcome\nshipped\n"), 0o644))

	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle` + acceptanceStubsYAML + `
turns:
  - intent: { name: go }
acceptance:
  files:
    - { path: .artifacts/report.md, content_matches: "Outcome" }
    - { path: .artifacts/missing.md }
`
	appPath, flowPath := writeFixture(t, dir, acceptanceAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	requireSessionFailure(t, report, "acceptance: files[.artifacts/missing.md]", "does not exist")
}

// legacyAppYAML is a minimal machine-only app (no hosts, no stubs — the
// fixture stays on the legacy path): `go` moves idle → done and sets a world
// var via effects.
const legacyAppYAML = `
app:
  id: acceptance_legacy_test
  version: 0.1.0
  title: "acceptance legacy path test"
  author: a
  license: CC0
hosts: []
world:
  mode: { type: string, default: "" }
root: idle
intents:
  go:
    description: "Finish."
    examples: ["go"]
states:
  idle:
    on:
      go:
        - target: done
          effects:
            - set:
                mode: "human"
  done:
    terminal: true
`

// TestAcceptance_LegacyPath_StateWorldFilesPass verifies that on the legacy
// machine-only path the state/world/files checks run normally, and that a
// forbidden list trivially passes (no dispatch events exist there — the same
// behaviour as the existing fixture-level expect_no_host_calls).
func TestAcceptance_LegacyPath_StateWorldFilesPass(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
turns:
  - intent: { name: go }
    expect_state: done
expect_no_errors: true
acceptance:
  final_state_in: ["done"]
  world:
    mode: { matches: "^hu" }
  host_calls:
    forbidden: [host.transport.post]
  files:
    - { path: never-written.md, must_not_exist: true }
`
	appPath, flowPath := writeFixture(t, dir, legacyAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results)
	require.Equal(t, 1, report.Passed)
}

// TestAcceptance_LegacyPath_RequiredHostCallsFailLoudly verifies that
// host_calls.required on the legacy path fails with the explicit
// "requires the orchestrator path" diagnosis rather than silently passing
// (or failing with a misleading zero-count message).
func TestAcceptance_LegacyPath_RequiredHostCallsFailLoudly(t *testing.T) {
	dir := t.TempDir()
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
turns:
  - intent: { name: go }
acceptance:
  host_calls:
    required:
      - { handler: host.anything }
`
	appPath, flowPath := writeFixture(t, dir, legacyAppYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	requireSessionFailure(t, report, "acceptance: host_calls.required", "requires the orchestrator path")
}
