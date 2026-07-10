package host

import (
	"context"
	"testing"

	"kitsoki/internal/app"

	"github.com/stretchr/testify/require"
)

// TestRegisterPendingSeedFromTaskArgs_BackgroundJobCtx is the regression guard for
// the blocker the goal-seeker dogfood surfaced (2026-07-04, second run): the
// punch-list drive room invokes host.agent.task with `background: true`, so the
// handler runs on a scheduler job ctx that carries AgentCallCtx (the orchestrator
// stamps it with the parent session id) but NOT the WithKitsokiSessionID key, and
// the studio process env has no KITSOKI_SESSION_ID. So kitsokiSessionIDFromCtx is
// empty and the OLD `if sessionID == "" { return }` guard made registration a
// silent no-op — no seed file was ever written, and every codex maker stranded
// with an empty ticket_id. This reproduces that exact ctx shape and asserts the
// seed is registered (via the AgentCallCtx fallback) AND consumable by a
// session-less codex consumer via oldest-fallback.
func TestRegisterPendingSeedFromTaskArgs_BackgroundJobCtx(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	t.Setenv("KITSOKI_SESSION_ID", "") // no process-env session id (the studio reality)

	story := "stories/implementation/app.yaml"
	// The background-job ctx: AgentCallCtx present, no WithKitsokiSessionID.
	ctx := WithAgentCallCtx(context.Background(), AgentCallCtx{SessionID: app.SessionID("goal-seeker-bg")})
	require.Empty(t, kitsokiSessionIDFromCtx(ctx), "precondition: the old bail condition (empty session id) must hold")

	args := map[string]any{
		"context": map[string]any{
			"prompt": "prompts/drive_item.md",
			"args": map[string]any{
				"item": map[string]any{
					"story":    story,
					"world_in": map[string]any{"ticket_id": "WB.2", "ticket_title": "arena on main"},
				},
			},
		},
	}
	RegisterPendingSeedFromTaskArgs(ctx, args)

	// A codex-spawned studio MCP consumes with an empty session id (no forwarded
	// env) → must resolve via oldest-fallback. This is the real maker's path.
	got, ok := TakePendingSeed("", story)
	require.True(t, ok, "background-job dispatch must register a seed via the AgentCallCtx session id, not bail on empty kitsokiSessionIDFromCtx")
	require.Equal(t, "WB.2", got["ticket_id"])
	require.Equal(t, "arena on main", got["ticket_title"])
}

// TestRegisterPendingSeedFromTaskArgs_ItemManifest proves the auto-registration
// path host.agent.task drives before spawning the maker: a task whose
// context.args carry the punch-list item manifest (item.{story, world_in})
// registers a pending seed keyed by the parent session id (read from ctx), which
// the studio server then consumes for exactly that story. No LLM / no subprocess.
func TestRegisterPendingSeedFromTaskArgs_ItemManifest(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	const parentSID = "goal-seeker-1"

	story := "stories/bugfix/app.yaml"
	world := map[string]any{"ticket_id": "0.2", "ticket_title": "Fix the thing"}
	args := map[string]any{
		"agent": "driver",
		"context": map[string]any{
			"prompt": "prompts/drive_item.md",
			"args": map[string]any{
				"item": map[string]any{
					"story":    story,
					"world_in": world,
				},
			},
		},
	}

	// The parent session id rides on ctx (WithKitsokiSessionID), exactly as
	// agent-serve sets it per-RPC — not the process-global env.
	ctx := WithKitsokiSessionID(context.Background(), parentSID)
	RegisterPendingSeedFromTaskArgs(ctx, args)

	got, ok := TakePendingSeed(parentSID, story)
	require.True(t, ok, "a pending seed must be registered from item.{story, world_in}")
	require.Equal(t, "0.2", got["ticket_id"], "period-id ticket must survive verbatim")
	require.Equal(t, "Fix the thing", got["ticket_title"])

	// Consume-once: a second take on the same key finds nothing.
	_, ok2 := TakePendingSeed(parentSID, story)
	require.False(t, ok2, "the seed is consume-once")
}

