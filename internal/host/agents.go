// Package host — Agent context shim and per-call agent resolution for
// host.agent.{ask,talk,ask_with_mcp}.
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

// Agent is the per-call configuration applied when a host.agent.* invocation
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
// host.agent.ask or host.agent.decide (enforced at loader time). Nil means
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
	SystemPrompt string
	Model        string
	// Effort, when non-empty, is forwarded to `claude --effort`
	// (low|medium|high|xhigh|max). An effect's `with: { effort }` arg overrides
	// it per call; empty leaves the CLI default.
	Effort             string
	Tools              []string
	BashProfile        *BashProfile
	DefaultCwd         string
	ExternalSideEffect *bool
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
}

// Provider is a backend profile applied to the `claude` subprocess for an
// agent invocation: Env entries are merged onto the process environment
// (overriding ambient values of the same key) and Model, when non-empty,
// supplies the --model default for an invocation whose agent declares no
// explicit model. It is the host-side translation of app.ProviderDecl, kept
// here so the host package needs no app import.
type Provider struct {
	Model string
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
	name, _ := args["provider"].(string)
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
	if strings.TrimSpace(agent.Model) == "" && strings.TrimSpace(prov.Model) != "" {
		agent.Model = prov.Model
	}
	if strings.TrimSpace(agent.Effort) == "" && strings.TrimSpace(prov.Effort) != "" {
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

// readOnlyDeniedTools are the repo-mutating / arbitrary-exec tools a converse
// agent that declares external_side_effect:false must never run. Bash is in
// the set because it is arbitrary code execution — a "read-only" agent with
// Bash can still write files via `echo >`, python, sed, … (the leak the
// task-fs-sandbox proposal calls out). WebFetch/WebSearch are deliberately NOT
// denied: they read external state, which a read-only agent may legitimately
// do.
var readOnlyDeniedTools = []string{"Write", "Edit", "MultiEdit", "NotebookEdit", "Bash"}

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

// agentIsReadOnly reports whether an agent has explicitly declared
// external_side_effect: false. Unset (nil) is treated as write-capable so the
// posture only tightens for agents that opted into read-only.
func agentIsReadOnly(a Agent) bool {
	return a.ExternalSideEffect != nil && !*a.ExternalSideEffect
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
	switch permMode {
	case "denyAll":
		cliMode, disallowed = "default", readOnlyDeniedTools
	case "ask":
		cliMode = "default"
	default: // bypassPermissions
		cliMode = permMode
	}

	if agentIsReadOnly(agent) {
		if cliMode == "bypassPermissions" {
			cliMode = "default"
		}
		disallowed = readOnlyDeniedTools
	}
	// AskUserQuestion is denied on every converse turn too — same headless
	// auto-resolve hazard (see alwaysDeniedTools). Merge so we never emit a
	// duplicate --disallowedTools entry alongside readOnlyDeniedTools.
	disallowed = withAlwaysDenied(disallowed)
	return cliMode, disallowed
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
