// Package app holds the loaded, validated application definition.
// Types here map directly onto the YAML authoring format (§3) and carry
// yaml struct tags for deserialization via goccy/go-yaml.
package app

// StatePath is a slash-separated path identifying a state in the graph,
// e.g. "bar/dark" for a nested compound state.
type StatePath string

// SessionID uniquely identifies a runtime session.
type SessionID string

// TurnNumber is the monotonic turn counter within a session.
type TurnNumber int64

// ---- App-definition types (loaded from YAML) --------------------------------

// AppDef is the top-level deserialized application definition.
type AppDef struct {
	App     AppMeta           `yaml:"app"`
	World   map[string]VarDef `yaml:"world,omitempty"`
	Intents map[string]Intent `yaml:"intents,omitempty"`
	Root    any               `yaml:"root"` // string state name or inline compound/parallel root
	States  map[string]*State `yaml:"states,omitempty"`
	OffPath *OffPathDef       `yaml:"off_path,omitempty"`
	// Hosts is the allow-list of host handler names this app may invoke (§2).
	Hosts []string `yaml:"hosts,omitempty"`
	// Proposals declares named proposal kinds (§3).
	Proposals map[string]*ProposalKind `yaml:"proposals,omitempty"`
	// Include lists glob patterns for additional YAML files to merge (§9).
	Include []string `yaml:"include,omitempty"`

	// PhaseTemplates declares reusable phase shapes (proposal §5.2).
	// Authors instantiate templates by listing phases under `phases:`.
	PhaseTemplates map[string]*PhaseTemplate `yaml:"phase_templates,omitempty"`

	// Agents declares named Claude agents — first-class personas / system
	// prompts (and optionally model overrides) reusable across any
	// host.oracle.{ask,talk,ask_with_mcp} call via the `agent: <name>` arg
	// in the effect's `with:` block. Generalises OffPathDef.Persona into a
	// per-call primitive so different rooms / intents can speak in
	// different voices through the same engine path.
	// Phases declares the phase graph that instantiates a phase template
	// (proposal §5.3). Expanded into States at load time.
	Phases *PhasesBlock `yaml:"phases,omitempty"`
	// CheckpointIntents is a per-room intent menu merged into every
	// {id}_awaiting_reply state during phase-template expansion (proposal §6).
	CheckpointIntents map[string]Intent `yaml:"checkpoint_intents,omitempty"`
	// MetaModes declares named off-path concerns (meta-mode proposal §2).
	MetaModes map[string]*MetaModeDef `yaml:"meta_modes,omitempty"`
	// Agents declares named per-context agents (meta-mode proposal §2.1).
	// Generalises OffPathDef.Persona / OffPathDef.Agent into a top-level
	// primitive any host.oracle.* call site can reference by name. Bound
	// at startup via agents.BuildRegistry(def.AgentSpecs()) + host.SetAgentRegistry.
	Agents map[string]*AgentDecl `yaml:"agents,omitempty"`

	// Imports declares aliased composition with private worlds
	// (see docs/imports.md §3). Each import binds a child app
	// under a string alias; child states/world keys are namespaced
	// under that alias at load time.
	Imports map[string]*ImportDef `yaml:"imports,omitempty"`

	// Exits declares the child-side exit contract — named return
	// points the parent maps to its own states via imports.<alias>.exits.
	// Standalone apps may declare exits for documentation but they have
	// no runtime effect outside an import context.
	Exits map[string]*ExitDef `yaml:"exits,omitempty"`

	// Exports declares what the child app surfaces to importers.
	// Currently only intents (see docs/imports.md §6).
	Exports *ExportsBlock `yaml:"exports,omitempty"`

	// HostInterfaces declares named capabilities the app depends on
	// (see docs/imports.md §11). Importers rebind via
	// imports.<alias>.host_bindings.
	HostInterfaces map[string]*HostInterfaceDef `yaml:"host_interfaces,omitempty"`

	// ImportWrappers records one entry per immediate import the loader
	// folded into this AppDef. Populated by resolveImports; never set
	// by YAML authors. Used by:
	//   - validateDef to reject parent transitions targeting a deep
	//     state inside an imported child (proposal §8 / §16.7);
	//   - the metamode controller's file-watch tree to include every
	//     imported manifest directory (proposal §16.4);
	//   - the trace logger / future tooling to label states by the
	//     import alias chain they live under (proposal §16.5).
	ImportWrappers map[string]*ImportWrapperInfo `yaml:"-"`

	// LoadedManifests is the set of absolute paths the loader read
	// during this AppDef's recursive load: the root manifest plus
	// every imported child's manifest at every depth. Used by the
	// metamode controller's auto-watch so edits to a sibling
	// imported story trigger reload.
	LoadedManifests []string `yaml:"-"`
}

