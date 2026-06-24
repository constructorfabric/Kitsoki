package studio

// session_live_no_profile_repro_test.go — regression guard for
// "session_new {harness:live} with no profile silently uses synthetic backend".
//
// BUG (now fixed): when the MCP server boots with a default harness profile that
// is a synthetic/fake backend (e.g. "synthetic-codex" pointing at an emulated
// API endpoint), calling session_new with harness:live but no profile= argument
// silently resolved to that synthetic default.  The session_new response carried
// mode:"live" with no indication of the resolved profile, so the caller (the
// maker agent) believed a real LLM was backing the session — until agent rooms
// returned empty output and acceptance failed 5 turns later.
//
// FIXED by d8194886 ("fail loud on harness:live with no profile when backends
// declared"): OpenDrivingSession now returns a hard error when mode==live,
// profile=="", and backends ARE declared — BEFORE the silent default-profile
// fallback. The legacy single-default path (no profiles declared) is untouched.
// This file was the RED-gate; it is now GREEN and guards against regression.
//
// Issue: issues/bugs/2026-06-23T092411Z-mcp-live-harness-no-profile-uses-synthetic.md (RESOLVED)

import (
	"context"
	"fmt"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	"kitsoki/internal/orchestrator"
)

// syntheticDefaultProfiles returns a profile map where the DEFAULT profile is
// the "synthetic-codex" backend — the exact scenario that surfaced during the
// imports-rewriter dogfood delivery.  The "codex" (real) profile is also
// present so the caller CAN select a real backend by name.
func syntheticDefaultProfiles() (profiles map[string]orchestrator.HarnessProfile, defaultProfile string) {
	return map[string]orchestrator.HarnessProfile{
		"codex": {
			Name:    "codex",
			Backend: "codex",
			Model:   "codex-mini",
		},
		"synthetic-codex": {
			Name:    "synthetic-codex",
			Backend: "codex",
			Model:   "hf:codex-mini",
			Env:     map[string]string{"OPENAI_BASE_URL": "https://api.synthetic.new/openai"},
		},
	}, "synthetic-codex"
}

// liveCapableStub is a harness that accepts the live open call without
// actually contacting an LLM — the exact structural stand-in
// TestOpenDrivingSession_LiveBuilderThreadsStoryPath uses.  We only need the
// session to OPEN; we never call session.drive, so routing never fires.
type liveCapableStub struct{ closed bool }

func (s *liveCapableStub) RunTurn(_ context.Context, _ harness.TurnInput) (mcpsdk.CallToolParams, error) {
	return mcpsdk.CallToolParams{}, fmt.Errorf("liveCapableStub: RunTurn must not be called in this test")
}
func (s *liveCapableStub) Close() error { s.closed = true; return nil }

// newLiveCapableSession returns a StudioSession backed by a stub builder that:
//   - returns a liveCapableStub for HarnessLive (so session_new(harness:live) succeeds)
//   - returns an error for HarnessReplay (keeps the replay path unchanged)
func newLiveCapableSession() *StudioSession {
	return NewStudioSession(func(mode HarnessMode, recordingPath, storyPath string) (harness.Harness, error) {
		if mode == HarnessLive {
			return &liveCapableStub{}, nil
		}
		return nil, fmt.Errorf("replay not configured in this test")
	})
}

// TestSessionLiveNoProfile_SilentlySyntheticDefault is the RED-gate.
//
// It opens a session with harness:live + no profile while the server's default
// profile is "synthetic-codex".  The test asserts that either:
//
//  1. session_new returns an error (the hard-error safeguard), OR
//  2. the returned handle carries the resolved profile name so the caller knows
//     it went synthetic.
//
// Currently NEITHER is true: the session opens with mode:"live" and the
// orchestrator silently selects "synthetic-codex", but the caller receives no
// signal.  This test fails today and must pass after the fix.
func TestSessionLiveNoProfile_SilentlySyntheticDefault(t *testing.T) {
	sess := newLiveCapableSession()
	profiles, defaultProfile := syntheticDefaultProfiles()
	sess.SetHarnessProfiles(profiles, defaultProfile) // default = synthetic-codex

	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
		Mode:      HarnessLive,
		Profile:   "", // deliberately omitted — the bug scenario
	})

	// Expected after fix — ONE of the following must hold:
	//
	// Option A: hard error ("no real backend for harness=live").
	if err != nil {
		// If the fix returns an error, the test passes.  Good.
		t.Logf("session_new returned error (option A — hard error): %v", err)
		return
	}

	// No error returned — the session opened.  Option B must hold: the resolved
	// profile must be surfaced in the handle so the caller isn't blindly using
	// a synthetic backend.
	require.NotNil(t, sh, "session handle must not be nil when no error returned")
	require.NotNil(t, sh.Runtime, "driving runtime must be wired")

	resolvedProfile := sh.Runtime.orch.Selection().Profile
	t.Logf("resolved profile: %q (server default was %q)", resolvedProfile, defaultProfile)

	// BUG assertion: the orchestrator secretly selected "synthetic-codex" but
	// session_new's SessionOpenOK carries only {ok, handle, state, mode:"live"}
	// with no profile field — the caller sees no signal.
	//
	// For the fix, the caller needs the resolved profile in the return.  We
	// approximate that here by checking the handle itself exposes the selection,
	// but the real requirement is that handleSessionNew returns it on the wire.
	//
	// After the fix, the SessionOpenOK struct should gain a "profile" field.
	// Until then, this t.Fatal proves the bug is real and measurable.
	if resolvedProfile == "synthetic-codex" {
		t.Fatalf(
			"BUG CONFIRMED: session_new(harness:live, no profile) silently resolved to %q "+
				"(the server synthetic default) without any signal in the response. "+
				"session_new must either error or surface the resolved profile.",
			resolvedProfile,
		)
	}

	// If neither the error nor the synthetic selection was observed, something
	// unexpected happened — surface it.
	require.Equal(t, "", resolvedProfile,
		"unexpected: session opened with a non-synthetic selection — "+
			"this is neither the bug nor the fix, check what changed")
}

// TestSessionLiveExplicitProfile_NotAffectedByFix confirms that an explicit
// profile= selection still works correctly after the fix is applied — so the
// fix does not break the intentional explicit-selection path.
func TestSessionLiveExplicitProfile_NotAffectedByFix(t *testing.T) {
	sess := newLiveCapableSession()
	profiles, defaultProfile := syntheticDefaultProfiles()
	sess.SetHarnessProfiles(profiles, defaultProfile)

	// Explicit profile="codex" — the caller KNOWS what backend they want.
	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: profileCloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
		Mode:      HarnessLive,
		Profile:   "codex", // explicit, not synthetic
	})
	require.NoError(t, err, "explicit valid profile must succeed")
	require.NotNil(t, sh.Runtime)
	require.Equal(t, "codex", sh.Runtime.orch.Selection().Profile,
		"explicit profile selection must reach the orchestrator")
}
