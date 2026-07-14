// Package testrunner — G-VIEW silent view-render gate tests.
//
// Both flow-fixture rigs (runOneFlowLegacy and runOneFlowOrchestrator, in
// flows.go) re-render the landed state's view after a turn settles, so
// `expect_view_matches` sees exactly what `kitsoki session show` would
// render — not the possibly-stale/empty view machine.Turn (or the
// orchestrator's post-bind dispatch) captured before this second render.
// Before the G-VIEW gate, a non-nil error from that second render was
// silently dropped in both rigs: the harness fell back to whatever view the
// turn already carried (often empty, for states whose on_enter binds — see
// machine.go hostCallsWillBind) and the turn was reported as passing unless
// the fixture happened to declare `expect_view_matches`.
//
// The gate now runs unconditionally, alongside — not instead of —
// `expect_view_matches`: any non-nil post-turn RenderState error appends a
// "G-VIEW: ..." failure naming the landed state and the render error.
//
// These tests prove the gate has teeth in both directions, on both rigs,
// using the testdata/apps/view_render_gap fixtures, driven end-to-end
// through testrunner.RunFlows with a stubbed host.test.scan (no LLM, no
// live host call):
//
//   - legacy_broken_view_fails_gate.yaml / orchestrator_broken_view_fails_gate.yaml:
//     `broken`'s on_enter binds a host call (so the fast-path render inside
//     machine.Turn is skipped) and its view references a pongo2 filter that
//     does not exist. The fixtures carry no expect_view_matches, so the
//     gate is the ONLY thing that can fail these turns — proving it fires
//     unconditionally on both rigs.
//   - legacy_healthy_view_passes_gate.yaml / orchestrator_healthy_view_passes_gate.yaml:
//     `fine` binds the same way but renders cleanly — proving the gate does
//     not false-positive on the common, healthy path.
package testrunner_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestGViewGate_FiresOnSilentRenderFailure_Legacy is the negative-proof half
// for runOneFlowLegacy: a fixture whose landed state's view fails to render
// must fail — automatically, with no opt-in assertion required — naming the
// state and the render error.
func TestGViewGate_FiresOnSilentRenderFailure_Legacy(t *testing.T) {
	const appPath = "../../testdata/apps/view_render_gap/app.yaml"
	const glob = "../../testdata/apps/view_render_gap/flows/legacy_broken_view_fails_gate.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows itself should not return a fatal (infra) error")
	require.NotEmpty(t, report.Results)
	require.Equal(t, 1, report.Failed, "the broken-view fixture must fail the G-VIEW gate")
	require.Equal(t, 0, report.Passed)

	r := report.Results[0]
	require.False(t, r.Skipped, "fixture should not be skipped (no LLM involved)")
	require.Len(t, r.Turns, 1)

	tr := r.Turns[0]
	require.False(t, tr.Passed, "turn should fail: the landed state's view does not render")

	gviewFailure := findGViewFailure(t, tr.Failures)
	require.Contains(t, gviewFailure, "broken", "failure must name the landed state")
	require.Contains(t, gviewFailure, "nosuchfilter", "failure must surface the underlying render error")
}

// TestGViewGate_FiresOnSilentRenderFailure_Orchestrator is the same proof
// for runOneFlowOrchestrator.
func TestGViewGate_FiresOnSilentRenderFailure_Orchestrator(t *testing.T) {
	const appPath = "../../testdata/apps/view_render_gap/app.yaml"
	const glob = "../../testdata/apps/view_render_gap/flows/orchestrator_broken_view_fails_gate.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows itself should not return a fatal (infra) error")
	require.NotEmpty(t, report.Results)
	require.Equal(t, 1, report.Failed, "the broken-view fixture must fail the G-VIEW gate")
	require.Equal(t, 0, report.Passed)

	r := report.Results[0]
	require.False(t, r.Skipped, "fixture should not be skipped (no LLM involved)")
	require.Len(t, r.Turns, 1)

	tr := r.Turns[0]
	require.False(t, tr.Passed, "turn should fail: the landed state's view does not render")

	gviewFailure := findGViewFailure(t, tr.Failures)
	require.Contains(t, gviewFailure, "broken", "failure must name the landed state")
	require.Contains(t, gviewFailure, "nosuchfilter", "failure must surface the underlying render error")
}

// TestGViewGate_PassesWhenViewRenders_Legacy is the positive-proof half for
// runOneFlowLegacy: the same shape of fixture, but the landed state's view
// renders cleanly, so the gate must not misfire.
func TestGViewGate_PassesWhenViewRenders_Legacy(t *testing.T) {
	const appPath = "../../testdata/apps/view_render_gap/app.yaml"
	const glob = "../../testdata/apps/view_render_gap/flows/legacy_healthy_view_passes_gate.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.NotEmpty(t, report.Results)
	require.Equal(t, 0, report.Failed, "the healthy-view fixture must pass the G-VIEW gate")
	require.Equal(t, 1, report.Passed)

	assertNoGViewFailures(t, report)
}

// TestGViewGate_PassesWhenViewRenders_Orchestrator is the same proof for
// runOneFlowOrchestrator.
func TestGViewGate_PassesWhenViewRenders_Orchestrator(t *testing.T) {
	const appPath = "../../testdata/apps/view_render_gap/app.yaml"
	const glob = "../../testdata/apps/view_render_gap/flows/orchestrator_healthy_view_passes_gate.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.NotEmpty(t, report.Results)
	require.Equal(t, 0, report.Failed, "the healthy-view fixture must pass the G-VIEW gate")
	require.Equal(t, 1, report.Passed)

	assertNoGViewFailures(t, report)
}

func findGViewFailure(t *testing.T, failures []string) string {
	t.Helper()
	for _, f := range failures {
		if strings.HasPrefix(f, "G-VIEW:") {
			return f
		}
	}
	require.Fail(t, "expected a G-VIEW failure", "got: %v", failures)
	return ""
}

func assertNoGViewFailures(t *testing.T, report *testrunner.FlowReport) {
	t.Helper()
	for _, r := range report.Results {
		for _, tr := range r.Turns {
			require.True(t, tr.Passed, "turn %d failures: %v", tr.TurnIndex+1, tr.Failures)
			for _, f := range tr.Failures {
				require.False(t, strings.HasPrefix(f, "G-VIEW:"), "unexpected G-VIEW failure: %s", f)
			}
		}
	}
}