// ImportWrapperInfo carries the post-fold metadata for one import
// alias. Populated by resolveImports.
type ImportWrapperInfo struct {
	// Alias is the prefix the importer chose (`bf`, `frontier`, …).
	// Mirrors the map key but kept here so callers iterating values
	// don't lose the key.
	Alias string
	// Entry is the child state the import was declared to start in
	// (the import's `entry:` field). Used by §16.7 to allow
	// `<alias>.<entry>` from the parent while rejecting deeper paths.
	Entry string
	// SourcePath is the absolute path to the child manifest.
	// Used by §16.4 for auto-watch and for diagnostic messages.
	SourcePath string
}

// ImportDef declares one entry in an AppDef's `imports:` block
// (see docs/imports.md §3).
type ImportDef struct {
	// Source resolves to a child app.yaml. Forms:
	//   - "./relative/path"          — relative to importer's dir
	//   - "/absolute/path"           — absolute path (test escape hatch)
	//   - "@kitsoki/<name>"          — under <repo-root>/stories/<name>
	Source string `yaml:"source"`
	// Version is optional metadata; v1 parses and stores it for
	// traceability only — no semver resolution or compatibility check
	// happens at load time. Reserved for a future registry / lockfile
	// surface (proposal §4 and §16.2).
	Version string `yaml:"version,omitempty"`
	// Entry is the child's initial state when this import is invoked.
	// Path is in the *child's* namespace (not alias-prefixed). Required
	// unless the child declares a Root the parent accepts as entry.
	Entry string `yaml:"entry,omitempty"`
	// Exits maps the child's declared exit names to parent-side targets
	// with optional projection effects (world_out per-exit).
	Exits map[string]*ImportExit `yaml:"exits,omitempty"`
	// WorldIn maps child world keys → parent expressions evaluated at
	// entry. The LHS names child world keys; the RHS is parent-scope
	// expr that resolves to the value pushed into the child's world.
	WorldIn map[string]string `yaml:"world_in,omitempty"`
	// Hosts controls how the child's host allow-list composes with the
	// parent's. "inherit" (default) unions silently; "declared" requires
	// the parent to list every child host explicitly.
	Hosts string `yaml:"hosts,omitempty"`
	// Intents declares parent↔child intent re-exports (§6).
	Intents *ImportIntents `yaml:"intents,omitempty"`
	// Overrides patches child states/intents/prompts at import time (§10).
	Overrides *ImportOverrides `yaml:"overrides,omitempty"`
	// HostBindings rebinds named child host_interfaces onto concrete
	// handler names (§11).
	HostBindings map[string]string `yaml:"host_bindings,omitempty"`
}

// ImportExit declares how a child exit maps to a parent state.
type ImportExit struct {
	// To is the parent-side state to transition into when the child
	// exits via this name.
	To string `yaml:"to"`
	// Set is an optional projection — child-scope expressions evaluated
	// at exit and written to parent world keys.
	Set map[string]any `yaml:"set,omitempty"`
}

// ImportIntents declares per-import intent re-exports (§6).
type ImportIntents struct {
	// Export lists parent intent names made visible inside the child
	// under the same name.
	Export []string `yaml:"export,omitempty"`
	// Import lists child intent names lifted into the parent (rare).
	// The child must list these in its own exports.intents.
	Import []string `yaml:"import,omitempty"`
}

