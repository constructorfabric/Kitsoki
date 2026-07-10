// Package host — the direct-API codeact.Agent.
//
// ApiCodeactAgent implements internal/host/codeact.Agent over HTTP instead of
// forking `claude -p` per step: each Next() composes a per-step prompt (the
// same goal/budget/capabilities/observation/error addendum RealCodeactAgent
// builds), dispatches ONE OpenAI-compatible /v1/chat/completions call through
// an agent.Agent from the plugin registry (a builtin.local_llm transport
// pointed at the model's endpoint), and parses the schema-shaped reply back
// into a codeact.Emission. It is selected by AgentCodeactHandler when the
// effect's resolved plugin is a builtin.local_llm transport — see the routing
// block in agent_codeact.go. Like RealCodeactAgent there is no retry loop
// here: codeact.Run already IS the retry loop, feeding a failed step's
// ErrorEnvelope back into the next Next() call so the model can self-correct.
//
// This is the "direct-API harness" path: no external coding-agent CLI, no
// tool_search/MCP-config translation, no sandbox-bypass flag, no quota
// inference from stdout — just an HTTP response and the step schema. The step
// schema (codeactStepSchemaJSON, the {action: snippet|done} discriminated
// union) is passed as AskRequest.SchemaJSON so an OpenAI-compatible endpoint
// (with the plugin's json_schema knob set) constrains the decode; the
// done()-payload schema gate is still enforced by codeact.Run itself, so the
// "same ErrorEnvelope feedback" contract holds without any per-step plumbing
// here.
package host

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kitsoki/internal/agent"
	"kitsoki/internal/host/codeact"
	"kitsoki/internal/sysprompt"
)

// apiCodeactStepDeadline is the soft per-step deadline sent on the AskRequest
// when no caller deadline is in scope. The hard cap is the caller's context
// cancel; this only bounds a single step's HTTP call. Mirrors the generous
// default TryDispatchVerb uses for plugin-routed verb calls.
const apiCodeactStepDeadline = 10 * time.Minute

// ApiCodeactAgent is the direct-API codeact.Agent: it drives the loop through
// an agent.Agent (a builtin.local_llm OpenAI-compatible transport) instead of
// forking claude. It holds everything resolved once at construction — the
// resolved plugin, the composed system preamble, the goal/budget/capabilities,
// and the fixed step schema — so each Next() only composes its own per-step
// prompt and one Ask call. It is safe for concurrent use only in the sense
// codeact.Run is single-threaded: Run calls Next sequentially, so the agent
// never sees two Next calls in flight on the same receiver.
type ApiCodeactAgent struct {
	plug         agent.Agent
	sysPreamble  string
	goal         string
	budget       int
	capabilities []string
	stepSchema   json.RawMessage
}

// newApiCodeactAgent builds an ApiCodeactAgent wrapping plug (a resolved
// builtin.local_llm transport). The persona is the named agent's system prompt
// from the agents: block, when one resolves; empty when the call names a
// plugin-only agent with no agents: persona (the Codeact verb contract still
// grounds the call). The system preamble is composed through the same
// kitsoki→project→task layering the CLI path uses (composeAgentSystemPrompt)
// so the model sees the identical contract text either way; it is folded into
// the per-step user message because the local_llm transport carries only a
// rendered PromptText (no separate system role), matching the "system framing
// is folded in upstream" contract local_llm.go already documents.
func newApiCodeactAgent(ctx context.Context, plug agent.Agent, args map[string]any, goal string, budget int, capabilities []string) *ApiCodeactAgent {
	persona := ""
	if a, ok := resolveAgent(ctx, args); ok {
		persona = effectiveSystemPrompt(args, a)
	}
	sysPreamble := composeAgentSystemPrompt(ctx, sysprompt.Codeact, persona).SystemPrompt
	return &ApiCodeactAgent{
		plug:         plug,
		sysPreamble:  sysPreamble,
		goal:         goal,
		budget:       budget,
		capabilities: capabilities,
		stepSchema:   json.RawMessage(codeactStepSchemaJSON),
	}
}

