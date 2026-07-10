// Package host — Agent context shim and per-call agent resolution for
// host.agent.{ask,talk,ask_with_mcp}.
//
// An Agent is a named system prompt (and optional model override) declared in
// the app's top-level `agents:` block (internal/app/types.go AgentDef). Effects
// reference an agent by name via the `agent: <name>` key in the effect's
// `with:` map; this package then looks up the agent in the per-session context.
// SystemPrompt becomes the Layer-3 (task) body composed by
// composeAgentSystemPrompt (sysprompt.go): kitsoki → project → task, joined
// stable-prefix-first and forwarded via `--system-prompt`, which REPLACES
// Claude Code's default (and `--model` when Model is set). The older
// `--append-system-prompt`-onto-Claude's-default posture survives only behind
// the explicit per-agent `inherit_claude_default` escape hatch — see
// appendComposedSystemPrompt for both paths.
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
	"kitsoki/internal/effect"
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

// Agent is the per-call configuration applied when a host.agent.* invocation
// names an agent. SystemPrompt becomes the Layer-3 (task) body composed by
// composeAgentSystemPrompt and forwarded via `claude --system-prompt` (default
// path) or `--append-system-prompt` (inherit_claude_default escape hatch);
// Model, when non-empty, is forwarded to `claude -p --model`. Description on
// the app-side AgentDef is documentation-only and intentionally not threaded
// through here.
//
// Tools, when non-empty, is forwarded as `--allowedTools` to claude. Per-call
// `tools:` on an effect wins over this field (precedence rule D5);
// the handler logs a warn-line when both are set.
//
// BashProfile is required when Bash is in Tools and the agent is used with
// host.agent.ask or host.agent.decide (enforced at loader time). Nil means
// "no Bash profile set"; task/converse handlers ignore this field.
//
// DefaultCwd, when non-empty, is used as the working directory for claude when
// the effect's working_dir arg is absent.
//
// Effect carries the agent's RESOLVED effect class (internal/app's loader
// populates this from AgentDecl.Effect after resolving the taxonomy's
// declared-vs-tool-surface-join invariants — see resolveAgentEffect).
// Empty means "not populated by the loader" (a hand-constructed Agent, e.g.
// in a unit test); resolveAgentEffect falls back to ExternalSideEffect and
// then to a Write default in that case — see resolveAgentEffect below.
//
// ExternalSideEffect is a DEPRECATED alias mirroring whether this agent may
// mutate external state (Mode C). Nil means neither Effect nor
// ExternalSideEffect was populated. True ⟺ Effect == external (Mode C, not
// replayable); false ⟺ Effect <= write (Mode A/B, deterministically
// replayable from diff).
type Agent struct {
	SystemPrompt string
	Model        string
	// Effort, when non-empty, is forwarded to `claude --effort`
	// (low|medium|high|xhigh|max). An effect's `with: { effort }` arg overrides
	// it per call; empty leaves the CLI default.
	Effort             string
	Tools              []string
	Toolbox            string
	MCPTools           []string
	MCPServers         map[string]any
	BashProfile        *BashProfile
	DefaultCwd         string
	Effect             effect.Effect
	ExternalSideEffect *bool
	Permissions        AgentPermissions
	// InheritClaudeDefault, when true, opts this agent out of the layered
	// system prompt: its persona is appended (--append-system-prompt) onto
	// Claude Code's default rather than composed under the kitsoki + project
	// layers and passed via --system-prompt. Migration escape hatch; default
	// false. See internal/sysprompt and docs/architecture/system-prompt.md.
	InheritClaudeDefault bool

	// Provider names a backend profile (see Provider / WithProviders) whose env
	// overrides and default model apply to invocations resolving to this agent.
	// An effect's `with: { provider: <name> }` arg overrides this per call.
	// Empty means the ambient environment (today's behavior).
	Provider string
	Harness  string

	// TokenBudget overrides the per-verb default pre-dispatch budget-gate
	// thresholds (see budget_gate.go) for this agent. Mirrors the
	// Toolbox/BashProfile declaration pattern: nil means "use the built-in
	// per-verb default"; a non-nil value that fails validation (WarnTokens
	// <= 0, or RefuseTokens < WarnTokens) makes every dispatch through this
	// agent refuse closed rather than silently falling back to the default —
	// an author-declared budget is trusted at face value or not at all.
	TokenBudget *BudgetThresholds
}

