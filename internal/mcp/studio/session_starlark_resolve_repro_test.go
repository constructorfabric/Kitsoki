package studio

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/harness"
	"kitsoki/internal/host"
)

// writeStarlarkStory writes a tiny standalone story whose ROOT state's on_enter
// invokes host.starlark.run with a STORY-RELATIVE script path (`scripts/echo.star`),
// mirroring how the real stories (e.g. stories/punch-list rooms) reference their
// scripts. The script sets one output that is bound into world; a resolution
// failure routes the effect's on_error to the `failed` state and leaves the bound
// world var empty. Returns the app.yaml path.
func writeStarlarkStory(t *testing.T, appID string) (appPath, storyDir string) {
	t.Helper()
	storyDir = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(storyDir, "scripts"), 0o755))

	// echo.star returns {"out": "hi"} — the observable proof the script resolved,
	// loaded, and ran. If the script path fails to resolve, host.starlark.run
	// errors and this output never reaches world.
	require.NoError(t, os.WriteFile(
		filepath.Join(storyDir, "scripts", "echo.star"),
		[]byte("def main(ctx):\n    return {\"out\": \"hi\"}\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(storyDir, "scripts", "echo.star.yaml"),
		[]byte("outputs:\n  out: { type: string }\n"), 0o644))

	appPath = filepath.Join(storyDir, "app.yaml")
	body := `app:
  id: ` + appID + `
  version: 0.1.0
  title: "gh37 starlark resolve repro"

hosts:
  - host.starlark.run

intents:
  enter:
    title: "Enter"

world:
  echoed: { type: string, default: "" }

root: start

states:
  start:
    view: |
      start
    on_enter:
      - invoke: host.starlark.run
        id: run_echo
        once: true
        with:
          script: scripts/echo.star
        bind:
          echoed: "out"
        on_error: failed
  failed:
    view: |
      failed
`
	require.NoError(t, os.WriteFile(appPath, []byte(body), 0o644))
	return appPath, storyDir
}

// TestStudioSession_StarlarkScriptResolves is the studio-level regression test
// for gh37: a driving studio session over a starlark-backed story must resolve
// the effect's story-relative `script: scripts/*.star` path against the story's
// own directory, NOT the server working directory.
//
// newSessionRuntime loads the story via app.LoadWithResolver but never publishes
// KITSOKI_APP_DIR, so host.starlark.run's env-only resolvePromptPath resolves the
// relative script against the process cwd (the studio server cwd), misses, and
// the effect's on_error bounces the room to the `failed` state — the script's
// output never reaches world.
//
// The test contaminates the process-global KITSOKI_APP_DIR with an unrelated dir
// (the stale/absent global posture of a replay studio session) and asserts the
// session still resolves and runs its own script. A correct fix — publishing
// KITSOKI_APP_DIR=abs(dir(storyPath)) in newSessionRuntime, or routing script
// resolution through the per-session working tree — makes this pass for ANY
// mechanism.
func TestStudioSession_StarlarkScriptResolves(t *testing.T) {
	// Contaminate the process-global with a dir that does NOT contain the story's
	// scripts — modelling a stale/foreign global left by a prior session (or none
	// at all). The session must resolve its script regardless of this value.
	t.Setenv(host.AppDirEnv, t.TempDir())

	appPath, _ := writeStarlarkStory(t, "gh37-starlark-resolve")

	sess := NewStudioSession(func(HarnessMode, string, string) (harness.Harness, error) {
		return noRouteStub{}, nil
	})
	sh, err := sess.OpenDrivingSession(context.Background(), OpenDrivingSessionParams{
		StoryPath: appPath,
		TracePath: filepath.Join(t.TempDir(), "trace.jsonl"),
	})
	require.NoError(t, err)
	require.NotNil(t, sh.Runtime)

	j, err := sh.Runtime.orch.LoadJourney(sh.Runtime.sid)
	require.NoError(t, err)

	// End-to-end outcome: the script's output reached world (proving it resolved,
	// loaded, and ran), and the room did NOT bounce to the on_error `failed` state.
	require.NotEqual(t, "failed", string(j.State),
		"room bounced to on_error state — host.starlark.run failed to resolve the story-relative script "+
			"(newSessionRuntime did not publish KITSOKI_APP_DIR for the story dir)")
	require.Equal(t, "hi", j.World.Get("echoed"),
		"the starlark script's output must reach world; empty means the script path failed to resolve "+
			"against the story dir")
}