// Next implements codeact.Agent. It composes this step's user message (the
// system preamble + the per-step addendum), calls plug.Ask with the fixed
// discriminated-union step schema, and parses the schema-shaped reply into a
// codeact.Emission. A transport error or a malformed reply is a hard error
// (codeact.Run aborts) — parity with RealCodeactAgent, which aborts on "model
// exited without calling submit" / "action=snippet but snippet is empty". The
// done()-payload schema-rejection self-correction is handled by codeact.Run
// itself, so this method does not need its own retry loop.
//
// The ctx parameter is honoured for cancellation; the provider/ladder-adjusted
// context RealCodeactAgent captures at construction is not relevant here —
// the transport (LocalLLMAgent) owns its own HTTP client and deadline.
func (a *ApiCodeactAgent) Next(ctx context.Context, step int, obs map[string]any, errEnv *codeact.ErrorEnvelope) (codeact.Emission, error) {
	oc := AgentCallCtxFrom(ctx)
	deadline := time.Now().Add(apiCodeactStepDeadline)
	req := agent.AskRequest{
		SessionID:  oc.SessionID,
		TurnNumber: oc.Turn,
		StatePath:  oc.StatePath,
		Verb:       "codeact",
		PromptText: a.sysPreamble + "\n\n" + a.buildStepPrompt(step, obs, errEnv),
		SchemaJSON: a.stepSchema,
		Deadline:   deadline,
		CallID:     newUUID(),
	}
	resp, err := a.plug.Ask(ctx, req)
	if err != nil {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: api call failed: %w", step, err)
	}
	return parseCodeactEmission(resp.Submission, step)
}

// buildStepPrompt composes the per-step user addendum: the shared step context
// (goal/budget/capabilities/observation/error) plus the API-path instruction to
// respond with a single JSON object matching the step schema. The system
// preamble (Codeact verb contract + persona) is already prepended by Next.
func (a *ApiCodeactAgent) buildStepPrompt(step int, obs map[string]any, errEnv *codeact.ErrorEnvelope) string {
	return codeactStepContext(a.goal, a.budget, step, a.capabilities, obs, errEnv) +
		"\nRespond with a single JSON object matching the step schema — exactly one of:\n" +
		"  {\"action\": \"snippet\", \"snippet\": \"<Starlark source defining def main(ctx): ...>\"}\n" +
		"  {\"action\": \"done\", \"payload\": {<final result fields>}}\n" +
		"Do not emit done until you are confident the goal is satisfied.\n"
}

// codeactStepContext builds the per-step context shared by both codeact agents
// (CLI and direct-API): the goal, remaining budget, granted capability names,
// and the previous step's observation or structured error (mutually exclusive —
// codeact.Run only ever sets one). Each agent appends its own submission
// suffix: the CLI path instructs the model to call the validator's submit tool;
// the API path instructs it to respond with a JSON object matching the step
// schema. Hoisted here so the two paths cannot drift on the goal/budget/
// capabilities/observation framing.
func codeactStepContext(goal string, budget, step int, capabilities []string, obs map[string]any, errEnv *codeact.ErrorEnvelope) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", goal)
	fmt.Fprintf(&b, "Step: %d of %d (budget remaining: %d)\n", step+1, budget, budget-step)

	if len(capabilities) > 0 {
		b.WriteString("\nCapabilities available in ctx for this call:\n")
		for _, c := range capabilities {
			desc := codeactCapabilityDescriptions[c]
			if desc == "" {
				desc = c
			}
			fmt.Fprintf(&b, "- %s\n", desc)
		}
	} else {
		b.WriteString("\nNo ctx capabilities are declared for this call — snippets may only read ctx.world.get and return a result dict.\n")
	}

	switch {
	case errEnv != nil:
		fmt.Fprintf(&b, "\nThe previous step failed:\n%s\n", errEnv.Message)
	case obs != nil:
		if obsJSON, err := json.MarshalIndent(obs, "", "  "); err == nil {
			fmt.Fprintf(&b, "\nThe previous step's observation:\n%s\n", string(obsJSON))
		}
	case step == 0:
		b.WriteString("\nThis is the first step; there is no prior observation.\n")
	}
	return b.String()
}

// parseCodeactEmission decodes a schema-shaped agent submission (the
// {action: snippet|done, ...} discriminated union) into a codeact.Emission.
// A missing/invalid action, an unparseable body, or a snippet action with an
// empty snippet is a hard error — codeact.Run wraps it and aborts — parity
// with RealCodeactAgent's "model exited without calling submit" and
// "action=snippet but snippet is empty" guards. The local_llm transport
// already strips markdown code fences from the model's content, so the
// submission here is expected to be a bare JSON object.
func parseCodeactEmission(submission json.RawMessage, step int) (codeact.Emission, error) {
	if len(strings.TrimSpace(string(submission))) == 0 {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: api returned an empty submission", step)
	}
	var out struct {
		Action  string         `json:"action"`
		Snippet string         `json:"snippet"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(submission, &out); err != nil {
		return codeact.Emission{}, fmt.Errorf("codeact step %d: parse submission: %w", step, err)
	}
	switch out.Action {
	case "snippet":
		if strings.TrimSpace(out.Snippet) == "" {
			return codeact.Emission{}, fmt.Errorf("codeact step %d: action=snippet but snippet is empty", step)
		}
		return codeact.Emission{Snippet: out.Snippet}, nil
	case "done":
		return codeact.Emission{Done: true, Payload: out.Payload}, nil
	default:
		return codeact.Emission{}, fmt.Errorf("codeact step %d: unknown action %q", step, out.Action)
	}
}
