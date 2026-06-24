package studio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/render"
)

// writeMinimalStoryWithSchema writes a tiny standalone story under a fresh temp
// dir containing a schemas/<schemaName> file, and returns the app.yaml path and
// the story dir. The story has no agent room — the test asserts on the per-session
// prompt renderer's schema resolution directly, which is the seam host.agent.task's
// validator resolution flows through.
func writeMinimalStoryWithSchema(t *testing.T, appID, schemaName string) (appPath, storyDir string) {
	t.Helper()
	storyDir = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(storyDir, "schemas"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(storyDir, "schemas", schemaName),
		[]byte(`{"type":"object"}`), 0o644))

	appPath = filepath.Join(storyDir, "app.yaml")
	body := `app:
  id: ` + appID + `
  version: 0.1.0
  title: "Concurrent Schema Test"

hosts:
  - host.run

intents:
  enter:
    title: "Enter"

root: lobby

states:
  lobby:
    view: |
      Lobby.
`
	require.NoError(t, os.WriteFile(appPath, []byte(body), 0o644))
	return appPath, storyDir
}

// TestConcurrentSessions_SchemaResolvesPerSession is the studio-level regression
// test for the P1 concurrent-session schema-bleed bug
// (issues/bugs/2026-06-23T100426Z-studio-concurrent-sessions-agent-schema-bleed.md).
//
// TWO live driving sessions on DIFFERENT stories run in ONE studio session. Each
// session.new(harness:live) publishes KITSOKI_APP_DIR (a process-global env var)
// pointing at its own story dir, so whichever opened LAST wins the global. When the
// EARLIER session then dispatches host.agent.task, its acceptance.schema (a
// story-relative path like `schemas/x.json`) MUST resolve against ITS OWN story dir —
// carried per-dispatch by the orchestrator's prompt renderer (built from def.BaseDir)
// — not the contaminated global.
//
// Before the fix the validator's schema resolution (buildValidatorMCPServer) read
// only the process-global KITSOKI_APP_DIR, so session A's dispatch resolved against
// session B's story dir (the observed cherny-loop bleed). After the fix it resolves
// through the per-call renderer first, isolating each concurrent session.
//
// This test asserts directly on each session's prompt renderer — the seam the
// validator resolution now flows through (resolvePromptPathCtx) — with the global env
// contaminated by the "other" session, mirroring the live failure deterministically
// and with no LLM.
func TestConcurrentSessions_SchemaResolvesPerSession(t *testing.T) {
	sess := NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
		return noRouteStub{}, nil
	})

	// Session A: story A with schemas/a.json.
	appA, storyDirA := writeMinimalStoryWithSchema(t, "concurrent-schema-a", "a.json")
	shA, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: appA,
		TracePath: t.TempDir() + "/traceA.jsonl",
	})
	require.NoError(t, err)
	require.NotNil(t, shA.Runtime)

	// Session B: story B with schemas/b.json (a DIFFERENT story, opened SECOND).
	appB, storyDirB := writeMinimalStoryWithSchema(t, "concurrent-schema-b", "b.json")
	shB, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: appB,
		TracePath: t.TempDir() + "/traceB.jsonl",
	})
	require.NoError(t, err)
	require.NotNil(t, shB.Runtime)

	// Simulate the live MCP harness builder publishing the process-global
	// KITSOKI_APP_DIR for the most-recently-opened live session (story B). In the
	// real `kitsoki mcp` process this is set by loadAppWithEnv inside
	// studioHarnessBuilder on each session.new(harness:live).
	t.Setenv(host.AppDirEnv, storyDirB)

	// Each session's orchestrator records its OWN story dir as def.BaseDir — the
	// per-session value the orchestrator feeds to buildPromptRenderer (and which the
	// process-global KITSOKI_APP_DIR clobbers across concurrent sessions). Prove the
	// per-session bases survive a concurrent open even after the global env was
	// repointed at story B.
	require.Equal(t, storyDirA, shA.Runtime.def.BaseDir,
		"session A must retain its own story dir as BaseDir after session B opened")
	require.Equal(t, storyDirB, shB.Runtime.def.BaseDir,
		"session B must retain its own story dir as BaseDir")
	require.NotEqual(t, shA.Runtime.def.BaseDir, shB.Runtime.def.BaseDir,
		"concurrent sessions must carry distinct per-session story dirs")

	// Build each session's prompt renderer exactly as the orchestrator does (from
	// def.BaseDir) — this is the per-dispatch base host.agent.task resolves the
	// validator schema against via resolvePromptPathCtx. Each must resolve its
	// story-relative schema to ITS OWN story dir, NOT the global env (story B) and
	// NOT each other.
	rendererA, err := render.NewPromptRenderer(render.PromptPath{Story: shA.Runtime.def.BaseDir}, true)
	require.NoError(t, err)
	rendererB, err := render.NewPromptRenderer(render.PromptPath{Story: shB.Runtime.def.BaseDir}, true)
	require.NoError(t, err)

	resolvedA, okA := rendererA.ResolvePromptName("schemas/a.json")
	require.True(t, okA, "session A's renderer must resolve its own schema")
	require.Equal(t, filepath.Join(storyDirA, "schemas", "a.json"), resolvedA,
		"session A's schema must resolve against story A — not the globally-last-loaded "+
			"session B's KITSOKI_APP_DIR (the concurrent-session bleed)")

	resolvedB, okB := rendererB.ResolvePromptName("schemas/b.json")
	require.True(t, okB, "session B's renderer must resolve its own schema")
	require.Equal(t, filepath.Join(storyDirB, "schemas", "b.json"), resolvedB,
		"session B's schema must resolve against story B")

	// And the bleed itself: session A must NOT see story B's schema as its own.
	_, leaks := rendererA.ResolvePromptName("schemas/b.json")
	require.False(t, leaks,
		"session A's renderer must NOT resolve story B's schema — that is the cross-session bleed")
}