// ImportOverrides patches a child app's states / intents / prompts (§10).
type ImportOverrides struct {
	// States replaces named child states whole-cloth. Each key must
	// match an existing child state name; load fails otherwise.
	States map[string]*State `yaml:"states,omitempty"`
	// Intents replaces named child intent definitions (slots, examples).
	Intents map[string]Intent `yaml:"intents,omitempty"`
	// Prompts maps child-relative prompt paths → parent-relative paths.
	// At load time the parent's file replaces the child's bytes.
	Prompts map[string]string `yaml:"prompts,omitempty"`
}

// ExitDef declares one named exit the child app surfaces (§7).
type ExitDef struct {
	Description string `yaml:"description,omitempty"`
	// Requires lists child world keys that must be set when this exit
	// fires. Best-effort static check; runtime guard backs it up.
	Requires []string `yaml:"requires,omitempty"`
}

// ExportsBlock declares what an app surfaces to importers (§6).
type ExportsBlock struct {
	Intents []string `yaml:"intents,omitempty"`
}

// HostInterfaceDef declares one named capability surface (§11.1).
type HostInterfaceDef struct {
	Description string                      `yaml:"description,omitempty"`
	Operations  map[string]*HostInterfaceOp `yaml:"operations,omitempty"`
	// Default is the handler name bound when no importer overrides.
	Default string `yaml:"default,omitempty"`
}

// HostInterfaceOp declares one operation in a host interface (§11.1).
type HostInterfaceOp struct {
	Input  map[string]any `yaml:"input,omitempty"`
	Output map[string]any `yaml:"output,omitempty"`
}

// PhaseTemplate is a reusable phase shape. It declares a parameter schema and
// a set of states that get instantiated once per phase. State-key keys may
// contain `{paramname}` placeholders; state body strings may contain
// `{{ tpl.paramname }}` and `{{ phase.next.<arc> }}` expressions which the
// loader substitutes at expansion time.
type PhaseTemplate struct {
	Parameters map[string]PhaseTemplateParam `yaml:"parameters,omitempty"`
	States     map[string]*State             `yaml:"states,omitempty"`
}

// PhaseTemplateParam declares one parameter on a phase template.
type PhaseTemplateParam struct {
	Type     string `yaml:"type"`
	Required bool   `yaml:"required,omitempty"`
	Default  any    `yaml:"default,omitempty"`
}

// PhasesBlock is the top-level `phases:` declaration that picks a template
// and supplies a graph of phase instances. Graph is parsed as a raw map so
// authors can put arbitrary template-parameter keys alongside the
// structured `next:`, `cycle_budgets:`, and `checkpoint:` fields.
type PhasesBlock struct {
	Template string                    `yaml:"template"`
	Graph    map[string]map[string]any `yaml:"graph,omitempty"`
}

// AppMeta holds the app-level metadata block.
type AppMeta struct {
	ID      string `yaml:"id"`
	Version string `yaml:"version"`
	Title   string `yaml:"title,omitempty"`
	Author  string `yaml:"author,omitempty"`
	License string `yaml:"license,omitempty"`
}

// VarDef describes one world variable in the schema.
type VarDef struct {
	Type    string   `yaml:"type"`
	Default any      `yaml:"default,omitempty"`
	Values  []string `yaml:"values,omitempty"` // for enum types
}

// WorldSchema is the compiled schema of all world variables.
type WorldSchema map[string]VarDef