// AgentPermissions is the resolved permission posture for one agent contract.
// Mode accepts the kitsoki-facing values ask, bypassPermissions, denyAll plus
// Claude CLI permission modes. DisallowedTools are appended as a hard-deny set.
type AgentPermissions struct {
	Mode            string
	DisallowedTools []string
}

// Provider is a backend profile applied to one agent invocation. Backend
// selects the coding-agent CLI adapter, Env entries are merged onto the process
// environment, and Model/Effort supply defaults unless the call explicitly
// selected this provider. It is the host-side translation of app.ProviderDecl,
// kept here so the host package needs no app import.
type Provider struct {
	Backend string
	Model   string
	// Effort supplies the --effort default for an invocation whose agent (and
	// effect) declare no explicit effort. Empty leaves the agent/CLI default.
	Effort string
	Env    map[string]string
}

// QuotaControl describes a local, provider-neutral throttle for one resolved
// profile. Zero values are ignored; callers may set only concurrency, only a
// token bucket, or both.
type QuotaControl struct {
	Window          string
	TokensPerWindow int64
	MaxConcurrent   int
	ReserveTokens   int64
	StatePath       string
	LeaseTimeout    string
}

// providersKey is the unexported context key for the injected providers map.
type providersKey struct{}

// WithProviders injects the named-provider map into ctx so agent handlers can
// resolve an agent's Provider / an effect's `provider:` arg to a Provider value.
// Passing nil is safe; handlers that see no providers map leave every call on
// the ambient environment.
func WithProviders(ctx context.Context, providers map[string]Provider) context.Context {
	if providers == nil {
		return ctx
	}
	return context.WithValue(ctx, providersKey{}, providers)
}

// ProvidersFromContext returns the providers map previously injected with
// WithProviders, or nil when none was injected.
func ProvidersFromContext(ctx context.Context) map[string]Provider {
	if v, ok := ctx.Value(providersKey{}).(map[string]Provider); ok {
		return v
	}
	return nil
}

// providerEnvKey is the unexported context key carrying the resolved provider's
// env overrides down to the claude exec layer (runClaudeOneShotReal /
// runClaudeStreamJSON).
type providerEnvKey struct{}

// WithAgentProviderEnv returns a child context carrying env as the per-call
// provider environment overrides applied to the claude subprocess. A nil/empty
// map is a no-op so callers needn't guard. The most recent call wins (a nested
// override replaces, not merges).
func WithAgentProviderEnv(ctx context.Context, env map[string]string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	return context.WithValue(ctx, providerEnvKey{}, env)
}

// AgentProviderEnvFromCtx returns the provider env overrides installed by
// WithAgentProviderEnv, or nil when none is installed (ambient environment).
func AgentProviderEnvFromCtx(ctx context.Context) map[string]string {
	if v, ok := ctx.Value(providerEnvKey{}).(map[string]string); ok {
		return v
	}
	return nil
}

