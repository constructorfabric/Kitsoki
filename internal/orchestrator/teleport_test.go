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

// TestTeleport_ReplayDeterministic verifies P0-3: after a Teleport call the
// event log must contain enough information for BuildJourney (loadJourney) to
// reconstruct the post-teleport world state without the live DB.  Concretely:
//
//   - The destination state must be restored (via TransitionApplied).
//   - Merged slot keys must be restored (via EffectApplied events).
//   - teleport_job_id / teleport_proposal_id must also be restored.
func TestTeleport_ReplayDeterministic(t *testing.T) {
	def := &app.AppDef{
		App:  app.AppMeta{ID: "teleport-replay-test"},
		Root: "init",
		World: map[string]app.VarDef{
			"x":                    {Type: "string", Default: ""},
			"teleport_job_id":      {Type: "string", Default: ""},
			"teleport_proposal_id": {Type: "string", Default: ""},
		},
		States: map[string]*app.State{
			"init": {View: app.LegacyView("init")},
			"dest": {View: app.LegacyView("dest x={{ world.x }}")},
		},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	orch := orchestrator.New(def, m, s, noopHarness{})

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Teleport to "dest" with a slot and metadata.
	target := inbox.TeleportTarget{
		State:      "dest",
		Slots:      map[string]any{"x": "teleported"},
		JobID:      "job-abc",
		ProposalID: "prop-xyz",
	}
	out, err := orch.Teleport(ctx, sid, target)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("dest"), out.NewState)

	// Simulate a process restart: rebuild journey from the event log.
	// loadJourney is unexported; use LoadJourney (the exported wrapper).
	rebuilt, err := orch.LoadJourney(sid)
	require.NoError(t, err)

	require.Equal(t, app.StatePath("dest"), rebuilt.State,
		"destination state must be restored after replay")
	require.Equal(t, "teleported", rebuilt.World.Vars["x"],
		"slot x must be restored after replay")
	require.Equal(t, "job-abc", rebuilt.World.Vars["teleport_job_id"],
		"teleport_job_id must be restored after replay")
	require.Equal(t, "prop-xyz", rebuilt.World.Vars["teleport_proposal_id"],
		"teleport_proposal_id must be restored after replay")
}
