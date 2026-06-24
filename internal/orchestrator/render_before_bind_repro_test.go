package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestRenderBeforeBindRepro is the reproduction artifact for bug
// 2026-05-14T103205Z-tui-view-render-before-bind:
//
//	"TUI view templates render BEFORE on_enter binds — first frame shows
//	 '(pending)' even when host call already returned"
//
// The scenario the bug describes: a state's on_enter chain runs a synchronous
// invoke: with a bind: projection, and the state's view references the bound
// world key WITHOUT a `??` fallback. At filing time (rev 75c4f11, 2026-05-14)
// the first frame rendered against the PRE-bind world snapshot, showing the
// schema default instead of the bound value.
//
// This test pins the behavior at HEAD across both seams the bug names:
//
//  1. machine.Turn (the original pre-bind render entry point): when the
//     queued host calls declare a bind:, Turn must NOT render the pre-bind
//     view. Pre-fix it returned the stale "(pending)" frame; the fix
//     (commit db7b283f, "render view once after host bind settles",
//     2026-05-15 — one day after filing) skips that render entirely.
//
//  2. orchestrator.Turn (the seam the TUI actually consumes via result.View):
//     the orchestrator re-renders against the POST-bind world after dispatch,
//     so result.View shows the bound value, never the default.
//
// If the bug were live, sub-test "machine_pre_bind_render_is_stale" would see
// the default value in the machine-level View and sub-test
// "orchestrator_view_is_post_bind" would see it in result.View.
func TestRenderBeforeBindRepro(t *testing.T) {
	const stalePlaceholder = "(pending)"

	// World key the on_enter bind populates; the view references it WITHOUT a
	// `?? "(pending)"` fallback — the exact authoring shape the bug says new
	// authors hit on day one. Its schema default is the stale placeholder so a
	// pre-bind render is unmistakable in the output.
	def := &app.AppDef{
		App:   app.AppMeta{ID: "render-before-bind-repro"},
		Root:  "init",
		Hosts: []string{"host.test.diff"},
		World: map[string]app.VarDef{
			"feature_branch_diff": {Type: "string", Default: stalePlaceholder},
		},
		Intents: map[string]app.Intent{
			"enter": {Title: "Enter"},
			"done":  {Title: "Done"},
		},
		States: map[string]*app.State{
			"init": {
				View: app.LegacyView("init"),
				On:   map[string][]app.Transition{"enter": {{Target: "review"}}},
			},
			"review": {
				// View references the bound key with NO `??` fallback.
				View: app.LegacyView("diff={{ world.feature_branch_diff }}"),
				OnEnter: []app.Effect{
					{
						Invoke: "host.test.diff",
						Bind:   map[string]string{"feature_branch_diff": "diff"},
					},
				},
				On: map[string][]app.Transition{"done": {{Target: "end"}}},
			},
			"end": {Terminal: true, View: app.LegacyView("ended")},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	const boundDiff = "--- a/foo\n+++ b/foo\n@@ real diff @@"
	reg := host.NewRegistry()
	reg.Register("host.test.diff", func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"diff": boundDiff}}, nil
	})

	ctx := context.Background()

	t.Run("machine_pre_bind_render_is_skipped", func(t *testing.T) {
		// Drive a bare machine.Turn from init into review. Pre-fix this
		// returned a non-empty View rendered against the pre-bind world,
		// containing the stale placeholder. The fix skips the render when
		// host calls will bind, so View is empty and the orchestrator owns it.
		w := machine.WorldFromSchema(app.WorldSchema(def.World))

		tr, err := m.Turn(ctx, app.StatePath("init"), w, intent.IntentCall{Intent: "enter"})
		require.NoError(t, err)
		require.Equal(t, app.StatePath("review"), tr.NewState)
		require.NotEmpty(t, tr.HostCalls, "review.on_enter must queue the binding host call")

		require.NotContains(t, tr.View, stalePlaceholder,
			"BUG REPRODUCED: machine.Turn rendered the pre-bind world snapshot; "+
				"the stale schema default leaked into the first frame")
		require.Empty(t, tr.View,
			"render-after-bind contract: machine.Turn must skip the pre-bind "+
				"render when queued host calls declare a bind:")
	})

	t.Run("pre_bind_world_render_demonstrates_stale_frame", func(t *testing.T) {
		// Demonstrate the bug MECHANISM directly and deterministically:
		// rendering review's view against the PRE-bind world (the snapshot
		// that existed when the bug was filed, before the on_enter invoke
		// returned) yields the stale "(pending)" frame the user saw; the SAME
		// view against the POST-bind world yields the real diff. This is the
		// exact stale-vs-fresh delta the runtime fix eliminated by deferring
		// the render until after the bind settles.
		preBind := machine.WorldFromSchema(app.WorldSchema(def.World))
		staleView, err := m.RenderState("review", preBind)
		require.NoError(t, err)
		require.Contains(t, staleView, stalePlaceholder,
			"pre-bind render must reproduce the stale first frame the bug reports")

		postBind := preBind.With("feature_branch_diff", boundDiff)
		freshView, err := m.RenderState("review", postBind)
		require.NoError(t, err)
		require.Contains(t, freshView, boundDiff,
			"post-bind render must carry the real host result")
		require.NotEqual(t, staleView, freshView,
			"the stale and fresh frames must differ — that delta IS the bug")
	})

	t.Run("orchestrator_view_is_post_bind", func(t *testing.T) {
		// Drive a full orchestrator.Turn (the seam the TUI consumes via
		// result.View). The orchestrator dispatches the host call and
		// re-renders against the post-bind world.
		s, err := store.OpenMemory()
		require.NoError(t, err)
		defer s.Close()

		orch := orchestrator.New(def, m, s, &staticHarness{intentName: "enter"},
			orchestrator.WithHostRegistry(reg))

		sid, err := orch.NewSession(ctx)
		require.NoError(t, err)

		out, err := orch.Turn(ctx, sid, "enter")
		require.NoError(t, err)
		require.Equal(t, app.StatePath("review"), out.NewState)

		require.NotContains(t, out.View, stalePlaceholder,
			"BUG REPRODUCED: orchestrator result.View carries the stale "+
				"pre-bind default into the frame the TUI displays")
		require.True(t, strings.Contains(out.View, boundDiff),
			"orchestrator must re-render against the post-bind world; "+
				"result.View should carry the bound host result")
	})
}
