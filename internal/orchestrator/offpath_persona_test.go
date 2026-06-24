package orchestrator_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestAskOffPath_PersonaReachesAgent asserts that when OffPathDef.Persona
// is set on the AppDef, AskOffPath forwards it as a `system_prompt` arg into
// host.agent.talk, which in turn passes it as --append-system-prompt to the
// claude binary. We use the shared fake-agent.sh which echoes the system
// prompt back in its answer (when present) so the assertion can ride the
// existing test-binary contract without a new mock layer.
func TestAskOffPath_PersonaReachesAgent(t *testing.T) {
	t.Setenv(host.AgentBinEnv, fakeAgentPath(t))

	const persona = "speak like a frontier scout"

	def := minimalOffPathApp()
	def.OffPath = &app.OffPathDef{
		Trigger: "/freeform",
		Banner:  "*** off-trail ***",
		Return:  "/onpath",
		Persona: persona,
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithChatStore(chathost.NewAdapter(rawChatStore)),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	answer, err := orch.AskOffPath(ctx, sid, "where to camp?")
	require.NoError(t, err)
	require.True(t, strings.Contains(answer, persona),
		"persona must reach the agent binary via --append-system-prompt; got answer=%q", answer)
}

// TestAskOffPath_NoPersona_StillGrounded asserts the layered contract: when no
// Persona is configured on OffPathDef there is no Layer-3 persona, but the call
// is STILL grounded — the composed system prompt carries the kitsoki Layer-1
// fragment via --system-prompt. Grounding is unconditional.
func TestAskOffPath_NoPersona_StillGrounded(t *testing.T) {
	// Reuse the standard setup which builds a minimalOffPathApp() without a
	// Persona — that's exactly the no-persona case we want to assert.
	orch, _, _, sid := setupOffPathOrch(t)
	ctx := context.Background()

	answer, err := orch.AskOffPath(ctx, sid, "anything")
	require.NoError(t, err)
	require.True(t, strings.Contains(answer, "kitsoki"),
		"no persona configured, but the call must still be grounded by the kitsoki layer: %q", answer)
}

// TestAskOffPath_AgentRefReachesAgent asserts the generalised path: when
// OffPathDef.Agent names an entry in AppDef.Agents (instead of OffPathDef.
// Persona being set inline), AskOffPath resolves the agent and threads its
// SystemPrompt through host.agent.talk via the new agents-context shim.
// This proves the new primitive round-trips the same way the back-compat
// Persona shortcut does.
func TestAskOffPath_AgentRefReachesAgent(t *testing.T) {
	t.Setenv(host.AgentBinEnv, fakeAgentPath(t))

	const agentSystemPrompt = "speak like a wise frontier guide"

	def := minimalOffPathApp()
	def.OffPath = &app.OffPathDef{
		Trigger: "/freeform",
		Banner:  "*** off-trail ***",
		Return:  "/onpath",
		Agent:   "frontier_guide",
	}
	def.Agents = map[string]*app.AgentDecl{
		"frontier_guide": {SystemPrompt: agentSystemPrompt},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithChatStore(chathost.NewAdapter(rawChatStore)),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	answer, err := orch.AskOffPath(ctx, sid, "where to camp?")
	require.NoError(t, err)
	require.Contains(t, answer, agentSystemPrompt,
		"agent-resolved system_prompt must reach the agent binary: %q", answer)
}

// TestAskOffPath_PersonaWinsOverAgent asserts the priority rule: when both
// Persona and Agent are set on OffPathDef, the inline Persona wins (it's
// the back-compat shortcut and stays authoritative when present).
func TestAskOffPath_PersonaWinsOverAgent(t *testing.T) {
	t.Setenv(host.AgentBinEnv, fakeAgentPath(t))

	const inlinePersona = "INLINE wins"
	const agentPrompt = "agent LOSES"

	def := minimalOffPathApp()
	def.OffPath = &app.OffPathDef{
		Trigger: "/freeform",
		Banner:  "*** off-trail ***",
		Return:  "/onpath",
		Persona: inlinePersona,
		Agent:   "frontier_guide",
	}
	def.Agents = map[string]*app.AgentDecl{
		"frontier_guide": {SystemPrompt: agentPrompt},
	}

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, noopHarness{},
		orchestrator.WithChatStore(chathost.NewAdapter(rawChatStore)),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	answer, err := orch.AskOffPath(ctx, sid, "anything")
	require.NoError(t, err)
	require.Contains(t, answer, inlinePersona)
	require.NotContains(t, answer, agentPrompt)
}
