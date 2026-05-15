// Package app holds the loaded, validated application definition.
// Types here map directly onto the YAML authoring format (§3) and carry
// yaml struct tags for deserialization via goccy/go-yaml.
package app

import "time"

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

	// Routing holds the per-app semantic-routing configuration
	// (semantic-routing proposal §6). Lives at the root app level and
	// is NOT merged across imports — when an importer folds a child,
	// the importer's Routing block wins and the child's is dropped.
	// A nil Routing means "use defaults" (see RoutingConfig.WithDefaults).
	Routing *RoutingConfig `yaml:"routing,omitempty"`

	// BaseDir is the absolute path to the directory containing the
	// root manifest. Populated by the loader (not by YAML authors).
	// Downstream consumers — notably internal/render.AppRenderer —
	// resolve <appDir>/views/ against this path so {% extends %} /
	// {% include %} references in pongo2 templates locate per-app
	// .pongo files (view-elements proposal phase H, §3.3).
	BaseDir string `yaml:"-"`
}

// RoutingConfig is the per-app semantic-routing block declared under
// `app.routing:` in YAML (semantic-routing proposal §6). All fields are
// optional; zero values are replaced by WithDefaults so a partially-
// specified routing: block still loads with sane defaults. The loader
// calls WithDefaults once after unmarshalling.
type RoutingConfig struct {
	// Enabled toggles the semantic-routing tier on this app. Defaults
	// to true; set false to keep today's deterministic-or-LLM behaviour.
	Enabled bool `yaml:"enabled,omitempty"`
	// SemanticHighBar is the confidence floor above which a semantic
	// verdict is submitted directly (§2.1). Defaults to 0.80.
	SemanticHighBar float64 `yaml:"semantic_high_bar,omitempty"`
	// SemanticMidBar is the confidence floor above which a verdict
	// triggers a clarification card (§2.1). Defaults to 0.65. Must be
	// strictly less than SemanticHighBar.
	SemanticMidBar float64 `yaml:"semantic_mid_bar,omitempty"`
	// CacheEnabled toggles the turn-result cache (§2.2). Defaults to true.
	CacheEnabled bool `yaml:"cache_enabled,omitempty"`
	// CacheMaxAge is the duration after which a cold cache row is
	// evicted (§7.4). Defaults to 30 days. Set "0" to disable.
	CacheMaxAge Duration `yaml:"cache_max_age,omitempty"`
	// StopwordsExtra extends the built-in stopword list (§2.3) with
	// app-specific filler ("yall", "wagon", …).
	StopwordsExtra []string `yaml:"stopwords_extra,omitempty"`
	// CacheCap is the row-count ceiling per app before LRU trim fires
	// (§7.3). Defaults to 10000.
	CacheCap int `yaml:"cache_cap,omitempty"`
	// CacheTrimFraction is the fraction of the cap evicted on overflow
	// (§7.3). Defaults to 0.10.
	CacheTrimFraction float64 `yaml:"cache_trim_fraction,omitempty"`
	// RevalidateStrikes is the number of consecutive revalidate
	// failures before a cache row is evicted (§7.2). Defaults to 3.
	RevalidateStrikes int `yaml:"revalidate_strikes,omitempty"`
	// ConfidenceDecay halves the effective CacheMaxAge for rows whose
	// originating LLM verdict had confidence < 0.7 (§7.5). Default off.
	ConfidenceDecay bool `yaml:"confidence_decay,omitempty"`
}

// DefaultRoutingConfig returns the all-defaults RoutingConfig used when
// an app declares no `routing:` block. Callers that find AppDef.Routing
// == nil should treat the app as if it carried this value.
func DefaultRoutingConfig() RoutingConfig {
	return RoutingConfig{
		Enabled:           true,
		SemanticHighBar:   0.80,
		SemanticMidBar:    0.65,
		CacheEnabled:      true,
		CacheMaxAge:       Duration(30 * 24 * time.Hour),
		CacheCap:          10000,
		CacheTrimFraction: 0.10,
		RevalidateStrikes: 3,
		ConfidenceDecay:   false,
	}
}

// WithDefaults returns a RoutingConfig where every zero-valued numeric /
// duration field is replaced by the corresponding default from
// DefaultRoutingConfig. Note: bool fields (Enabled, CacheEnabled,
// ConfidenceDecay) pass through unchanged — Go's zero value for bool is
// false, but two of the three default to true. The loader is responsible
// for unmarshalling routing: through a code path that seeds defaults
// before YAML decode, so an absent key keeps its default rather than
// being overwritten by the zero value. UnmarshalYAML on this type does
// exactly that (see below).
//
// String/slice fields (StopwordsExtra) pass through untouched: a nil
// slice is the natural "no extras."
func (r RoutingConfig) WithDefaults() RoutingConfig {
	d := DefaultRoutingConfig()
	out := r
	if out.SemanticHighBar == 0 {
		out.SemanticHighBar = d.SemanticHighBar
	}
	if out.SemanticMidBar == 0 {
		out.SemanticMidBar = d.SemanticMidBar
	}
	if out.CacheMaxAge == 0 {
		out.CacheMaxAge = d.CacheMaxAge
	}
	if out.CacheCap == 0 {
		out.CacheCap = d.CacheCap
	}
	if out.CacheTrimFraction == 0 {
		out.CacheTrimFraction = d.CacheTrimFraction
	}
	if out.RevalidateStrikes == 0 {
		out.RevalidateStrikes = d.RevalidateStrikes
	}
	return out
}

