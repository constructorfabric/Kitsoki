package studio

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// story_turn.go — the dry-run debugging microscope.
//
// `story.turn` applies ONE transition to a story — given a starting state, a
// world, and a chosen intent (+ slots) — and returns the rich, persistence-free
// outcome: the next state, the world before/after, the effects applied, every
// host.* call WITH its error, the guard hint on a rejection, and the rendered
// view. It is `kitsoki turn`'s stateless probe (orchestrator.OneShot) reached
// over MCP.
//
// Why it can't be done through the existing surface: render.tui renders a
// state+world but never APPLIES an intent; session.submit advances a real,
// persisted machine and needs a live handle. Neither answers the question the
// kitsoki-debugging skill exists for — "I drove this intent and the room
// silently bounced to idle; which host call failed?" — because the TUI's
// `on_error:` arcs swallow exactly that. story.turn surfaces it: the OneShot
// host_calls carry the underlying error the arc hid.
//
// No LLM: only the direct-intent path is exposed, which goes straight to the
// machine; the harness (the sole interpretive seam) is never invoked, so a
// no-op stub satisfies orchestrator.New's non-nil requirement. The orchestrator
// is built on an in-memory store that is discarded on return — nothing persists.
// Host effects DO execute (that is how a failing host.run surfaces), so the tool
// is a write surface and a read-only server omits it.

// registerStoryTurnTool wires story.turn. Omitted on a read-only server: a turn
// can fire host.run / host.agent effects that mutate the worktree.
func (srv *Server) registerStoryTurnTool() {
	if srv.readOnly {
		return
	}
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "story.turn",
		Description: "Dry-run ONE transition and see exactly what happens — the debugging microscope render.tui/session.submit can't give you. {dir? (story dir or app.yaml; defaults to the bound workspace), state (required, the starting state path), intent (required, the intent to apply), slots? (intent slots), world? (world var overrides; merged over the schema defaults)}. Goes straight to the machine (NO LLM, NO live handle) and persists NOTHING. Returns the full OneShot outcome: {mode, prev_state, next_state, world_before, world_after, effects_applied, host_calls[ (each with its error) ], view_rendered, allowed_intents, error_code, error_message, guard_hint, slots_needed}. Use it when a room 'silently bounced to idle' — host_calls surfaces the failure the on_error arc swallowed. Host effects DO run (that's how a failing host.run shows up).",
	}, srv.handleStoryTurn)
}

// StoryTurnArgs is the input to story.turn.
type StoryTurnArgs struct {
	// Dir overrides the workspace (story dir or app.yaml); defaults to the bound
	// workspace handle.
	Dir string `json:"dir,omitempty"`
	// State is the starting state path (required).
	State string `json:"state"`
	// Intent is the intent to apply directly (required).
	Intent string `json:"intent"`
	// Slots are the intent's slots.
	Slots map[string]any `json:"slots,omitempty"`
	// World overrides world vars, merged over the story's schema defaults.
	World map[string]any `json:"world,omitempty"`
}

// handleStoryTurn builds the same stateless one-shot orchestrator `kitsoki turn`
// does and returns OneShot's rich result verbatim. The in-memory store is
// discarded on return, so nothing is persisted.
func (srv *Server) handleStoryTurn(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StoryTurnArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.State == "" {
		return buildToolError(ErrBadRequest, "story.turn: state is required"), nil, nil
	}
	if args.Intent == "" {
		return buildToolError(ErrBadRequest, "story.turn: intent is required (story.turn drives a chosen intent, no free-text routing)"), nil, nil
	}
	_, appPath, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}

	def, err := app.LoadWithResolver(appPath, nil, srv.importResolver)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.turn: load story: %v", err)), nil, nil
	}

	// Default world: schema defaults, then caller overrides (mirrors turn.go).
	defaultWorld := machine.WorldFromSchema(def.World)
	merged := make(map[string]any, len(defaultWorld.Vars)+len(args.World))
	for k, v := range defaultWorld.Vars {
		merged[k] = v
	}
	for k, v := range args.World {
		merged[k] = v
	}

	m, err := machine.New(def)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.turn: build machine: %v", err)), nil, nil
	}
	s, err := store.OpenMemory()
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.turn: open in-memory store: %v", err)), nil, nil
	}
	defer func() { _ = s.Close() }()

	h := &storyTurnNoRunHarness{}

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)
	host.RegisterStarlarkBindings(hostReg, def.StarlarkHostBindings)
	if err := hostReg.ValidateAllowList(def.Hosts); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.turn: validate hosts: %v", err)), nil, nil
	}

	// Chats + agent registries so a room that invokes host.chat.* / host.agent.*
	// (via an effect's agent: field) is exercised faithfully. The DB is the same
	// in-memory store, discarded on return.
	chatStore, err := chats.NewStore(s.DB())
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.turn: init chats store: %v", err)), nil, nil
	}
	agentReg, agentRegErr := agent.BuildRegistryFromDef(def, h)
	if agentRegErr != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.turn: build agent registry: %v", agentRegErr)), nil, nil
	}
	defer func() { _ = agentReg.Close() }()

	orchOpts := []orchestrator.Option{
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithChatStore(chathost.NewAdapter(chatStore)),
		orchestrator.WithAgentRegistry(agentReg),
	}
	if opt, ok := semanticRoutingEnvOption(); ok {
		orchOpts = append(orchOpts, opt)
	}
	orch := orchestrator.New(def, m, s, h, orchOpts...)

	result, err := orch.OneShot(ctx, orchestrator.OneShotInput{
		State:  app.StatePath(args.State),
		World:  merged,
		Intent: args.Intent,
		Slots:  args.Slots,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("story.turn: %v", err)), nil, nil
	}
	return nil, storyTurnView{Mode: result.Mode.String(), OneShotResult: result}, nil
}

// storyTurnView wraps OneShotResult so the int OutcomeMode is emitted as its
// stable string name (mirrors cmd/kitsoki's turnOutput).
type storyTurnView struct {
	Mode string `json:"mode"`
	*orchestrator.OneShotResult
}

// storyTurnNoRunHarness is the no-op harness for the direct-intent path: it is
// never called (the intent goes straight to the machine), but orchestrator.New
// requires a non-nil harness. If anything ever does invoke it, it errors loudly
// rather than silently reaching for an LLM.
type storyTurnNoRunHarness struct{}

func (n *storyTurnNoRunHarness) RunTurn(context.Context, harness.TurnInput) (mcpsdk.CallToolParams, error) {
	return mcpsdk.CallToolParams{}, fmt.Errorf("story.turn: the harness must not be invoked on the direct-intent path")
}

func (n *storyTurnNoRunHarness) Close() error { return nil }
