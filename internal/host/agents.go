// Package host — Agent context shim and per-call agent resolution for
// host.oracle.{ask,talk,ask_with_mcp}.
//
// An Agent is a named system prompt (and optional model override) declared in
// the app's top-level `agents:` block (internal/app/types.go AgentDef). Effects
// reference an agent by name via the `agent: <name>` key in the effect's
// `with:` map; this package then looks up the agent in the per-session context
// and threads its system_prompt onto the claude CLI via
// `--append-system-prompt` (and `--model` when Model is set).
//
// Defined here (not in internal/app) so the host package stays free of an app
// import; the orchestrator builds a map[string]Agent from app.AppDef.Agents and
// injects it via WithAgents before dispatching each host call. This mirrors
// the chats / clarifications shim pattern (see chats.go, host.go).
package host

import (
	"context"
	"log/slog"
	"strings"

	"kitsoki/internal/bashprofile"
)

// BashProfileKind is an alias for bashprofile.Kind. The canonical enum lives in
// package bashprofile; this alias keeps host-package callers source-compatible.
type BashProfileKind = bashprofile.Kind

// BashProfileReadOnly, BashProfileCommands, and BashProfileSandboxWrite are
// aliases for the canonical constants in package bashprofile.
const (
	BashProfileReadOnly     = bashprofile.ReadOnly
	BashProfileCommands     = bashprofile.Commands
	BashProfileSandboxWrite = bashprofile.SandboxWrite
)

// BashProfile configures how the Bash tool is restricted for an agent.
// Exactly one of the three forms is in effect depending on Kind:
//
//   - ReadOnly:     Kind == BashProfileReadOnly; Commands and ScratchDir unused.
//   - Commands:     Kind == BashProfileCommands; Commands holds the argv0 allowlist.
//   - SandboxWrite: Kind == BashProfileSandboxWrite; ScratchDir is an optional
//     template for the scratch directory root (empty → system TempDir).
type BashProfile struct {
	Kind       BashProfileKind
	Commands   []string // non-nil when Kind == BashProfileCommands
	ScratchDir string   // optional when Kind == BashProfileSandboxWrite
}

// Agent is the per-call configuration applied when a host.oracle.* invocation
// names an agent. SystemPrompt is forwarded to `claude --append-system-prompt`;
// Model, when non-empty, is forwarded to `claude -p --model`. Description on
// the app-side AgentDef is documentation-only and intentionally not threaded
// through here.
//
// Tools, when non-empty, is forwarded as `--allowedTools` to claude. Per-call
// `tools:` on an effect wins over this field (precedence rule D5);
// the handler logs a warn-line when both are set.
//
// BashProfile is required when Bash is in Tools and the agent is used with
// host.oracle.ask or host.oracle.decide (enforced at loader time). Nil means
// "no Bash profile set"; task/converse handlers ignore this field.
//
// DefaultCwd, when non-empty, is used as the working directory for claude when
// the effect's working_dir arg is absent.
//
// ExternalSideEffect declares whether this agent may mutate external state
// (Mode C). Nil means the value was inferred
// from the tool surface at loader time. True → Mode C (not replayable);
// false → Mode A/B (deterministically replayable from diff).
type Agent struct {
	SystemPrompt       string
	Model              string
	Tools              []string
	BashProfile        *BashProfile
	DefaultCwd         string
	ExternalSideEffect *bool
}

// agentsKey is the unexported context key for the injected agents map.
type agentsKey struct{}

// WithAgents injects the agents map into ctx so host.oracle.* handlers can
// resolve a `with: { agent: <name> }` arg to an Agent value. Callers pass a
// snapshot of AppDef.Agents (translated by the orchestrator) so the handler
// doesn't need to import the app package. Passing nil is safe; handlers that
// see no agents map silently ignore the agent: arg (legacy / test paths).
func WithAgents(ctx context.Context, agents map[string]Agent) context.Context {
	if agents == nil {
		return ctx
	}
	return context.WithValue(ctx, agentsKey{}, agents)
}

// AgentsFromContext returns the agents map previously injected with
// WithAgents, or nil when none was injected.
func AgentsFromContext(ctx context.Context) map[string]Agent {
	if v, ok := ctx.Value(agentsKey{}).(map[string]Agent); ok {
		return v
	}
	return nil
}

// resolveAgent reads the optional `agent` arg from a handler's call args,
// looks up its Agent value in ctx, and returns (agent, ok). When the arg is
// missing/empty or no agents map is in ctx, returns (Agent{}, false). When
// the arg is present but doesn't resolve, returns (Agent{}, false) so the
// caller falls back to whatever explicit system_prompt / no-prompt path it
// would have used otherwise — agent: misspellings are caught at load time
// (see internal/app/loader.go validateAgentRef) so a runtime miss only
// happens on test scaffolding that skips the app loader.
func resolveAgent(ctx context.Context, args map[string]any) (Agent, bool) {
	name, _ := args["agent"].(string)
	if name == "" {
		return Agent{}, false
	}
	agents := AgentsFromContext(ctx)
	if agents == nil {
		return Agent{}, false
	}
	a, ok := agents[name]
	return a, ok
}

// effectiveSystemPrompt merges the call-site `system_prompt` arg (when set)
// with the resolved agent's SystemPrompt. The explicit inline value WINS so
// authors can override a named agent's prompt for one call without rewriting
// the agents block. When only one source is present that value is returned;
// when neither is set the result is empty (no --append-system-prompt added).
func effectiveSystemPrompt(args map[string]any, agent Agent) string {
	if inline, _ := args["system_prompt"].(string); inline != "" {
		return inline
	}
	return agent.SystemPrompt
}

// effectiveTools resolves the final tool list for a handler call, honouring
// the D5 precedence rule:
//
//	per-call `tools:` arg wins over agent.Tools; warn when both are set.
//
// Returns nil when neither source is set (no --allowedTools flag added).
// The returned slice is ready to join with commas for --allowedTools.
func effectiveTools(ctx context.Context, args map[string]any, agent Agent) []string {
	// Per-call tools from the effect's with: block.
	var perCall []string
	if raw, ok := args["tools"]; ok && raw != nil {
		switch v := raw.(type) {
		case []string:
			perCall = v
		case []any:
			for _, item := range v {
				if s, ok2 := item.(string); ok2 {
					perCall = append(perCall, s)
				}
			}
		case string:
			if v != "" {
				perCall = []string{v}
			}
		}
	}

	if len(perCall) > 0 && len(agent.Tools) > 0 {
		slog.WarnContext(ctx, "per-call tools: overrides agent.Tools (D5); agent.Tools ignored",
			"per_call_tools", perCall, "agent_tools", agent.Tools)
		return perCall
	}
	if len(perCall) > 0 {
		return perCall
	}
	if len(agent.Tools) > 0 {
		return agent.Tools
	}
	return nil
}

// appendAllowedToolsFlag appends --allowedTools <csv> to cliArgs when tools is
// non-empty. The CSV format is what the claude CLI expects.
func appendAllowedToolsFlag(cliArgs []string, tools []string) []string {
	if len(tools) == 0 {
		return cliArgs
	}
	return append(cliArgs, "--allowedTools", strings.Join(tools, ","))
}

// appendDefaultCwd returns workingDir if non-empty, otherwise returns
// agent.DefaultCwd. Implements the per-call working_dir wins rule.
func appendDefaultCwd(workingDir string, agent Agent) string {
	if workingDir != "" {
		return workingDir
	}
	return agent.DefaultCwd
}
