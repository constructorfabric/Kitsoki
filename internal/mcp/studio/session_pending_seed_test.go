package studio

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	"kitsoki/internal/host"
)

// TestOpenDrivingSession_PendingSeedBackstop proves the deterministic seed
// backstop end-to-end through the REAL on-disk host store (no mock): a parent
// registers a pending seed keyed by (KITSOKI_SESSION_ID, story) and a subsequent
// session.new opened WITHOUT initial_world still seeds the nested story's world.
// This is the core of the fix — the maker no longer has to pass initial_world for
// the nested driven session to self-provision.
//
// It also locks: explicit initial_world wins on conflicting keys (seed only fills
// gaps); consume-once (a second open gets no seed); and no registration ⇒ today's
// behaviour byte-identical.
func TestOpenDrivingSession_PendingSeedBackstop(t *testing.T) {
	// Isolate the store to this test and establish the parent lineage the studio
	// server reads from its own env.
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	const parentSID = "parent-sess-backstop"
	t.Setenv("KITSOKI_SESSION_ID", parentSID)

	newSess := func() *StudioSession {
		return NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
			return noRouteStub{}, nil
		})
	}

	// ── Register a seed, then open WITHOUT initial_world ─────────────────────
	// cloak's declared defaults are disturbance=0, wearing_cloak=true.
	seed := map[string]any{"disturbance": 7, "wearing_cloak": false}
	require.NoError(t, host.RegisterPendingSeed(parentSID, profileCloakApp, seed))

	sh, err := newSess().OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
		// NOTE: no InitialWorld — the whole point is the maker omitted it.
	})
	require.NoError(t, err)
	require.NotNil(t, sh.Runtime)

	j, err := sh.Runtime.orch.LoadJourney(sh.Runtime.sid)
	require.NoError(t, err)
	require.EqualValues(t, 7, j.World.Vars["disturbance"],
		"pending seed must seed the nested world even with no initial_world arg (0 → 7)")
	require.Equal(t, false, j.World.Vars["wearing_cloak"],
		"pending seed must seed every seeded key (wearing_cloak true → false)")

	// ── Consume-once: a second open on the same key gets no seed ─────────────
	sh2, err := newSess().OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace2.jsonl",
	})
	require.NoError(t, err)
	j2, err := sh2.Runtime.orch.LoadJourney(sh2.Runtime.sid)
	require.NoError(t, err)
	require.EqualValues(t, 0, j2.World.Vars["disturbance"],
		"the seed is consume-once: a sequential second open must fall back to the story default (0)")
}

// TestOpenDrivingSession_PendingSeedExplicitWins locks the merge precedence: when
// the maker DID pass initial_world, its explicit values win on conflicting keys
// and the pending seed only fills the gaps — so behaviour is identical whether or
// not the maker cooperated.
func TestOpenDrivingSession_PendingSeedExplicitWins(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	const parentSID = "parent-sess-explicit"
	t.Setenv("KITSOKI_SESSION_ID", parentSID)

	sess := NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
		return noRouteStub{}, nil
	})

	seed := map[string]any{"disturbance": 7, "wearing_cloak": false}
	require.NoError(t, host.RegisterPendingSeed(parentSID, profileCloakApp, seed))

	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
		// Explicit conflicts with the seed on disturbance; wearing_cloak is a gap.
		InitialWorld: map[string]any{"disturbance": 3},
	})
	require.NoError(t, err)
	j, err := sh.Runtime.orch.LoadJourney(sh.Runtime.sid)
	require.NoError(t, err)
	require.EqualValues(t, 3, j.World.Vars["disturbance"],
		"explicit initial_world must win on a conflicting key (seed 7 loses to explicit 3)")
	require.Equal(t, false, j.World.Vars["wearing_cloak"],
		"the seed must still fill a gap the explicit initial_world did not set")
}

// TestOpenDrivingSession_NoPendingSeedIsNoop confirms that with the lineage env
// set but NO seed registered, the open is byte-identical to today: the story's
// own defaults stand and nothing is seeded.
func TestOpenDrivingSession_NoPendingSeedIsNoop(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	t.Setenv("KITSOKI_SESSION_ID", "parent-sess-noop")

	sess := NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
		return noRouteStub{}, nil
	})
	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	j, err := sh.Runtime.orch.LoadJourney(sh.Runtime.sid)
	require.NoError(t, err)
	require.EqualValues(t, 0, j.World.Vars["disturbance"],
		"no registered seed ⇒ the story default stands (disturbance 0)")
}