// applyProvider resolves the provider for one agent invocation and returns the
// context and agent to use downstream. Selection precedence (principle of least
// surprise, mirroring system_prompt / tools): an effect's `with: { provider }`
// arg wins over the resolved agent's Provider; neither set means the ambient
// environment (the returned ctx/agent are unchanged).
//
// When a provider resolves:
//   - its Env is installed via WithAgentProviderEnv so the claude exec layer
//     merges it onto the subprocess environment, and
//   - the provider's Model becomes the agent's effective model for active
//     harness profiles. Named story providers still only fill blank models.
//
// An unknown provider name (no providers map, or a name absent from it) is a
// no-op here — load-time validation already rejects unknown static references;
// a runtime miss only happens on test scaffolding that skips the app loader,
// where falling back to ambient is the safe behavior.
func applyProvider(ctx context.Context, args map[string]any, agent Agent) (context.Context, Agent) {
	explicitProvider := false
	name, _ := args["harness"].(string)
	if strings.TrimSpace(name) != "" {
		explicitProvider = true
	}
	if strings.TrimSpace(name) == "" {
		name, _ = args["provider"].(string)
	}
	if strings.TrimSpace(name) != "" {
		name = strings.TrimSpace(name)
		explicitProvider = true
	}
	if name == "" {
		name = agent.Harness
	}
	if name == "" {
		name = agent.Provider
	}
	if name == "" {
		// No explicit provider on this call: fall back to the session's active
		// harness profile. This is an operator-selected backend/model for the
		// session, so it supersedes story-local model defaults that may name a
		// provider-specific model invalid for the selected endpoint.
		if prof, ok := ActiveProfileFromContext(ctx); ok {
			if strings.TrimSpace(prof.Provider.Model) != "" {
				agent.Model = prof.Provider.Model
			}
			if strings.TrimSpace(prof.Provider.Effort) != "" {
				agent.Effort = prof.Provider.Effort
			}
			ctx = WithAgentProviderEnv(ctx, prof.Provider.Env)
		}
		return ctx, agent
	}
	providers := ProvidersFromContext(ctx)
	if providers == nil {
		return ctx, agent
	}
	prov, ok := providers[name]
	if !ok {
		return ctx, agent
	}
	if strings.TrimSpace(prov.Backend) != "" {
		ctx = WithAgentBackendNamed(ctx, prov.Backend)
	}
	if (explicitProvider || strings.TrimSpace(agent.Model) == "") && strings.TrimSpace(prov.Model) != "" {
		agent.Model = prov.Model
	}
	if (explicitProvider || strings.TrimSpace(agent.Effort) == "") && strings.TrimSpace(prov.Effort) != "" {
		agent.Effort = prov.Effort
	}
	ctx = WithAgentProviderEnv(ctx, prov.Env)
	return ctx, agent
}

// activeProfileKey carries the session's active harness profile (a Provider plus
// its name) down to applyProvider as the operator-selected session default.
type activeProfileKey struct{}

// ActiveProfile is the resolved harness profile in effect for a session: a
// Provider (env + model + effort) plus the profile Name (recorded in traces).
// It is installed per-dispatch by the orchestrator from the live selection and
// consulted by applyProvider only when an agent call names no explicit
// provider. The profile's model supersedes story-local model defaults so
// provider-specific model names do not leak to the selected endpoint.
type ActiveProfile struct {
	Name     string
	Provider Provider
	Quota    QuotaControl
}

// WithActiveProfile installs the session's active harness profile onto ctx. A
// zero-value profile (no name, empty provider) is a no-op so callers needn't
// guard the no-profiles case.
func WithActiveProfile(ctx context.Context, p ActiveProfile) context.Context {
	if p.Name == "" && p.Provider.Model == "" && p.Provider.Effort == "" && len(p.Provider.Env) == 0 && p.Quota == (QuotaControl{}) {
		return ctx
	}
	return context.WithValue(ctx, activeProfileKey{}, p)
}

// ActiveProfileFromContext returns the active harness profile installed with
// WithActiveProfile, and whether one was installed.
func ActiveProfileFromContext(ctx context.Context) (ActiveProfile, bool) {
	p, ok := ctx.Value(activeProfileKey{}).(ActiveProfile)
	return p, ok
}

// ActiveProfileNameFromCtx returns just the active profile's name (for trace
// stamping), or "" when none is installed.
func ActiveProfileNameFromCtx(ctx context.Context) string {
	if p, ok := ActiveProfileFromContext(ctx); ok {
		return p.Name
	}
	return ""
}

// agentsKey is the unexported context key for the injected agents map.
type agentsKey struct{}