// UnmarshalYAML implements goccy/go-yaml's BytesUnmarshaler. It seeds
// the receiver with DefaultRoutingConfig before decoding the YAML body
// so author-omitted bool fields (`enabled:`, `cache_enabled:`) keep
// their default of true rather than landing on Go's zero value. After
// decode the function calls WithDefaults to backfill numeric/duration
// fields the author left out. The combined effect: a partial
// `routing:` block like
//
//	routing: { semantic_high_bar: 0.85 }
//
// loads as { Enabled:true, CacheEnabled:true, SemanticHighBar:0.85,
// SemanticMidBar:0.65, CacheMaxAge:30d, CacheCap:10000, … }.
func (r *RoutingConfig) UnmarshalYAML(b []byte) error {
	*r = DefaultRoutingConfig()
	// Decode into a temporary type alias so we don't recurse into this
	// UnmarshalYAML. The defaults seeded above survive any field the
	// YAML body omits.
	type raw RoutingConfig
	tmp := raw(*r)
	if err := unmarshalRoutingRaw(b, &tmp); err != nil {
		return err
	}
	*r = RoutingConfig(tmp).WithDefaults()
	return nil
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
	//
	// The View type custom-unmarshals YAML and accepts either the legacy
	// scalar string form (a Markdown / pongo2 template body) or the typed
	// element-array form introduced by the view-elements proposal
	// (docs/proposals/view-elements-proposal.md). See view_element.go for
	// the schema; the array form is opt-in per-state.
	View View `yaml:"view,omitempty"`
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

	// IntentAliases records the bare → renamed mapping produced by the
	// imports rewriter. When this state lives inside one or more import
	// alias wrappers, every intent that the rewriter renamed (e.g.
	// `accept` → `bf__accept`, then `bf__accept` → `core__bf__accept`
	// on a second fold) gains an entry here. Both the original bare
	// name and any intermediate prefixed names point to the final,
	// fully-prefixed key actually present in `On`. Used at runtime by
	// the emit_intent dispatcher to resolve a bare intent name emitted
	// by an LLM-judge inside the imported child against the renamed
	// arc on the current state. Never set by YAML authors; populated
	// by `internal/app/imports_rewriter.go::rewriteState` during fold.
	IntentAliases map[string]string `yaml:"-"`
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
	// When present it wins over the target state's view for this turn. Same
	// schema as State.View — see view_element.go.
	View View `yaml:"view,omitempty"`
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

	// EmitIntent dispatches a synthetic intent against the current state
	// after the surrounding effects chain completes. Used to auto-advance
	// from on_enter (e.g. LLM judge → "accept") and from transition effect
	// chains where a follow-up intent should fire without an external
	// driver. Together with EmitSlots it forms a self-loop within the
	// single-turn budget.
	//
	// Loader enforces:
	//   - non-empty EmitIntent must resolve to an intent declared on the
	//     current state's `on:` arcs (after compile-time intent prefix
	//     expansion through imports);
	//   - the synthetic dispatch is bounded by the depth cap (see
	//     EmitIntentMaxDepth in the machine package);
	//   - mixing EmitIntent with Target on the same effect is rejected.
	//
	// Template values are supported — the literal string may itself be a
	// `{{ ... }}` expression resolved at fire time against the current
	// world (e.g. `emit_intent: "{{ world.llm_verdict.intent }}"`).
	EmitIntent string `yaml:"emit_intent,omitempty"`

	// EmitSlots holds slot values passed to the synthesised intent call.
	// Expression interpolation supported, evaluated against the world AT
	// THE TIME the emit_intent effect fires (post all preceding effects
	// in the same chain).
	EmitSlots map[string]any `yaml:"slots,omitempty"`
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
	// Synonyms is the author-declared list of alternate phrasings that
	// resolve to this intent (semantic-routing proposal §4.1, §4.3).
	// Each entry is either a plain phrase ("wade", "walk it") or a
	// template-shaped phrase ("buy {items} for {total_cost}"). At
	// Phase 0 the loader stores the raw strings and validates that
	// they're non-empty; template compilation lands in Phase 4
	// (internal/semroute).
	Synonyms []string `yaml:"synonyms,omitempty"`
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
	// Synonyms maps each enum value to a list of alternate phrasings
	// (semantic-routing proposal §4.2). Only meaningful when
	// Type == "enum"; the loader rejects the field on non-enum slots
	// and rejects keys that are not in Values.
	Synonyms map[string][]string `yaml:"synonyms,omitempty"`
}

// GuardExpr is a compiled guard expression (produced by internal/expr).
type GuardExpr struct {
	Source   string // original expr-lang source
	compiled any    // opaque compiled program; populated by internal/expr
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

// MetaModeDef declares one meta mode.
//
// Group + Trigger form the `group + verb` namespacing scheme. Map keys
// are `<group>.<verb>` (e.g. "story.bug"); the trigger parser splits
// `/meta <group> <verb>` on whitespace and resolves via that key.
// Exactly one mode per group may set Default:true — it is the verb
// bare `/meta <group>` resolves to.
//
// Backward compat: an un-namespaced YAML mode (key has no `.`) is
// treated by the loader as having Group == its key and no default-verb
// rule (a single-mode group is implicitly default-able). See
// docs/meta-mode.md §3.2 for the user-facing reference.
type MetaModeDef struct {
	Trigger string `yaml:"trigger"`
	// Group is the namespace token (`story`, `kitsoki`, or an
	// app-defined name). When set, the map key MUST be `Group.Trigger`.
	// Optional for back-compat with un-namespaced YAML.
	Group string `yaml:"group,omitempty"`
	// Default flags the verb that bare `/meta <Group>` resolves to.
	// Exactly one mode per group may set this; the validator enforces
	// the rule for groups with ≥2 modes.
	Default bool           `yaml:"default,omitempty"`
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
