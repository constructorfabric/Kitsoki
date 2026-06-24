package studio

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
)

// TestOpenDrivingSession_SeedsInitialWorld locks the studio twin of a flow
// fixture's initial_world:. session.new(initial_world:{…}) must seed those vars
// onto the session BEFORE the first on_enter, so a story can be driven headlessly
// on specific parameters (e.g. a ticket seeded into the bugfix pipeline) with no
// operator. Without this, the MCP could only drive a story from its default world.
func TestOpenDrivingSession_SeedsInitialWorld(t *testing.T) {
	sess := NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
		return noRouteStub{}, nil
	})

	// Seed story-declared keys (the bugfix pipeline's ticket vars are likewise
	// declared); the world is schema-bound, so seeding declared keys is the
	// supported contract. cloak's defaults are disturbance=0, wearing_cloak=true.
	seed := map[string]any{
		"disturbance":   7,
		"wearing_cloak": false,
	}
	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath:    profileCloakApp,
		TracePath:    t.TempDir() + "/trace.jsonl",
		InitialWorld: seed,
	})
	require.NoError(t, err)
	require.NotNil(t, sh.Runtime)

	j, err := sh.Runtime.orch.LoadJourney(sh.Runtime.sid)
	require.NoError(t, err)
	require.EqualValues(t, 7, j.World.Vars["disturbance"],
		"initial_world must override the story default (disturbance 0 → 7)")
	require.Equal(t, false, j.World.Vars["wearing_cloak"],
		"initial_world must override the story default (wearing_cloak true → false)")
}

// TestOpenDrivingSession_NoInitialWorldIsNoop confirms the empty/omitted case is
// a clean no-op (the prior behaviour): the session opens normally with no seeded
// keys beyond the story's own defaults.
func TestOpenDrivingSession_NoInitialWorldIsNoop(t *testing.T) {
	sess := NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
		return noRouteStub{}, nil
	})
	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.NotNil(t, sh.Runtime)
}