// State is a node in the directed graph. It may be atomic, compound, or parallel.
type State struct {
	// Type is "atomic" (default), "compound", or "parallel".
	Type string `yaml:"type,omitempty"`
	// Mode declares a special harness mode for this state.
	// "conversational" enables the Oracle Room free-form harness (§7).
	Mode string `yaml:"mode,omitempty"`
	// Description is shown in the §7.1 location indicator.
	Description string `yaml:"description,omitempty"`
	// View is the render template shown to the user on arrival.
	View string `yaml:"view,omitempty"`
	// Terminal marks end states.
	Terminal bool `yaml:"terminal,omitempty"`
	// Initial is the initial child state for compound states; supports expr interpolation.
	Initial string `yaml:"initial,omitempty"`
	// States holds nested child states (compound/parallel).
	States map[string]*State `yaml:"states,omitempty"`
	// On maps intent names to ordered transition lists.
	On map[string][]Transition `yaml:"on,omitempty"`
	// OnEnter holds effects/invocations fired on state entry.
	OnEnter []Effect `yaml:"on_enter,omitempty"`
	// Intents holds locally-scoped intent definitions (§3.4).
	Intents map[string]Intent `yaml:"intents,omitempty"`
	// Menu is an explicit list of allowed intent names overriding the default.
	Menu []string `yaml:"menu,omitempty"`
	// RelevantWorld pins world keys shown in the §7.1 location indicator.
	RelevantWorld []string `yaml:"relevant_world,omitempty"`
	// RelevantSlots pins slot names shown in the §7.1 location indicator.
	RelevantSlots []string `yaml:"relevant_slots,omitempty"`
	// Timeout declares an automatic transition after a duration.
	Timeout *TimeoutDef `yaml:"timeout,omitempty"`
}

// Transition is one entry in a state's on[intent] list.
type Transition struct {
	// Target is the destination state path. "." means self.
	Target string `yaml:"target"`
	// When is the guard expression (expr-lang). Empty = always true.
	When string `yaml:"when,omitempty"`
	// Default marks this as the catch-all branch when no prior guard matched.
	Default bool `yaml:"default,omitempty"`
	// Effects is the ordered list of world mutations applied when this transition fires.
	Effects []Effect `yaml:"effects,omitempty"`
	// GuardHint is a human-readable hint shown when the guard fails (§5.2, §7.5).
	GuardHint string `yaml:"guard_hint,omitempty"`
	// View is the transition-scoped narrative shown on this transition (§7.6).
	// When present it wins over the target state's view for this turn.
	View string `yaml:"view,omitempty"`
	// Emit lists events emitted to parallel regions after this transition.
	Emit []string `yaml:"emit,omitempty"`
	// PushHistory controls whether the outgoing state is pushed onto the history stack.
	// Default true (push on every normal transition). Set false for utility transitions
	// like entering the Oracle Room or Inbox (§5.1 stackless transitions).
	PushHistory *bool `yaml:"push_history,omitempty"`
}

// Effect is one atomic world mutation or side-effect invocation.
type Effect struct {
	// When is an optional guard expression (expr-lang). When non-empty
	// and the expression evaluates false against the current world, the
	// effect is skipped silently (no set/invoke/say/etc. runs). Empty =
	// always-on. Used to branch on_enter / transition effects on world
	// flags (e.g. `when: world.narration` vs `when: not world.narration`
	// per proposal §6.2.1 / §9.6). Eval errors are non-fatal: a bad
	// expression aborts the surrounding effects chain with an error so
	// authors get a loud failure rather than a silently-skipped effect.
	When string `yaml:"when,omitempty"`
	// Set maps world-variable names to new values (expr or literal).
	Set map[string]any `yaml:"set,omitempty"`
	// Increment maps world-variable names to integer delta values.
	Increment map[string]int `yaml:"increment,omitempty"`
	// Say appends a narrative message (expr interpolation supported).
	Say string `yaml:"say,omitempty"`
	// Invoke calls a host-namespace function (§2, §11).
	Invoke string `yaml:"invoke,omitempty"`
	// With holds arguments for an Invoke call.
	With map[string]any `yaml:"with,omitempty"`
	// Bind extracts keys from the host result into world variables: bind: {world_key: result_key}.
	Bind map[string]string `yaml:"bind,omitempty"`
	// OnError is a state transition target fired when a host invoke returns an error.
	// The $host_error slot is populated with {code, message} for guard evaluation.
	OnError string `yaml:"on_error,omitempty"`
	// Emit sends a named event to parallel regions.
	Emit string `yaml:"emit,omitempty"`
	// Background, when true, dispatches Invoke as a job and binds job_id
	// (or default last_job_id) instead of running synchronously. Requires
	// Invoke to be non-empty; validated at load time.
	Background bool `yaml:"background,omitempty"`
	// OnComplete fires after the job terminates. Effects in this list run in
	// the originating state's context. Cannot itself contain background: true
	// (validated at load time).
	OnComplete []Effect `yaml:"on_complete,omitempty"`
	// Target, when non-empty, is the state path the session transitions to
	// after this effect's mutations land. Only meaningful inside an
	// `on_complete:` chain — the orchestrator scans the chain for the
	// first effect with Target set and dispatches a synthetic transition
	// (TransitionApplied + StateExited + StateEntered + target on_enter)
	// once all preceding effects have applied without error. Mixing Target
	// with Set / Increment / Say / Invoke on the same effect is rejected
	// at load time (transition and mutation should live on separate
	// effects). Target is also rejected outside on_complete: blocks (use
	// a normal transition's target: instead). The standard Effect.When
	// guard still applies — a false guard skips the entire effect,
	// Target included.
	Target string `yaml:"target,omitempty"`
}

