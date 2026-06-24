package studio

import (
	"context"
	"fmt"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	"kitsoki/internal/orchestrator"
)

// profileCloakApp is the no-custom-hosts cloak story used by the selection
// tests; its initial on_enter is a pure render so no agent fires at open and the
// stub harness below is never asked to route.
const profileCloakApp = "../../../testdata/apps/cloak/app.yaml"

// noRouteStub is a harness that fails loudly if ever asked to route — the
// selection tests only open a session (initial on_enter), they never drive, so a
// real cassette is unnecessary and the stub keeps the test no-LLM and recording-free.
type noRouteStub struct{}

func (noRouteStub) RunTurn(context.Context, harness.TurnInput) (mcpsdk.CallToolParams, error) {
	return mcpsdk.CallToolParams{}, fmt.Errorf("noRouteStub: must not route in a selection test")
}
func (noRouteStub) Close() error { return nil }

// newProfileSession opens a studio session whose builder yields the no-route
// stub for any mode (no cassette needed).
func newProfileSession() *StudioSession {
	return NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
		return noRouteStub{}, nil
	})
}

// profileFixtures is a two-backend map for the selection tests. The Backend
// values are inert here (no agent fires at open); only the selection plumbing is
// under test.
func profileFixtures() map[string]orchestrator.HarnessProfile {
	return map[string]orchestrator.HarnessProfile{
		"claude-native":    {Name: "claude-native", Backend: "claude", Model: "claude-sonnet-4-5"},
		"synthetic-claude": {Name: "synthetic-claude", Backend: "synthetic", Model: "hf:claude-sonnet-4-5"},
	}
}

// TestSessionProfile_ExplicitSelectionReachesOrchestrator locks the seam that
// makes synthetic/codex usable over MCP: a session.new(profile:…) must route its
// agent dispatch through the named backend, i.e. the orchestrator's initial
// selection equals the requested profile (overriding the server default). Before
// this, the studio runtime never wired WithHarnessProfiles, so every MCP session
// was pinned to the static default backend.
func TestSessionProfile_ExplicitSelectionReachesOrchestrator(t *testing.T) {
	sess := newProfileSession()
	sess.SetHarnessProfiles(profileFixtures(), "claude-native")

	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
		Profile:   "synthetic-claude",
	})
	require.NoError(t, err)
	require.NotNil(t, sh.Runtime)
	require.Equal(t, "synthetic-claude", sh.Runtime.orch.Selection().Profile,
		"an explicit profile must become the session's active selection")
}

// TestSessionProfile_DefaultsToServerProfile confirms an omitted profile falls
// back to the server's boot-time default (SetHarnessProfiles), not a hard error
// or the static path.
func TestSessionProfile_DefaultsToServerProfile(t *testing.T) {
	sess := newProfileSession()
	sess.SetHarnessProfiles(profileFixtures(), "claude-native")

	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
		// no Profile → server default
	})
	require.NoError(t, err)
	require.Equal(t, "claude-native", sh.Runtime.orch.Selection().Profile,
		"an omitted profile falls back to the configured default")
}

// TestSessionProfile_NoneConfiguredLeavesLegacyPath confirms the no-op contract:
// with no profiles seeded, a session opens on the legacy default-backend path
// (empty selection) and a stray profile request is harmless.
func TestSessionProfile_NoneConfiguredLeavesLegacyPath(t *testing.T) {
	sess := newProfileSession() // no SetHarnessProfiles

	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
		Profile:   "synthetic-claude",
	})
	require.NoError(t, err)
	require.Empty(t, sh.Runtime.orch.Selection().Profile,
		"no declared profiles ⇒ legacy default-backend path, never a hard error")
}
