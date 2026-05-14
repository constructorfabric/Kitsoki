package orchestrator_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/inbox"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestTeleport_OregonTrail_WarpFromIntro is the regression test for the
// `/warp` slash command's interactive path: a fresh session at `intro`
// teleports to `leg_c_awaiting_reply` with a primed world. Verifies the
// state lookup, world merge, and view re-render all succeed against the
// real three-layer-import oregon-trail app. If `/warp` "doesn't work"
// interactively, the failure mode shows up here as a non-nil error or
// an empty / wrong View.
func TestTeleport_OregonTrail_WarpFromIntro(t *testing.T) {
	def, err := app.Load("../../stories/oregon-trail/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarness{})

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	target := inbox.TeleportTarget{
		State: app.StatePath("leg_c_awaiting_reply"),
		Slots: map[string]any{
			"money":            int64(400),
			"party_alive":      int64(5),
			"oxen":             int64(4),
			"food_lbs":         int64(1000),
			"current_landmark": "Chimney Rock",
			"miles_traveled":   int64(600),
			"day":              int64(22),
			"profession":       "banker",
			"month":            "june",
		},
	}
	out, err := orch.Teleport(ctx, sid, target)
	require.NoError(t, err, "Teleport against oregon-trail must succeed")
	require.NotNil(t, out)
	require.Equal(t, app.StatePath("leg_c_awaiting_reply"), out.NewState)
	require.NotEmpty(t, out.View, "view must render at the teleport target")
	require.NotEmpty(t, out.AllowedIntents, "menu must populate at the teleport target")

	// Sanity: face_robbery is a checkpoint intent available at the target.
	hasFaceRobbery := false
	for _, in := range out.AllowedIntents {
		if in == "face_robbery" {
			hasFaceRobbery = true
			break
		}
	}
	require.True(t, hasFaceRobbery, "face_robbery should be allowed at leg_c_awaiting_reply post-warp; got %v", out.AllowedIntents)
}
