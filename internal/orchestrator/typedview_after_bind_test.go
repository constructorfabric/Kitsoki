package orchestrator_test

// Regression: a turn that ENTERS a room whose on_enter host calls bind must
// still ship a populated TypedView on its TurnOutcome.
//
// machine.Turn deliberately skips its typed render when the transition's
// on_enter host calls will bind (the pre-bind world would make bound-field
// view templates error — see machine.go hostCallsWillBind), and the post-bind
// re-render in dispatchHostCalls only produces the *text* view. Before
// orchestrator.refreshTypedViewAfterBind, TurnOutcome.TypedView stayed nil all
// the way to the web surface (newTurnResult), which then sent typed_view=null
// and the browser fell back to the ANSI-stripped 80-col text — collapsing the
// room's typed elements (banner, prose, choice→buttons) into one monospace
// blob. This is exactly what made dev-story's design_search overlap list AND
// its "Actions" menu render garbled in the web UI.
//
// The existing TypedView regressions (submitdirect_typedview_test.go,
// continueturn_typedview_test.go) only cover NON-binding destinations, so they
// never caught this. This fixture's `room` binds on entry.

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

func TestTurnIntoBindingOnEnterPreservesTypedView(t *testing.T) {
	t.Parallel()

	def, err := app.Load("../../testdata/apps/typedview_after_bind/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Stub the on_enter host call so it binds world.scan_result (no LLM).
	reg := host.NewRegistry()
	reg.Register("host.test.scan", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"summary": "two overlapping drafts"}}, nil
	})

	orch := orchestrator.New(def, m, s, &staticHarness{intentName: "enter"},
		orchestrator.WithHostRegistry(reg))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "enter")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.Equal(t, app.StatePath("room"), out.NewState)

	// The bind must have happened (proves on_enter ran — which is what made
	// machine.Turn skip its typed render): the post-bind text view embeds the
	// bound value.
	require.Contains(t, out.View, "two overlapping drafts",
		"sanity: on_enter host call should have bound scan_result into the view")

	// The fix: typed-view payload survives the binding on_enter.
	require.NotNil(t, out.TypedView,
		"entering a room whose on_enter binds must still populate TypedView "+
			"(else the web falls back to the plain-text 80-col blob)")
	require.NotNil(t, out.Renderer, "Renderer must accompany the refreshed TypedView")

	// The choice element (the "Actions" menu) must reach the browser as a
	// typed element so it renders as buttons rather than garbled text.
	var sawChoice, sawProse bool
	for _, el := range out.TypedView.Elements {
		switch el.Kind {
		case "choice":
			sawChoice = true
		case "prose":
			sawProse = true
		}
	}
	require.True(t, sawChoice, "room's choice element must survive in the typed view")
	require.True(t, sawProse, "room's prose element must survive in the typed view")
}