// TestRegisterPendingSeedFromTaskArgs_NoSeedIsNoop confirms the guardrails: the
// (story, world) extraction is the no-op gate — a missing story or missing world
// leaves the store empty, so an ordinary task without a seed is unaffected. Note
// the session id is NO LONGER a gate: a full item with no session lineage still
// registers (reachable via the consumer's oldest-fallback), because gating on a
// non-empty session id defeated the backstop in the exact case it exists for (a
// codex-spawned studio MCP with no forwarded session env). See BackgroundJobCtx.
func TestRegisterPendingSeedFromTaskArgs_NoSeedIsNoop(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	t.Setenv("KITSOKI_SESSION_ID", "")

	// A full item with NO session lineage anywhere still registers — the seed must
	// not depend on a session id being threaded (the background-job / codex reality).
	args := map[string]any{
		"context": map[string]any{
			"args": map[string]any{
				"item": map[string]any{
					"story":    "stories/bugfix/app.yaml",
					"world_in": map[string]any{"ticket_id": "0.2"},
				},
			},
		},
	}
	RegisterPendingSeedFromTaskArgs(context.Background(), args)
	got, ok := TakePendingSeed("", "stories/bugfix/app.yaml")
	require.True(t, ok, "a full item registers even with no session lineage (oldest-fallback consumer)")
	require.Equal(t, "0.2", got["ticket_id"])

	// Session id present but no world ⇒ still a no-op (the real gate is the seed world).
	ctx := WithKitsokiSessionID(context.Background(), "sid-x")
	RegisterPendingSeedFromTaskArgs(ctx, map[string]any{
		"context": map[string]any{
			"args": map[string]any{
				"item": map[string]any{"story": "stories/bugfix/app.yaml"},
			},
		},
	})
	_, ok = TakePendingSeed("sid-x", "stories/bugfix/app.yaml")
	require.False(t, ok, "no world_in ⇒ nothing registered")
}

// TestRegisterPendingSeedFromTaskArgs_FlatShape covers the generic fallback
// shape (context.args.{story, world_in}) for a task that is not the punch-list
// item manifest.
func TestRegisterPendingSeedFromTaskArgs_FlatShape(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	const sid = "sid-flat"
	story := "stories/delivery/app.yaml"
	args := map[string]any{
		"context": map[string]any{
			"args": map[string]any{
				"story":         story,
				"initial_world": map[string]any{"ticket_id": "9"},
			},
		},
	}
	RegisterPendingSeedFromTaskArgs(WithKitsokiSessionID(context.Background(), sid), args)
	got, ok := TakePendingSeed(sid, story)
	require.True(t, ok)
	require.Equal(t, "9", got["ticket_id"])
}

// TestTakePendingSeed_SessionlessConsumerFallsBack is the codex path: the parent
// registers a seed tagged with its own session id, but the maker's studio server
// is a codex-spawned `kitsoki mcp` that never inherited KITSOKI_SESSION_ID, so it
// consumes with an EMPTY session id. The oldest-fallback must still hand it the
// seed — otherwise every gpt-5.5/codex maker strands with an empty ticket_id.
func TestTakePendingSeed_SessionlessConsumerFallsBack(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	story := "stories/implementation/app.yaml"
	require.NoError(t, RegisterPendingSeed("goal-seeker-7", story, map[string]any{"ticket_id": "0.2"}))

	got, ok := TakePendingSeed("", story) // codex consumer: no forwarded session id
	require.True(t, ok, "a session-less consumer must still resolve the story's seed")
	require.Equal(t, "0.2", got["ticket_id"])

	_, ok2 := TakePendingSeed("", story)
	require.False(t, ok2, "still consume-once")
}

// TestTakePendingSeed_PrefersMatchingLineage proves a session-aware consumer
// (claude/GLM maker whose env forwarded the parent id) picks its OWN seed even
// when another parent's seed for the same story sits ahead of it in the FIFO —
// preserving cross-parent isolation the story-only key would otherwise lose.
func TestTakePendingSeed_PrefersMatchingLineage(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())
	story := "stories/implementation/app.yaml"
	require.NoError(t, RegisterPendingSeed("parent-A", story, map[string]any{"ticket_id": "A"}))
	require.NoError(t, RegisterPendingSeed("parent-B", story, map[string]any{"ticket_id": "B"}))

	// Consumer B matches the second (younger) entry despite A being oldest.
	got, ok := TakePendingSeed("parent-B", story)
	require.True(t, ok)
	require.Equal(t, "B", got["ticket_id"], "must prefer the lineage-matching entry, not the oldest")

	// A's seed is untouched and still resolvable.
	gotA, okA := TakePendingSeed("parent-A", story)
	require.True(t, okA)
	require.Equal(t, "A", gotA["ticket_id"])
}
