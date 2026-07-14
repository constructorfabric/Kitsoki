// Package testrunner — tests for the expectation-based mock primitives
// added for the cake end-to-end pipeline test:
//
//   - HostStub.ByOp  — per-op envelope dispatch on a prefix-fallback stub
//   - expect_host_calls — turn-level shorthand over HostDispatched events
//   - expect_no_host_calls — fixture/turn-level "must not fire" guard
//   - expect_files — filesystem assertions after a flow completes
//
// Each test is self-contained — it writes a small app.yaml + flow fixture
// to t.TempDir() so the assertions exercise the actual flow runner end-to-
// end, not the helper functions in isolation. That keeps the suite honest
// against runner-side refactors.
package testrunner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// writeFixture writes app + flow YAML in dir and returns paths.
func writeFixture(t *testing.T, dir, appYAML, flowYAML string) (appPath, flowPath string) {
	t.Helper()
	appPath = filepath.Join(dir, "app.yaml")
	flowPath = filepath.Join(dir, "flow.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(appYAML), 0o644))
	require.NoError(t, os.WriteFile(flowPath, []byte(flowYAML), 0o644))
	return appPath, flowPath
}

// TestHostStub_ByOp_DispatchesPerOp verifies a single stub serves
// different envelopes for distinct ops under one prefix-fallback handler.
func TestHostStub_ByOp_DispatchesPerOp(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: byop_test
  version: 0.1.0
  title: "by_op test"
  author: a
  license: CC0
hosts:
  - host.fake_ticket
world:
  status:  { type: string, default: "" }
  log:     { type: string, default: "" }
root: idle
intents:
  list_then_get:
    description: "Run list then get."
    examples: ["go"]
states:
  idle:
    on:
      list_then_get:
        - target: loaded
  loaded:
    on_enter:
      - invoke: host.fake_ticket
        with:
          op: "list_mine"
        bind:
          log: tickets
      - invoke: host.fake_ticket
        with:
          op: "transition"
          to: "resolved"
        bind:
          status: result
    on:
      list_then_get:
        - target: loaded
`
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
host_handlers:
  host.fake_ticket:
    by_op:
      list_mine:
        data: { tickets: "from-list-mine" }
      transition:
        data: { result: "from-transition" }
turns:
  - intent: { name: list_then_get }
    expect_state: loaded
    expect_world:
      log:    "from-list-mine"
      status: "from-transition"
expect_no_errors: true
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results)
	require.Equal(t, 1, report.Passed)
}

func TestOperationRunExpectations(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: op_expect_test
  version: 0.1.0
  title: "operation expectation test"
  author: a
  license: CC0
operations:
  demo_run:
    title: "Demo run"
    mode: autonomous
    execution_mode: one-shot
    run_in_background: true
    terminal_artifact: done_artifact
intents:
  go:
    description: "Run the operation."
    examples: ["go"]
root: idle
states:
  idle:
    on:
      go:
        - target: done
          operation: demo_run
  done:
    terminal: true
`
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
turns:
  - intent: { name: go }
    expect_state: done
expect_terminal: true
expect_operation_policy: demo_run
expect_operation_status: completed
expect_terminal_artifact: done_artifact
expect_no_errors: true
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results)
	require.Equal(t, 1, report.Passed)
}

func TestOperationRunWaitingExpectations(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: op_waiting_expect_test
  version: 0.1.0
  title: "operation waiting expectation test"
  author: a
  license: CC0
operations:
  demo_run:
    title: "Demo run"
    mode: autonomous
    execution_mode: one-shot
    stop_on: [needs-human, host-error]
intents:
  go:
    description: "Run the operation."
    examples: ["go"]
world:
  status: { type: string, default: "" }
  needs_human_reason: { type: string, default: "" }
root: idle
states:
  idle:
    on:
      go:
        - target: needs-human
          operation: demo_run
          effects:
            - set:
                status: needs-human
                needs_human_reason: "Regression gate was never RED."
  needs-human:
    terminal: true
`
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
turns:
  - intent: { name: go }
    expect_state: needs-human
expect_terminal: true
expect_operation_policy: demo_run
expect_operation_status: waiting
expect_stop_reason: needs-human
expect_no_errors: true
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results)
	require.Equal(t, 1, report.Passed)
}

// TestHostStub_ByCall_DispatchesPerCallSite verifies a single stub serves
// different envelopes for two invokes that share a handler name AND identical
// args, distinguished only by the author-assigned `id:` on each invoke. This
// is the case that handler-name keying alone cannot express — the workaround
// it replaces is picking a different verb or splitting the phase. Without the
// `id:` → args["call"] threading both calls would key on call="" and miss
// every named by_call entry, falling through to the (empty) top-level
// envelope, leaving both world vars at their defaults — so this test fails if
// the threading regresses.
func TestHostStub_ByCall_DispatchesPerCallSite(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: bycall_test
  version: 0.1.0
  title: "by_call test"
  author: a
  license: CC0
hosts:
  - host.agent.decide
world:
  analyst_out: { type: string, default: "" }
  judge_out:   { type: string, default: "" }
root: idle
intents:
  enter:
    description: "Enter the two-decide room."
    examples: ["go"]
states:
  idle:
    on:
      enter:
        - target: loaded
  loaded:
    on_enter:
      # Two decide calls, identical args, addressable only by id.
      - invoke: host.agent.decide
        id: analyst
        with:
          prompt: "same prompt"
        bind:
          analyst_out: verdict
      - invoke: host.agent.decide
        id: judge
        with:
          prompt: "same prompt"
        bind:
          judge_out: verdict
    on:
      enter:
        - target: loaded
`
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
host_handlers:
  host.agent.decide:
    by_call:
      analyst:
        data: { verdict: "from-analyst" }
      judge:
        data: { verdict: "from-judge" }
turns:
  - intent: { name: enter }
    expect_state: loaded
    expect_world:
      analyst_out: "from-analyst"
      judge_out:   "from-judge"
expect_no_errors: true
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results)
	require.Equal(t, 1, report.Passed)
}

