// Package testrunner — G-FLOW near-silent-bounce gate tests
// (never-silent-runtime proposal, Task 2.2).
//
// assertNoSilentOnErrorBounce (flows.go) scans a turn's events for a
// TransitionApplied carrying intent="on_error" (emitted by
// Orchestrator.enterRedirectState whenever an on_error: arc fires) and, when
// found, requires the turn's rendered view to contain the never-silent error
// banner marker (orchestrator.ErrorBannerMarker). It runs unconditionally
// inside both runOneFlowLegacy and runOneFlowOrchestrator's per-turn
// assertion blocks, alongside — not instead of — the existing opt-in
// expect_view_matches assertion.
//
// These tests prove the gate has teeth in both directions using the
// testdata/apps/error_banner fixtures, driven end-to-end through
// testrunner.RunFlows with a stubbed host.run (no LLM, no live host call):
//
//   - TestGFlowGate_FiresOnSilentOnErrorBounce: do_thing_silently lands in
//     bounced_silent, whose on_enter clears world.last_error before render
//     (set: {last_error: ""}) so the shared banner seam
//     (applyErrorBannerSeam in host_dispatch.go) never fires even though it
//     exists. The fixture carries no expect_view_matches, so the gate is the
//     ONLY thing that can fail this turn — proving it fires unconditionally,
//     naming the fixture file, the destination arc, and the banner marker.
//   - TestGFlowGate_PassesWhenBannerPresent: do_thing drives the same kind of
//     on_error: arc to a room (bounced) that also does not reference
//     world.last_error in its view, but this time last_error survives to
//     render, so the shared seam appends the banner and the gate is
//     satisfied — proving it does not false-positive on the common,
//     already-fixed path.
package testrunner_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestGFlowGate_FiresOnSilentOnErrorBounce is the negative-proof half of the
// gate's teeth: a fixture that drives an on_error: arc into a room whose
// on_enter wipes world.last_error before render must fail — automatically,
// with no opt-in assertion required — naming the fixture, the arc's
// destination, and the missing banner marker.
func TestGFlowGate_FiresOnSilentOnErrorBounce(t *testing.T) {
	const appPath = "../../testdata/apps/error_banner/app.yaml"
	const glob = "../../testdata/apps/error_banner/flows/redirect_silent_bounce_fails_gate.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows itself should not return a fatal (infra) error")
	require.NotEmpty(t, report.Results)
	require.Equal(t, 1, report.Failed, "the silent-bounce fixture must fail the G-FLOW gate")
	require.Equal(t, 0, report.Passed)

	r := report.Results[0]
	require.False(t, r.Skipped, "fixture should not be skipped (no LLM involved)")
	require.Len(t, r.Turns, 1)

	tr := r.Turns[0]
	require.False(t, tr.Passed, "turn should fail: on_error fired with no banner in the view")

	var gflowFailure string
	for _, f := range tr.Failures {
		if strings.HasPrefix(f, "G-FLOW:") {
			gflowFailure = f
			break
		}
	}
	require.NotEmpty(t, gflowFailure, "expected a G-FLOW failure, got: %v", tr.Failures)
	require.Contains(t, gflowFailure, filepath.Base(glob), "failure must name the fixture")
	require.Contains(t, gflowFailure, "bounced_silent", "failure must name the on_error: arc's destination")
	require.Contains(t, gflowFailure, "Action failed", "failure must name the missing banner marker")
}

// TestGFlowGate_PassesWhenBannerPresent is the positive-proof half: the same
// shape of fixture (on_error: arc into a room that renders no
// {{ world.last_error }} of its own) passes the gate once the shared
// never-silent banner seam (applyErrorBannerSeam) has appended the banner to
// the rendered view — proving the gate does not misfire on the fixed path.
func TestGFlowGate_PassesWhenBannerPresent(t *testing.T) {
	const appPath = "../../testdata/apps/error_banner/app.yaml"
	const glob = "../../testdata/apps/error_banner/flows/redirect_surfaces_error.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.NotEmpty(t, report.Results)
	require.Equal(t, 0, report.Failed, "the banner-bearing fixture must pass the G-FLOW gate")
	require.Equal(t, 1, report.Passed)

	for _, r := range report.Results {
		for _, tr := range r.Turns {
			require.True(t, tr.Passed, "turn %d failures: %v", tr.TurnIndex+1, tr.Failures)
			for _, f := range tr.Failures {
				require.False(t, strings.HasPrefix(f, "G-FLOW:"), "unexpected G-FLOW failure: %s", f)
			}
		}
	}
}