// WithAgents injects the agents map into ctx so host.agent.* handlers can
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
	var base Agent
	ok := false
	if name != "" {
		agents := AgentsFromContext(ctx)
		if agents != nil {
			base, ok = agents[name]
		}
	}
	if contract, hasContract := agentContractArg(args); hasContract {
		return mergeAgentContract(base, contract), true
	}
	return base, ok
}

func agentContractArg(args map[string]any) (map[string]any, bool) {
	raw, ok := args["agent_contract"]
	if !ok || raw == nil {
		return nil, false
	}
	m, ok := raw.(map[string]any)
	return m, ok
}

func mergeAgentContract(base Agent, c map[string]any) Agent {
	if s, _ := c["system_prompt"].(string); s != "" {
		base.SystemPrompt = s
	}
	if s, _ := c["model"].(string); s != "" {
		base.Model = s
	}
	if s, _ := c["effort"].(string); s != "" {
		base.Effort = s
	}
	if s, _ := c["cwd"].(string); s != "" {
		base.DefaultCwd = s
	}
	if s, _ := c["provider"].(string); s != "" {
		base.Provider = s
	}
	if s, _ := c["harness"].(string); s != "" {
		base.Harness = s
	}
	if tools := stringSliceArg(c, "tools"); len(tools) > 0 {
		base.Tools = tools
	}
	if s, _ := c["toolbox"].(string); s != "" {
		base.Toolbox = s
	}
	if mcp, ok := c["mcp"].(map[string]any); ok {
		if servers, ok := mcp["servers"].(map[string]any); ok {
			base.MCPServers = cloneAnyMap(servers)
		}
		if tools := stringSliceArg(mcp, "tools"); len(tools) > 0 {
			base.MCPTools = tools
		}
	}
	if perms, ok := c["permissions"].(map[string]any); ok {
		if s, _ := perms["mode"].(string); s != "" {
			base.Permissions.Mode = s
		}
		if tools := stringSliceArg(perms, "disallowed_tools"); len(tools) > 0 {
			base.Permissions.DisallowedTools = tools
		}
	}
	if v, ok := c["external_side_effect"].(bool); ok {
		base.ExternalSideEffect = &v
	}
	return base
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

// effectiveEffort resolves the final --effort level for a handler call. An
// inline `effort:` arg in the effect's with: block WINS over the resolved
// agent's Effort (mirroring effectiveSystemPrompt) so authors can dial one
// call up or down without rewriting the agents block. Returns "" when neither
// source is set (no --effort flag added; claude uses its own default).
func effectiveEffort(args map[string]any, agent Agent) string {
	if inline, _ := args["effort"].(string); inline != "" {
		return inline
	}
	return agent.Effort
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
		return appendMCPTools(perCall, args, agent)
	}
	if len(perCall) > 0 {
		return appendMCPTools(perCall, args, agent)
	}
	if len(agent.Tools) > 0 {
		return appendMCPTools(agent.Tools, args, agent)
	}
	return appendMCPTools(nil, args, agent)
}

func appendMCPTools(tools []string, args map[string]any, agent Agent) []string {
	out := append([]string(nil), tools...)
	out = append(out, agent.MCPTools...)
	if mcp, ok := args["mcp"].(map[string]any); ok {
		out = append(out, stringSliceArg(mcp, "tools")...)
	}
	return dedupeStrings(out)
}

