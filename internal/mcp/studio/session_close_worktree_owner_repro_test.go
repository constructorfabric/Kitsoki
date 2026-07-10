package studio_test

// session_close_worktree_owner_repro_test.go — RED reproduction for
// 2026-06-25T074726Z-session-close-leaks-worktree-owner:
//
//   studio MCP session.close releases the trace flock but NOT the
//   workspace owner marker. A session that created a workspace via
//   host.git_worktree stamps it with .kitsoki-owner pinned to that
//   session id; session.close leaves the marker behind, so every
//   later session that targets the same workspace bounces with:
//
//     workspace.create: <path> is already checked out by session "<dead-id>";
//     refusing to share
//
//   The workspace is bricked for the rest of the server-process lifetime.
//
// This test opens a session whose on_enter calls host.git_worktree create
// (seeding session_id via initial_world) so that .kitsoki-owner is stamped,
// calls CloseSession, then attempts to create the same worktree as a
// different session. The assertion is that the second create succeeds — a
// closed session must not squat the owner marker.
//
// RED on the unfixed tree: CloseSession does not release .kitsoki-owner, so
// the second create is refused with the "already checked out by session" error.
// Any correct fix (e.g. CloseSession calling a helper that removes the marker
// when it matches the closing session id) turns this GREEN without changing
// the test's behavioural contract.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	studio "kitsoki/internal/mcp/studio"
)

// worktreeReproStoryYAML is a minimal no-LLM story whose on_enter calls
// host.git_worktree create when world.workspace_id and world.repo are set.
// The only side-effect is the real on-disk git worktree create, which stamps
// .kitsoki-owner with world.session_id. No agent / harness is ever called.
const worktreeReproStoryYAML = `
app:
  id: worktree-repro
  version: 0.1.0
  title: "Workspace owner repro"

hosts:
  - host.git_worktree

world:
  session_id:   { type: string, default: "" }
  repo:         { type: string, default: "" }
  workspace_id: { type: string, default: "" }

root: idle

states:
  idle:
    on_enter:
      - when: "world.workspace_id != '' && world.repo != ''"
        invoke: host.git_worktree
        with:
          op:         create
          repo:       "{{ world.repo }}"
          id:         "{{ world.workspace_id }}"
          name:       "{{ world.workspace_id }}"
          base:       main
          session_id: "{{ world.session_id }}"
        on_error: idle
    view:
      - prose: "idle"
`

// initWorktreeReproRepo creates a minimal real git repo for the repro test.
// A committed file is required so git will accept worktree add operations.
func initWorktreeReproRepo(t *testing.T) string {
	t.Helper()
	return capsuletest.Open(t, "worktree-repro-repo")
}

// worktreeReproHarness is a no-LLM harness for the worktree repro test.
// The story's on_enter calls a host function only; no agent routing is needed.
type worktreeReproHarness struct{}

func (worktreeReproHarness) RunTurn(_ context.Context, _ harness.TurnInput) (mcpsdk.CallToolParams, error) {
	panic("worktreeReproHarness: must never route — story uses host-only on_enter")
}
func (worktreeReproHarness) Close() error { return nil }

// TestMCPSessionClose_ReleasesWorktreeOwnerForRerun is the reproduction.
// After session.close, a different session must be able to create the same
// workspace. On the unfixed tree the owner marker is never cleared, so the
// second create is refused — this test is RED.
func TestMCPSessionClose_ReleasesWorktreeOwnerForRerun(t *testing.T) {
	repo := initWorktreeReproRepo(t)
	ctx := context.Background()

	// Write the minimal story to a temp file so OpenDrivingSession can load it.
	storyDir := t.TempDir()
	storyPath := filepath.Join(storyDir, "app.yaml")
	require.NoError(t, os.WriteFile(storyPath, []byte(worktreeReproStoryYAML), 0o644))

	// Build the studio session with a no-LLM harness.
	sess := studio.NewStudioSession(
		func(_ studio.HarnessMode, _, _ string) (harness.Harness, error) {
			return worktreeReproHarness{}, nil
		},
	)

	// ── Session A ─────────────────────────────────────────────────────────────
	// initial_world seeds session_id, repo, and workspace_id before on_enter.
	// on_enter fires host.git_worktree create, which:
	//   1. creates .capsules/workspaces/reusable-worktree
	//   2. writes .capsules/workspaces/reusable-worktree/.kitsoki-owner = "closed-session"
	sh, err := sess.OpenDrivingSession(ctx, studio.OpenDrivingSessionParams{
		StoryPath: storyPath,
		TracePath: t.TempDir() + "/trace.jsonl",
		InitialWorld: map[string]any{
			"session_id":   "closed-session",
			"repo":         repo,
			"workspace_id": "reusable-worktree",
		},
	})
	require.NoError(t, err, "session A must open successfully")

	// Confirm the owner sentinel was stamped with "closed-session".
	sentinelPath := filepath.Join(repo, ".capsules", "workspaces", "reusable-worktree", ".kitsoki-owner")
	raw, err := os.ReadFile(sentinelPath)
	require.NoError(t, err, ".kitsoki-owner must exist after worktree create")
	require.Equal(t, "closed-session", strings.TrimSpace(string(raw)),
		"sentinel must name the session that created the worktree")

	// ── session.close ─────────────────────────────────────────────────────────
	// On the unfixed tree, CloseSession releases the trace sink and harness but
	// does NOT clear .kitsoki-owner. After the fix, closing the session releases
	// the marker so the workspace is available for the next session.
	require.NoError(t, sess.CloseSession(sh.Key), "session.close must succeed")

	// ── Second create (different session) ─────────────────────────────────────
	// After a correct close, "next-session" must be able to reuse the same
	// workspace. The workspace already exists on disk; the idempotency path in
	// the scripted provider checks .kitsoki-owner — if it still names "closed-session"
	// (the bug), the create is refused.
	r, herr := host.GitWorktreeHandler(ctx, map[string]any{
		"op":         "create",
		"repo":       repo,
		"id":         "reusable-worktree",
		"name":       "reusable-worktree",
		"base":       "main",
		"session_id": "next-session",
	})
	require.NoError(t, herr)
	// GATING ASSERTION — RED on the unfixed tree:
	//   r.Error = `workspace.create: "reusable-worktree" is already checked out
	//              by session "closed-session"; refusing to share — concurrent
	//              sessions on the same ticket must use distinct workspaces`
	// GREEN after any fix that makes CloseSession release the owner marker.
	require.Empty(t, r.Error,
		"after session.close, the closed session must not squat the worktree owner marker; got: %s",
		r.Error)
}
