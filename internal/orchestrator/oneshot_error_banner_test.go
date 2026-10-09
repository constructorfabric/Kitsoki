package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestOrchestrator_OneShot_AppliesErrorBannerOnRedirect is the never-silent
// invariant (docs/proposals/never-silent-runtime.md, Task 1.1/2.1) applied to
// the stateless one-shot path (`Orchestrator.OneShot`, the engine under
// `kitsoki turn` and MCP's `turn`/`drive` tools).
//
// The fixture app's `probe` state's on_enter invokes a failing host call with
// `on_error: probe_error`; `probe_error`'s view ("Error. Marker: {{
// world.marker }}") does not itself reference the failure, so the ONLY way
// the caller learns what happened is the shared error-banner seam
// (appendErrorBanner) firing on the redirect. Turn and ContinueTurn already
// get this from their own inline call; OneShot's Intent-call path dispatches
// through dispatchHostCallsDetailed directly and, prior to this fix, never
// called appendErrorBanner at all — a silent state change with a "clean"
// view and no clue why the turn bounced.
func TestOrchestrator_OneShot_AppliesErrorBannerOnRedirect(t *testing.T) {
	def, err := app.Load("testdata/hosterror/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.fail", func(ctx context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Error: "deliberate failure"}, nil
	})

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	out, err := orch.OneShot(context.Background(), orchestrator.OneShotInput{
		State:  app.StatePath("idle"),
		World:  map[string]any{"marker": ""},
		Intent: "ask",
		Slots:  map[string]any{},
	})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("probe_error"), out.NextState)

	// The redirected view must carry a distinguishable never-silent banner
	// (errorBannerFormat = "⚠ Action failed: %s") surfacing the failure —
	// not just the destination room's ordinary, failure-blind view.
	require.Contains(t, out.View, "⚠ Action failed:", "OneShot must apply the never-silent error banner on an on_error: redirect, same as Turn/ContinueTurn")
	require.Contains(t, out.View, "deliberate failure")
}
