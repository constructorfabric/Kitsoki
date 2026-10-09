package host_test

// agent_task_seed_pluginpath_test.go — regression guard for the seed-backstop
// dead-code bug the goal-seeker dogfood surfaced (2026-07-04).
//
// The deterministic maker-seeding backstop (RegisterPendingSeedFromTaskArgs)
// used to be registered AFTER the plugin-dispatch early-return in
// AgentTaskHandler. The codex-native punch-list maker ("driver") is dispatched
// through the plugin path, so the handler returned before ever reaching the
// registration — every plugin-dispatched maker stranded with an EMPTY ticket_id
// (nested session view "(no ticket)"), and its drive bounced idle→idle without
// integrating. The unit tests on RegisterPendingSeedFromTaskArgs all passed
// because they called it directly; nothing exercised the handler's plugin path.
//
// The fix hoists the registration BEFORE the plugin dispatch (mirroring the
// worktree-resolve hoist for the same "either dispatch path" reason). This test
// drives AgentTaskHandler through the plugin path and asserts the seed is
// registered — it fails against the pre-fix ordering.

import (
	"context"
	"encoding/json"
	"testing"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/host"
)

func TestAgentTaskHandler_PluginPath_RegistersSeed(t *testing.T) {
	t.Setenv("KITSOKI_PENDING_SEED_DIR", t.TempDir())

	const parentSID = "goal-seeker-plugin"
	const story = "stories/implementation/app.yaml"

	// A benign plugin registered under the named agent so the plugin path
	// engages (handled=true) and the handler early-returns right after the
	// seed registration point.
	reg := agent.NewRegistry()
	reg.Register("driver", agent.New(agent.AskFunc(func(_ context.Context, _ agent.AskRequest) (agent.AskResponse, error) {
		return agent.AskResponse{Submission: json.RawMessage(`{"ok":true}`)}, nil
	})))

	ctx := host.WithAgentRegistry(context.Background(), reg)
	ctx = host.WithAgentEventSink(ctx, &captureSink{})
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID(parentSID), Turn: 1, StatePath: "drive.s",
	})
	// Opt into the plugin dispatch path (the codex-native maker's route) …
	ctx = host.WithAgentPluginName(ctx, "driver")
	// … and make the named agent resolvable, no write tools so the durability
	// barrier is a no-op.
	ctx = host.WithAgents(ctx, map[string]host.Agent{"driver": {Model: "gpt-5.5"}})
	// The seed is keyed by the parent session id read from ctx.
	ctx = host.WithKitsokiSessionID(ctx, parentSID)

	args := map[string]any{
		"agent": "driver",
		"context": map[string]any{
			"prompt": "prompts/drive_item.md",
			"args": map[string]any{
				"item": map[string]any{
					"story": story,
					"world_in": map[string]any{
						"ticket_id":    "WB.2",
						"ticket_title": "arena no-LLM smoke",
					},
				},
			},
		},
	}

	res, err := host.AgentTaskHandler(ctx, args)
	if err != nil {
		t.Fatalf("AgentTaskHandler returned error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("AgentTaskHandler returned Result.Error: %s", res.Error)
	}

	// The maker's studio server (session-less codex consumer) resolves by story
	// via oldest-fallback; assert the seed is present at all first.
	got, ok := host.TakePendingSeed(parentSID, story)
	if !ok {
		t.Fatal("plugin-dispatched task did NOT register a pending seed — the backstop is dead code on the plugin path (regression)")
	}
	if got["ticket_id"] != "WB.2" {
		t.Fatalf("seed ticket_id = %v, want WB.2", got["ticket_id"])
	}
	if got["ticket_title"] != "arena no-LLM smoke" {
		t.Fatalf("seed ticket_title = %v, want %q", got["ticket_title"], "arena no-LLM smoke")
	}
}