// ProposalKind declares a named proposal kind (§3).
type ProposalKind struct {
	// Schema declares the typed fields of the proposal draft.
	Schema map[string]string `yaml:"schema,omitempty"`
	// Draft configures the initial drafting step.
	Draft *ProposalStep `yaml:"draft,omitempty"`
	// Refine configures the refinement step.
	Refine *ProposalStep `yaml:"refine,omitempty"`
	// Execute declares the host invocation that runs the proposal.
	Execute *ProposalExecute `yaml:"execute,omitempty"`
	// Views holds optional view overrides per lifecycle phase.
	Views map[string]string `yaml:"views,omitempty"`
	// Policy declares acceptance and confirmation policies.
	Policy *ProposalPolicy `yaml:"policy,omitempty"`
}

// ProposalStep configures a draft or refine step.
type ProposalStep struct {
	// Prompt is the path to the prompt template used for this step.
	Prompt string `yaml:"prompt,omitempty"`
}

// ProposalExecute declares what happens when the proposal is executed.
type ProposalExecute struct {
	// Invoke is the host handler name to call.
	Invoke string `yaml:"invoke,omitempty"`
	// With are the templated args passed to the handler.
	With map[string]any `yaml:"with,omitempty"`
	// Repeatable controls whether rerun/modify_and_rerun are available after success.
	Repeatable bool `yaml:"repeatable,omitempty"`
	// OnSuccess declares the transition after successful execution.
	// Valid values: "stay", "back", or a named state.
	OnSuccess string `yaml:"on_success,omitempty"`
	// Background, when true, runs the execute as a background job (§4).
	Background bool `yaml:"background,omitempty"`
	// OnComplete declares effects to run when a background job completes.
	OnComplete []Effect `yaml:"on_complete,omitempty"`
}

// ProposalPolicy configures automatic acceptance and confirmation.
type ProposalPolicy struct {
	// AutoAcceptIf is an expr evaluated against {$proposal, $world, $slots}.
	// When true on drafting→reviewing, skip straight to executing.
	AutoAcceptIf string `yaml:"auto_accept_if,omitempty"`
	// RequireConfirm, when true, always requires explicit user confirmation before execute.
	RequireConfirm bool `yaml:"require_confirm,omitempty"`
}

// Intent is a named, typed action available in a state.
type Intent struct {
	Title       string          `yaml:"title,omitempty"`
	Description string          `yaml:"description,omitempty"`
	Examples    []string        `yaml:"examples,omitempty"`
	Priority    int             `yaml:"priority,omitempty"`
	Hidden      bool            `yaml:"hidden,omitempty"`
	Slots       map[string]Slot `yaml:"slots,omitempty"`
}