// TestExpectHostCalls_PinsArgs verifies expect_host_calls matches a
// HostDispatched event and partial-matches args.
func TestExpectHostCalls_PinsArgs(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: ehc_test
  version: 0.1.0
  title: "expect_host_calls test"
  author: a
  license: CC0
hosts:
  - host.write_ledger
world:
  ok: { type: bool, default: false }
root: idle
intents:
  fire:
    description: "Fire ledger write."
    examples: ["go"]
states:
  idle:
    on:
      fire:
        - target: writing
  writing:
    on_enter:
      - invoke: host.write_ledger
        with:
          channel: "alpha"
          amount:  42
        bind:
          ok: ok
    on:
      fire:
        - target: writing
`
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
host_handlers:
  host.write_ledger:
    data: { ok: true }
turns:
  - intent: { name: fire }
    expect_state: writing
    expect_host_calls:
      - handler: host.write_ledger
        args:
          channel: "alpha"
          amount:  42
        times: 1
expect_no_errors: true
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results[0].Turns)
	require.Equal(t, 1, report.Passed)
}

// TestExpectNoHostCalls_FailsWhenInvoked verifies expect_no_host_calls
// reports a failure when a forbidden handler is invoked.
func TestExpectNoHostCalls_FailsWhenInvoked(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: enhc_test
  version: 0.1.0
  title: "expect_no_host_calls test"
  author: a
  license: CC0
hosts:
  - host.must_not_fire
world: {}
root: idle
intents:
  fire:
    description: "Fire forbidden handler."
    examples: ["go"]
states:
  idle:
    on:
      fire:
        - target: bad
  bad:
    on_enter:
      - invoke: host.must_not_fire
        with: { x: 1 }
    on:
      fire:
        - target: bad
`
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
host_handlers:
  host.must_not_fire:
    data: { ok: true }
turns:
  - intent: { name: fire }
    expect_state: bad
    expect_no_host_calls:
      - host.must_not_fire
expect_no_errors: true
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, report.Failed)
	// A failure for forbidden handler should be present.
	var sawForbidden bool
	for _, r := range report.Results {
		for _, tr := range r.Turns {
			for _, f := range tr.Failures {
				if filepath.Base(f) != f { // just ensure non-empty
				}
				if containsAny(f, "forbidden", "host.must_not_fire") {
					sawForbidden = true
				}
			}
		}
	}
	require.True(t, sawForbidden, "expected a failure naming the forbidden handler")
}

// TestExpectFiles_PathAndContent verifies expect_files asserts both
// presence and content_matches regex.
func TestExpectFiles_PathAndContent(t *testing.T) {
	dir := t.TempDir()
	// Pre-create the file the fixture asserts on, simulating a real
	// transport stub writing during the flow. This test pins the
	// assertion mechanism rather than the host integration — that's
	// covered by the artifacts_dir host test.
	artifactPath := filepath.Join(dir, ".artifacts", "report.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(artifactPath), 0o755))
	require.NoError(t, os.WriteFile(artifactPath, []byte("### Reproduction\nseed seeded\n"), 0o644))

	appYAML := `
app:
  id: ef_test
  version: 0.1.0
  title: "expect_files test"
  author: a
  license: CC0
hosts: []
world: {}
root: idle
intents:
  noop:
    description: "."
    examples: ["go"]
states:
  idle:
    on:
      noop:
        - target: idle
`
	flowYAML := `
test_kind: flow
app: ` + filepath.Join(dir, "app.yaml") + `
initial_state: idle
turns:
  - intent: { name: noop }
expect_no_errors: true
expect_files:
  - path: .artifacts/report.md
    content_matches: "Reproduction"
  - path: .artifacts/never.md
    must_not_exist: true
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, report.Failed, "report: %+v", report.Results[0].Turns)
}

// containsAny is a tiny helper to avoid an extra import.
func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		if p != "" && indexOf(s, p) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