func effectiveMCPServers(args map[string]any, agent Agent) map[string]any {
	out := cloneAnyMap(agent.MCPServers)
	mergeServers := func(raw any) {
		m, ok := raw.(map[string]any)
		if !ok {
			return
		}
		for k, v := range m {
			out[k] = v
		}
	}
	if mcp, ok := args["mcp"].(map[string]any); ok {
		mergeServers(mcp["servers"])
	}
	mergeServers(args["mcp_servers"])
	if len(out) == 0 {
		return nil
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func dedupeStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// appendAllowedToolsFlag appends --allowedTools <csv> to cliArgs when tools is
// non-empty. The CSV format is what the claude CLI expects.
func appendAllowedToolsFlag(cliArgs []string, tools []string) []string {
	if len(tools) == 0 {
		return cliArgs
	}
	return append(cliArgs, "--allowedTools", strings.Join(tools, ","))
}

// appendDisallowedToolsFlag appends --disallowedTools <csv> to cliArgs when
// tools is non-empty. Unlike --allowedTools (which only auto-approves under an
// enforcing permission mode), --disallowedTools is a HARD deny that the CLI
// honours under *every* permission mode — including bypassPermissions — so it
// is the reliable backstop for a read-only agent.
func appendDisallowedToolsFlag(cliArgs []string, tools []string) []string {
	if len(tools) == 0 {
		return cliArgs
	}
	return append(cliArgs, "--disallowedTools", strings.Join(tools, ","))
}

func setPermissionMode(cliArgs []string, mode string) []string {
	if strings.TrimSpace(mode) == "" {
		return cliArgs
	}
	out := append([]string(nil), cliArgs...)
	for i := 0; i < len(out)-1; i++ {
		if out[i] == "--permission-mode" {
			out[i+1] = mode
			return out
		}
	}
	return append(out, "--permission-mode", mode)
}

func effectivePermissionMode(args map[string]any, agent Agent, fallback string) string {
	mode, _ := args["permission_mode"].(string)
	if mode == "" {
		if p, ok := args["permissions"].(map[string]any); ok {
			mode, _ = p["mode"].(string)
		}
	}
	if mode == "" {
		mode = agent.Permissions.Mode
	}
	if mode == "" {
		mode = fallback
	}
	switch mode {
	case "ask":
		return "default"
	case "denyAll":
		return "default"
	default:
		return mode
	}
}

func effectiveDisallowedTools(args map[string]any, agent Agent) []string {
	var out []string
	out = append(out, agent.Permissions.DisallowedTools...)
	if p, ok := args["permissions"].(map[string]any); ok {
		out = append(out, stringSliceArg(p, "disallowed_tools")...)
		if mode, _ := p["mode"].(string); mode == "denyAll" {
			out = append(out, readOnlyDeniedTools...)
		}
	}
	if mode, _ := args["permission_mode"].(string); mode == "denyAll" {
		out = append(out, readOnlyDeniedTools...)
	}
	if agent.Permissions.Mode == "denyAll" {
		out = append(out, readOnlyDeniedTools...)
	}
	return dedupeStrings(out)
}

// readOnlyDeniedTools are the repo-mutating / arbitrary-exec tools a converse
// agent that declares external_side_effect:false must never run. Bash is in
// the set because it is arbitrary code execution — a "read-only" agent with
// Bash can still write files via `echo >`, python, sed, … (the leak the
// task-fs-sandbox proposal calls out). WebFetch/WebSearch are deliberately NOT
// denied: they read external state, which a read-only agent may legitimately
// do.
var readOnlyDeniedTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "Bash"}

// readOnlyAgentVerbDeniedTools are denied for ask/decide's read-only posture.
// Unlike converse, ask/decide may allow Bash when the agent declares a
// BashProfile; the Bash MCP wrapper applies the profile before any shell runs.
var readOnlyAgentVerbDeniedTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit"}

// fileMutationTools are the built-in durable file mutators. They are not the
// ask/decide enforcement path; enforceToolbox owns that. The task durability
// barrier and the extract LLM tier still need this small classifier.
var fileMutationTools = map[string]bool{
	"Edit":         true,
	"Write":        true,
	"MultiEdit":    true,
	"NotebookEdit": true,
}

// alwaysDeniedTools are tools denied on EVERY agent subprocess regardless of
// agent posture or permission mode.
//
// AskUserQuestion is the headless landmine: when a dispatched `claude -p` agent
// calls it, there is no interactive TTY to answer, so the CLI auto-resolves the
// tool immediately with EMPTY answers (~37ms; upstream
// anthropics/claude-code#50728). The model "hears" a blank answer and proceeds
// on a guess — the silent wrong-output failure operators kept hitting. kitsoki's
// supported channel for "ask the human" is the story's own ask/converse verbs
// surfaced to the TUI/web operator, never the embedded AskUserQuestion tool, so
// we hard-deny it everywhere.
//
// Agent/Task are also denied everywhere. Kitsoki stories declare the exact tool
// surface per agent; nested Claude Code subagents are outside that contract, can
// inherit a provider-default model instead of the selected harness profile, and
// multiply quota/concurrency behind Kitsoki's limiter. A story that wants fan-out
// should use Kitsoki's own pipeline stories (fleet, punch-list, dogfood-marathon)
// where traces, profiles, and quota are explicit.
//
// --disallowedTools is honoured even under bypassPermissions (see
// appendDisallowedToolsFlag), so this is a reliable backstop.
var alwaysDeniedTools = []string{"AskUserQuestion", "Agent", "Task"}

// withAlwaysDenied merges alwaysDeniedTools into an agent/posture-specific deny
// list, de-duplicating so a tool already present (e.g. via readOnlyDeniedTools)
// is not emitted twice.
func withAlwaysDenied(disallowed []string) []string {
	seen := make(map[string]bool, len(disallowed))
	out := make([]string, 0, len(disallowed)+len(alwaysDeniedTools))
	for _, t := range disallowed {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for _, t := range alwaysDeniedTools {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// resolveAgentEffect returns a's classified effect for enforcement decisions
// that only see the Agent value itself (no separately-resolved call-site tool
// list — contrast inferReplayMode in agent_task_replay.go, which does get one
// and uses effect.FromTools directly on it). Precedence:
//
//  1. a.Effect, when the loader populated it (every agent loaded through
//     internal/app's resolveAgentEffect always sets this — see the Effect
//     field doc on the Agent struct above).
//  2. The deprecated a.ExternalSideEffect, mapped through
//     effect.FromLegacyBool against a.Tools.
//  3. effect.Write — the safe "no classification info at all" default this
//     package used before the taxonomy existed (a hand-constructed Agent in
//     a unit test, or legacy test scaffolding that bypasses the loader
//     entirely). This deliberately does NOT fall back to effect.FromTools:
//     an agent with no tools declared is not necessarily an agent with no
//     capability (host.agent.task, for one, runs unrestricted regardless of
//     Tools), so "unknown" defaults to write-capable, not to the taxonomy's
//     formal (and here misleading) tool-less-surface-is-Pure join.
func resolveAgentEffect(a Agent) effect.Effect {
	if a.Effect != "" && a.Effect.Valid() {
		return a.Effect
	}
	if a.ExternalSideEffect != nil {
		return effect.FromLegacyBool(*a.ExternalSideEffect, a.Tools)
	}
	return effect.Write
}

// agentIsReadOnly reports whether an agent's resolved effect class is
// read-only (pure or read). Unclassified agents default to write-capable (see
// resolveAgentEffect) so the posture only tightens for agents that are
// actually read/pure.
func agentIsReadOnly(a Agent) bool {
	return resolveAgentEffect(a).LessEqual(effect.Read)
}

type ToolboxEnforcement struct {
	CLIMode      string
	AllowedTools []string
	DeniedTools  []string
	Toolbox      string
	Effect       effect.Effect
}

type ToolboxEnforcementOptions struct {
	// EffectCeiling, when set, caps the enforced effect class for verbs whose
	// contract is narrower than the agent's full capability. ask/decide use
	// this to stay read-only even if legacy per-call tools try to widen them.
	EffectCeiling effect.Effect
	// ReadOnlyDeniedTools overrides the default read-only deny set. ask/decide
	// use the Bash-profile-aware set; converse uses the stricter default.
	ReadOnlyDeniedTools []string
}

func enforceToolbox(ctx context.Context, args map[string]any, agent Agent, fallbackMode string, opts ...ToolboxEnforcementOptions) ToolboxEnforcement {
	var opt ToolboxEnforcementOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	allowed := effectiveTools(ctx, args, agent)
	class := effect.Join(resolveAgentEffect(agent), effect.FromTools(allowed))
	if opt.EffectCeiling.Valid() && !class.LessEqual(opt.EffectCeiling) {
		class = opt.EffectCeiling
	}
	mode := effectivePermissionMode(args, agent, fallbackMode)
	var denied []string
	if class.LessEqual(effect.Read) {
		readOnlyDeny := readOnlyDeniedTools
		if opt.ReadOnlyDeniedTools != nil {
			readOnlyDeny = opt.ReadOnlyDeniedTools
		}
		mode = "default"
		denied = withAlwaysDenied(readOnlyDeny)
	} else {
		denied = withAlwaysDenied(effectiveDisallowedTools(args, agent))
	}
	return ToolboxEnforcement{
		CLIMode:      mode,
		AllowedTools: allowed,
		DeniedTools:  denied,
		Toolbox:      agent.Toolbox,
		Effect:       class,
	}
}

// noToolsDispatchContract is appended to the persona of a dispatch whose
// resolved toolset is EMPTY. On the claude backend the empty allowlist is
// mechanically enforced; on backends whose intrinsic tools cannot be removed
// (codex must run with the approvals/sandbox bypass so the validator submit
// MCP tool can execute, and its shell survives that), this contract is the
// enforcement. Live-proven cost: a tools:[] judge on codex re-ran the full
// test suite its validator had just run, tripling the call's marginal tokens
// (see .context/llm-usage-audit-bugfix-qs1.md).
const noToolsDispatchContract = "TOOLING CONTRACT: this dispatch grants you NO workspace tools. " +
	"Do not run shell commands, read or write files, or explore the repository — " +
	"any workspace tool use violates your toolbox declaration and wastes the call. " +
	"Everything you need is already in the prompt; act on the provided material " +
	"alone and submit your structured answer directly (the submit/question tools " +
	"provided to you are the only permitted tool calls)."

// applyNoToolsContract returns the agent with the no-tools contract folded
// into its persona when the enforced toolset is empty. Static text appended
// to the persona (task) layer, so prompt-prefix stability is preserved.
func applyNoToolsContract(agent Agent, policy ToolboxEnforcement) Agent {
	if len(policy.AllowedTools) != 0 {
		return agent
	}
	if agent.SystemPrompt != "" {
		agent.SystemPrompt += "\n\n"
	}
	agent.SystemPrompt += noToolsDispatchContract
	return agent
}

func (p ToolboxEnforcement) WithAllowed(tools []string) ToolboxEnforcement {
	p.AllowedTools = dedupeStrings(tools)
	return p
}

func (p ToolboxEnforcement) AgentCalledFields(payload AgentCalledPayload) AgentCalledPayload {
	payload.Toolbox = p.Toolbox
	payload.AllowedTools = append([]string(nil), p.AllowedTools...)
	payload.DeniedTools = append([]string(nil), p.DeniedTools...)
	if p.Effect != "" {
		payload.Effect = string(p.Effect)
	}
	return payload
}

// converseToolPolicy computes the CLI permission posture for a converse call:
// the --permission-mode value the `claude` binary actually receives and the
// --disallowedTools backstop. It does two jobs.
//
// (1) Translate kitsoki's permission_mode vocabulary into a value the CLI
// accepts. The CLI's --permission-mode choices are
// acceptEdits|auto|bypassPermissions|default|dontAsk|plan; "ask" and "denyAll"
// are kitsoki-facing names, NOT CLI flags, so forwarding them verbatim makes
// claude exit with an "invalid choice" error. They map as:
//   - bypassPermissions → bypassPermissions (the documented default; no
//     allowlist enforcement)
//   - ask               → default (the allowlist binds; tools outside it are
//     not auto-approved — a headless `-p` run has no interactive confirm loop,
//     so an unapproved mutation is denied rather than prompted)
//   - denyAll           → default + the readOnlyDeniedTools deny-set
//
// (2) Tighten for a read-only agent (external_side_effect:false) regardless of
// the requested mode: downgrade bypassPermissions to "default" so the
// --allowedTools allowlist is actually honoured (under bypassPermissions the
// CLI approves EVERY tool, making the allowlist advisory — how the
// proposal_interviewer, declared tools:[Read,Grep,Glob], was able to Write a
// proposal file mid-discovery), and carry readOnlyDeniedTools as a hard
// backstop.
//
// A write-capable agent (external_side_effect unset or true) gets only the
// vocabulary translation.
func converseToolPolicy(permMode string, agent Agent) (cliMode string, disallowed []string) {
	policy := enforceToolbox(context.Background(), map[string]any{"permission_mode": permMode}, agent, permMode)
	return policy.CLIMode, policy.DeniedTools
}

// agentSettingSources is the --setting-sources value applied to every agent
// subagent invocation. It deliberately OMITS the "user" source so a story's
// agents never inherit the operator's user-global Claude Code configuration —
// enabledPlugins, custom agents, and skills installed under ~/.claude.
//
// Without this isolation, the exec'd `claude` CLI loads ~/.claude/settings.json
// by default, and any globally-enabled plugin can hijack a story's agent. The
// observed failure: with BMAD-METHOD enabled (enabledPlugins in user settings),
// the prd story's `interviewer` agent stopped following its --append-system-prompt
// and instead role-played BMAD's "John" PM persona — announcing a deprecation
// notice, picking its own output path, and presenting its own pick-one menu.
//
// Dropping "user" keeps "project" and "local" so the working_dir's own .claude
// config still applies, and leaves auth untouched (OAuth/credentials are read
// from the keychain, not from a setting source). A story's agents are therefore
// defined by its own --append-system-prompt / --model / --allowedTools flags.
const agentSettingSources = "project,local"

// appendSettingSourcesFlag pins --setting-sources to the hermetic source set so
// agent subagents are isolated from the operator's user-global plugins/skills.
// Applied at every claude-CLI construction site (ask/decide/task via
// buildBaseCLIArgs, both converse paths, and ask_structured).
func appendSettingSourcesFlag(cliArgs []string) []string {
	return append(cliArgs, "--setting-sources", agentSettingSources)
}

// appendDisableSlashCommandsFlag disables Claude Code slash commands, which is
// also the CLI's documented switch for disabling skills. Story-dispatched agents
// must follow the story's deterministic prompt and tool surface, not stop to
// discover or invoke project/user skills.
func appendDisableSlashCommandsFlag(cliArgs []string) []string {
	return append(cliArgs, "--disable-slash-commands")
}

// appendStrictMCPConfigFlag pins --strict-mcp-config so the agent subprocess uses
// ONLY the MCP servers kitsoki attaches via --mcp-config (the structured-output
// `submit` validator and, when present, the operator-ask bridge) and IGNORES every
// other MCP source — crucially the working_dir's project .mcp.json.
//
// Without it, a maker whose working_dir is a `.worktrees/<branch>` checkout of a
// repo that ships a tracked `.mcp.json` (this one does) loads that project MCP
// server alongside the validator. When that project server is failing/contended,
// claude's documented project-vs---mcp-config interference (anthropics/claude-code
// #4938, #17299) silently DROPS the validator from the live tool set, so the maker
// reports `No such tool available: submit`, burns all acceptance attempts, and the
// (correct) work is discarded. The interference init-races, so it bit long maker
// runs but not short ones. Strict mode removes the variable entirely.
//
// Applied at the same hermetic sites as appendSettingSourcesFlag; it is additive to
// argv and orthogonal to the --mcp-config entries (it only suppresses NON-flag MCP
// sources), so the validator + operator-ask servers kitsoki passes survive.
func appendStrictMCPConfigFlag(cliArgs []string) []string {
	return append(cliArgs, "--strict-mcp-config")
}

// appendDefaultCwd returns workingDir if non-empty, otherwise returns
// agent.DefaultCwd. Implements the per-call working_dir wins rule.
func appendDefaultCwd(workingDir string, agent Agent) string {
	if workingDir != "" {
		return workingDir
	}
	return agent.DefaultCwd
}
