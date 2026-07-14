package studio_test

// session_starlark_test.go — regression coverage for issue #37 / decomposition
// 2.7: an MCP-driven session must be able to run a story whose rooms invoke
// host.starlark.run with a RELATIVE script path. The handler
// (internal/host/starlark_run.go) resolves a relative script against
// KITSOKI_APP_DIR, published by loaders such as cmd/kitsoki's
// loadAppWithEnv — but the studio session runner never published it, so a
// story loaded purely through session.new (no CLI in the loop) could not
// resolve its own scripts. testdata/starlark_session is a self-contained,
// no-HTTP, no-LLM fixture whose root room's on_enter runs a Starlark script on
// entry; if KITSOKI_APP_DIR isn't published before dispatch, the on_enter's
// host.starlark.run call fails as an infra read error ("open scripts/hello.star:
// no such file or directory") that the machine absorbs into
// world.last_error/host_error rather than failing session.new outright — so
// the room silently reaches its terminal state with world.greeting still at
// its zero value. Asserting the bound value (not just a lack of a tool error)
// is what actually catches the regression.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
)

const starlarkSessionApp = "testdata/starlark_session/app.yaml"

// TestSessionNew_DrivesStarlarkBackedStoryToCompletion opens a driving session
// against a story whose root room's on_enter invokes host.starlark.run against
// a story-relative script path. Before the fix, KITSOKI_APP_DIR was never
// published for a studio-opened session, so the handler could not resolve
// "scripts/hello.star" and the on_enter failed with an infra read error,
// surfacing as a session.new tool error. After the fix, the app dir is
// published before the story loads/dispatches, the script resolves and runs,
// and the room's on_enter binds world.greeting — proving the room ran to
// completion (a terminal state) purely via session.new, with no LLM and no
// filesystem-relative assumptions about the test binary's cwd.
func TestSessionNew_DrivesStarlarkBackedStoryToCompletion(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": starlarkSessionApp,
		"harness":    "replay",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))

	var ok studio.SessionOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	require.Equal(t, "greeting_room", ok.State,
		"the terminal room's on_enter must have run the starlark script and settled here")

	res, err = callTool(ctx, cs, "session.world", map[string]any{
		"handle": ok.Handle,
		"key":    "greeting",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.world: %s", contentText(res))

	var world struct {
		OK    bool   `json:"ok"`
		Value string `json:"value"`
		Found bool   `json:"found"`
	}
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &world))
	require.True(t, world.Found)
	require.Equal(t, "hello from starlark", world.Value,
		"the starlark script's bound output must have run for real, not been skipped "+
			"(a regression here means KITSOKI_APP_DIR isn't published for this session, "+
			"so the relative script path failed to resolve — check world.last_error via "+
			"session.world for the underlying read error)")
}