// Slot is a typed parameter on an intent.
type Slot struct {
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required,omitempty"`
	Default     any      `yaml:"default,omitempty"`
	Values      []string `yaml:"values,omitempty"` // enum values
	Description string   `yaml:"description,omitempty"`
	Examples    []string `yaml:"examples,omitempty"`
	FormatHint  string   `yaml:"format_hint,omitempty"`
	Prompt      string   `yaml:"prompt,omitempty"`
	Validator   string   `yaml:"validator,omitempty"` // expr guard expression
	// Format names a custom semantic format (e.g. "jql"). Validated by
	// the MCP validator's RegisterFormat hooks. Distinct from FormatHint,
	// which is documentation-only.
	Format string `yaml:"format,omitempty"`
}

// GuardExpr is a compiled guard expression (produced by internal/expr).
type GuardExpr struct {
	Source   string // original expr-lang source
	compiled any    // opaque compiled program; populated by internal/expr
}

// View holds a parsed view template for a state.
type View struct {
	Source   string // original template source
	compiled any    // opaque compiled template; populated by internal/expr
}

// OffPathDef configures the off-path escape hatch (§3.1, §7.7).
type OffPathDef struct {
	Trigger string `yaml:"trigger"`
	Banner  string `yaml:"banner,omitempty"`
	Return  string `yaml:"return,omitempty"`
	// Persona is an optional inline system-prompt-style instruction
	// prepended to every off-path oracle call. Lets apps style the
	// off-path "oracle" voice without declaring a top-level agent
	// (e.g., a frontier wise-man for Oregon Trail). Empty falls back
	// to the engine default. When both Persona and Agent are set,
	// Persona wins — apps can override a named agent inline for
	// off-path only.
	Persona string `yaml:"persona,omitempty"`
	// Agent, when non-empty, names an entry in AppDef.Agents whose
	// SystemPrompt is applied to every off-path oracle call. Resolved
	// at runtime via the process-wide agent registry installed at
	// startup by host.SetAgentRegistry. Mutually composable with
	// Persona (above) — Persona wins when both are set.
	Agent string `yaml:"agent,omitempty"`
}

// TimeoutDef configures an automatic state transition after a duration.
type TimeoutDef struct {
	After  string `yaml:"after"`
	Target string `yaml:"target"`
}

// AgentDecl is one entry in the top-level agents: map (meta-mode proposal
// §2.1). Exactly one of SystemPrompt or SystemPromptPath must be set; the
// loader resolves SystemPromptPath against the app YAML directory and
// rewrites SystemPrompt with the file contents (clearing SystemPromptPath).
type AgentDecl struct {
	// Exactly one of these is required.
	SystemPrompt     string `yaml:"system_prompt,omitempty"`
	SystemPromptPath string `yaml:"system_prompt_path,omitempty"`

	Model string   `yaml:"model,omitempty"`
	Tools []string `yaml:"tools,omitempty"`
	Cwd   string   `yaml:"cwd,omitempty"`
}

// MetaModeDef declares one meta mode (meta-mode proposal §2).
type MetaModeDef struct {
	Trigger string         `yaml:"trigger"`
	Label   string         `yaml:"label,omitempty"`
	Banner  string         `yaml:"banner,omitempty"`
	Agent   string         `yaml:"agent"`
	Persist *bool          `yaml:"persist,omitempty"`
	Cwd     string         `yaml:"cwd,omitempty"`
	Tools   []string       `yaml:"tools,omitempty"`
	Return  *MetaReturnDef `yaml:"return,omitempty"`
}

// MetaReturnDef configures the exit message and intent for a meta mode.
type MetaReturnDef struct {
	Message string `yaml:"message,omitempty"`
	Intent  string `yaml:"intent,omitempty"`
}

// PersistOrDefault returns true unless the author explicitly set persist: false.
func (m *MetaModeDef) PersistOrDefault() bool {
	if m == nil || m.Persist == nil {
		return true
	}
	return *m.Persist
}

// ExitIntentOrDefault returns the configured return.intent or "onpath" if unset.
func (m *MetaModeDef) ExitIntentOrDefault() string {
	if m == nil || m.Return == nil || m.Return.Intent == "" {
		return "onpath"
	}
	return m.Return.Intent
}
