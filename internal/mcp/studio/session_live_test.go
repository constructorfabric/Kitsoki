package studio_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	studio "kitsoki/internal/mcp/studio"
)

// TestOpenDrivingSession_LiveBuilderThreadsStoryPath locks the live seam that
// makes the studio MCP a first-class LLM interface: a session opened with
// harness:live must reach the injected HarnessBuilder WITH the story path (a
// live harness needs the story's def for prompt context) and the returned live
// harness must be wired into a real driving runtime. Before this seam, live was
// refused outright in the server core; this proves the plumbing the production
// studioHarnessBuilder relies on, with no LLM (the stub stands in for the live
// harness so the test stays deterministic and free).
func TestOpenDrivingSession_LiveBuilderThreadsStoryPath(t *testing.T) {
	var gotMode studio.HarnessMode
	var gotStory string
	build := func(mode studio.HarnessMode, recordingPath, storyPath string) (harness.Harness, error) {
		gotMode, gotStory = mode, storyPath
		if mode == studio.HarnessLive {
			// A non-replay harness stands in for the real LiveHarness — the
			// point is that a live build reaches a runtime, not that it routes.
			return stubReplayHarness{}, nil
		}
		return harness.NewReplay(recordingPath)
	}
	sess := studio.NewStudioSession(build)

	sh, err := sess.OpenDrivingSession(context.Background(), studio.OpenDrivingSessionParams{
		Mode:      studio.HarnessLive,
		StoryPath: cloakApp,
		TracePath: t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err, "a live build must open a real driving runtime")
	require.Equal(t, studio.HarnessLive, gotMode, "the builder sees the opt-in live mode")
	require.Equal(t, cloakApp, gotStory,
		"the builder must receive the story path so a live harness can load its def")
	require.NotNil(t, sh.Runtime, "a live handle is backed by a real driving runtime")
	require.Equal(t, "live", string(sh.Mode))
}

// TestDefaultHarnessBuilder_LiveStaysRefused keeps the no-LLM default honest:
// the in-package default never constructs a live harness (live is a
// production-injected concern in cmd/kitsoki). A regression here would let an
// MCP server with no live builder silently no-op a live open.
func TestDefaultHarnessBuilder_LiveStaysRefused(t *testing.T) {
	_, err := studio.DefaultHarnessBuilder(studio.HarnessLive, "", cloakApp)
	require.Error(t, err, "the in-package default must refuse live")
	require.True(t, strings.Contains(err.Error(), "live"),
		"the refusal names the live mode so the cause is clear: %v", err)
}
