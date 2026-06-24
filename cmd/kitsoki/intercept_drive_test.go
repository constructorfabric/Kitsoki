package main

// intercept_drive_test.go — cmd-layer routing for the conflict-capable
// intercept. These assert the GATE's branch decision (OneShot fast path vs.
// multi-turn drive signal) without ever driving — no LLM, no persisted session,
// no real harness. The drive itself is covered free at the orchestrator level
// (internal/orchestrator/intercept_drive_test.go).

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// A match in a binding whose app declares a room flagged intercept_drive: rest
// (git-ops, via its conflict room) routes to the MULTI-TURN signal: matched, but
// NOT one-shotted (OneShot nil) — the caller is told to drive instead.
func TestInterceptEngine_GitOpsRebase_RoutesToMultiTurn(t *testing.T) {
	appPath := filepath.Join("..", "..", "stories", "git-ops", "app.yaml")
	res, err := runInterceptEngine(context.Background(), interceptEngineInput{
		AppPath: appPath,
		Room:    "intercept",
		Input:   "rebase onto main",
		Bar:     0.90,
	})
	require.NoError(t, err)
	require.True(t, res.Matched, "‘rebase onto main’ must match the rebase intent")
	require.Equal(t, "rebase", res.Intent)
	require.True(t, res.MultiTurn, "a git-ops match must route to the multi-turn drive (conflict room is flagged)")
	require.Nil(t, res.OneShot, "the multi-turn path must NOT run the stateless OneShot")
	require.Equal(t, 0, res.Exit)
}

// A match in a binding with NO flagged room (intercept-demo) keeps the stateless
// OneShot fast path — MultiTurn false, OneShot populated.
func TestInterceptEngine_DemoRebase_StaysOneShot(t *testing.T) {
	appPath := filepath.Join("..", "..", "stories", "intercept-demo", "app.yaml")
	res, err := runInterceptEngine(context.Background(), interceptEngineInput{
		AppPath: appPath,
		Room:    "commands",
		Input:   "rebase this onto main",
		Bar:     0.90,
	})
	require.NoError(t, err)
	require.True(t, res.Matched)
	require.False(t, res.MultiTurn, "a binding with no flagged room must not escalate")
	require.NotNil(t, res.OneShot, "the fast path must run the stateless OneShot")
}

// mergeClaudeHook writes the raised Claude timeout on a fresh install so a
// multi-turn drive is not killed at Claude's 30s default.
func TestMergeClaudeHook_WritesRaisedTimeout(t *testing.T) {
	updated, changed := mergeClaudeHook(map[string]any{})
	require.True(t, changed)
	h := findClaudeHookEntry(mustEvents(t, updated))
	require.NotNil(t, h, "the kitsoki hook entry must be present")
	to, _ := h["timeout"].(int)
	require.Equal(t, interceptHookTimeoutSeconds, to, "a fresh install must carry the raised timeout")
}

// mergeClaudeHook reconciles a pre-existing entry that lacks the timeout
// (an older install) up to the required value, reporting changed.
func TestMergeClaudeHook_ReconcilesMissingTimeout(t *testing.T) {
	legacy := map[string]any{
		"hooks": map[string]any{
			"UserPromptSubmit": []any{
				map[string]any{
					"hooks": []any{
						map[string]any{"type": "command", "command": hookCommandString},
					},
				},
			},
		},
	}
	updated, changed := mergeClaudeHook(legacy)
	require.True(t, changed, "an entry missing the timeout must be reconciled")
	h := findClaudeHookEntry(mustEvents(t, updated))
	require.NotNil(t, h)
	to, _ := h["timeout"].(int)
	require.Equal(t, interceptHookTimeoutSeconds, to)

	// Re-running is now idempotent.
	_, changed2 := mergeClaudeHook(updated)
	require.False(t, changed2, "re-running install must be a no-op once the timeout is present")
}

// mustEvents extracts the UserPromptSubmit event list from a settings map.
func mustEvents(t *testing.T, settings map[string]any) []any {
	t.Helper()
	hooks, ok := settings["hooks"].(map[string]any)
	require.True(t, ok, "settings must carry a hooks map")
	events, ok := hooks["UserPromptSubmit"].([]any)
	require.True(t, ok, "hooks must carry a UserPromptSubmit list")
	return events
}
