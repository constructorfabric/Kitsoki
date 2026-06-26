package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/intent"
	"kitsoki/internal/render"
	"kitsoki/internal/render/elements"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// AllowedIntent describes one intent that is currently valid for the user,
// as produced by Machine.AllowedIntents for the progressive-disclosure menu.
type AllowedIntent struct {
	Name        string   `json:"name"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	Priority    int      `json:"priority,omitempty"`
	Hidden      bool     `json:"hidden,omitempty"`
}

// ValidationResult is the return type of Machine.Validate. Exactly one of
// Accepted or Err is non-nil.
type ValidationResult struct {
	OK       bool
	Accepted intent.IntentCall
	Err      *intent.ValidationError
}

// HostInvocation describes a host.* side-effect call that the caller must
// dispatch outside the pure machine. The machine only constructs and returns
// these; it never executes them, which is what keeps a turn reproducible.
//
// The zero value is a no-op descriptor (empty Namespace, nil maps); callers
// dispatch only the non-zero entries the machine collected. A HostInvocation
// is plain data with no internal synchronisation — a single turn's slice is
// owned by one goroutine (the orchestrator dispatching them), so concurrent
// mutation of the same value is the caller's responsibility, not the machine's.
type HostInvocation struct {
	Namespace string         `json:"namespace"`
	Args      map[string]any `json:"args,omitempty"`
	// RawWith carries the *unresolved* `with:` templates from the YAML so the
	// orchestrator can re-render them at dispatch time, after any earlier host
	// call's `bind:` has updated the world.  This makes 2-step `on_enter:`
	// blocks work — e.g. step 1 binds a context envelope into
	// `world.<id>_context`, step 2's `args.context: "{{ world.<id>_context.data }}"`
	// resolves against the *post-step-1* world rather than the snapshot the
	// machine had when it queued the host calls.  Args is still populated with
	// the best-effort up-front resolution so callers that don't re-render get
	// reasonable behaviour.
	RawWith map[string]any `json:"raw_with,omitempty"`
	// Env is the expression-evaluation environment to use for re-rendering
	// RawWith.  Captured from the machine's effect-walk so the orchestrator
	// has access to the same slots/event/run scope (the World is overridden
	// at re-render time with the world snapshot below plus dispatch binds).
	Env any `json:"-"`
	// WorldSnapshot is a clone of the world AS OF THIS INVOKE'S POSITION in
	// the effect list — earlier `set:`/`increment:` effects are reflected,
	// LATER ones are NOT. The orchestrator re-renders RawWith against this
	// snapshot overlaid with the binds accumulated from earlier invokes in
	// the same chain. Using the snapshot (rather than the final post-chain
	// world) is what keeps a later `set:` from clobbering an earlier
	// invoke's `with:` arg — e.g. proposal/restart archives the chat with
	// `{{ world.proposal_chat_id }}` and then a following `set:` clears that
	// key; the archive must still see the pre-clear value. Empty for
	// HostInvocations built outside the effect-walk (older paths / test
	// stubs), in which case the orchestrator falls back to the live world.
	WorldSnapshot map[string]any `json:"-"`
	// Bind maps world variable names to keys in the host result's Data map.
	// e.g. bind: {workspace: "id"} copies result.Data["id"] into world["workspace"].
	Bind map[string]string `json:"bind,omitempty"`
	// OnError is a state path to transition to when the host returns an error.
	// When non-empty and the host fails, the machine should transition there
	// rather than erroring out. Before the redirect the engine sets the
	// reserved global world vars last_error (string) and host_error
	// ({namespace, message, data?, stderr?, exit_code?}); both are exempt from
	// import folding and readable by the target room without declaration.
	OnError   string `json:"on_error,omitempty"`
	EmitEvent string `json:"emit_event,omitempty"`
	// Background, when true, signals that the orchestrator should submit
	// this invocation to the scheduler instead of dispatching synchronously.
	Background bool `json:"background,omitempty"`
	// OnComplete is the saved effect chain to run when the job terminates.
	// The orchestrator persists these alongside the job spec; the machine
	// does not consume them.
	OnComplete []app.Effect `json:"on_complete,omitempty"`
	// AgentPlugin is the agent alias (e.g. "agent.autofix_fixer") declared
	// on the effect via the `agent:` field. Empty means resolve to the default
	// "agent.claude". Carried from app.Effect.AgentPlugin so the orchestrator
	// can route through host.Dispatch with the correct plugin.
	AgentPlugin string `json:"agent_plugin,omitempty"`
}

// TurnResult is returned by Machine.Turn after a successful transition. It is
// a snapshot of one turn's outcome and carries no internal synchronisation: it
// is safe to read from multiple goroutines once Turn has returned, but the
// caller must not mutate its slices/maps concurrently. The zero value is the
// "nothing happened" result (empty NewState, zero World, nil ValidationError);
// Turn never returns it for an accepted call.
//
// When the intent was rejected, ValidationError is non-nil and NewState/World
// equal the inputs unchanged — see Machine.Turn.
type TurnResult struct {
	NewState  app.StatePath    `json:"new_state"`
	World     world.World      `json:"world"`
	View      string           `json:"view"`
	Menu      []string         `json:"menu"`
	Events    []store.Event    `json:"events,omitempty"`
	HostCalls []HostInvocation `json:"host_calls,omitempty"`
	// ValidationError is set when the intent was rejected (no transition fired).
	// In that case NewState equals the input state and World is unchanged.
	ValidationError *intent.ValidationError `json:"validation_error,omitempty"`
	// TypedView is the parsed app.View used to produce View, when the
	// state's view payload is the typed element-array form (Issue 4 /
	// option (a) in the brief). Nil for legacy string views, for
	// extends:/template_file: forms, and for empty views. The
	// orchestrator copies it onto TurnOutcome so the TUI can re-render
	// at the live viewport width on resize.
	TypedView *app.View `json:"-"`
	// RenderEnv is the expr.Env captured at the render seam. Carried
	// alongside TypedView so the TUI's resize-time re-render uses the
	// same world / slots / menu snapshot the machine saw.
	RenderEnv expr.Env `json:"-"`
	// Renderer is the per-namespace AppRenderer for the new state's
	// alias (Issue 3). Carried alongside TypedView so leaf-string
	// {% include %} / {% extends %} inside the typed view's elements
	// resolve against the right per-app views/ tree on re-render.
	Renderer *render.AppRenderer `json:"-"`
}

// Machine is the pure deterministic core. See the package doc for the turn
// algorithm, event-ordering invariants, and concurrency contract.
type Machine interface {
	Turn(ctx context.Context, cur app.StatePath, w world.World, call intent.IntentCall) (TurnResult, error)
	AllowedIntents(cur app.StatePath, w world.World) []AllowedIntent
	// DecisionCandidates returns the intents an LLM/human decider may choose
	// between at a gate (firable, advancing, non-exit, non-self). See the impl.
	DecisionCandidates(cur app.StatePath, w world.World) []AllowedIntent
	// IsDecisionGate reports whether the state is a multi-way decision gate
	// (has a forward intent that is not an auto-emit target). See isDecisionGate.
	IsDecisionGate(cur app.StatePath, w world.World) bool
	Validate(cur app.StatePath, w world.World, call intent.IntentCall) ValidationResult
	// LookupIntent resolves an intent definition by name scoped to the given
	// state (state-local intents shadow the global library; parallel-encoded
	// paths probe each region leaf). Read-only callers (e.g. the runstatus web
	// surface, which needs each allowed intent's slot schema to tell the
	// browser which input box to bind) use it without re-deriving the
	// state→intent resolution rules.
	LookupIntent(cur app.StatePath, name string) (app.Intent, bool)
	// RenderState recomputes the view for the given state path and world snapshot.
	// Used by the orchestrator to refresh the view after host-call bindings land
	// so the user sees the updated world on the same turn.
	RenderState(cur app.StatePath, w world.World) (string, error)
	// RenderStateTyped is RenderState with the typed-view payload surfaced.
	// Returns the rendered text plus, when the resolved leaf's view is a
	// pure element-array form, the typed View / env / renderer needed for
	// the TUI to re-render at viewport width without double-processing
	// the ANSI output through Glamour. Used by the initial-paint seam in
	// Orchestrator.InitialViewTyped so the first frame of a typed-view
	// root state (e.g. dev-story/rooms/main.yaml's heading/prose/kv
	// composition) renders the same way subsequent turns do.
	RenderStateTyped(cur app.StatePath, w world.World) (string, *app.View, expr.Env, *render.AppRenderer, error)
	// TryGuards performs a dry-run of the guards for the given intent and
	// prefilled slots. It returns the resolved target state path (if a guard
	// matches) and the blocking hint (if no guard matches). It never mutates
	// state or world.
	//
	// When a slot referenced by a guard is absent from prefillSlots, expr will
	// evaluate to an error or zero value. We catch that and treat the guard as
	// "unresolved" — returning (target="", hint="", resolved=false, blocked=false).
	// Callers should treat unresolved as primary (passes by default): the guard
	// will be checked at submission time when all slots are present.
	TryGuards(cur app.StatePath, w world.World, intentName string, prefillSlots map[string]any) GuardDryRunResult
	// Menu returns the computed menu (primary + blocked entries) for
	// the given state and world. View-render call sites populate env.Menu
	// with the template-friendly view of this so authors can render the
	// "what can I do right now" surface inline.
	Menu(state app.StatePath, w world.World) MenuView
	// RunEffects walks effects and returns the new world, any host calls
	// collected, the accumulated say-text, the effect events, and an error.
	// State path is used purely for the env snapshot (slots, etc.).
	// This is the on_complete bridge entry-point: the orchestrator calls it
	// with the post-job world to apply the on_complete effect chain.
	//
	// Note on emit_intent: any synthetic intents captured during the
	// chain are dispatched against `state` BEFORE returning, and the
	// events from those dispatches are folded into the returned event
	// slice. The orchestrator caller still owns the "current state"
	// — callers that need to advance the session after RunEffects fires
	// an emit_intent should derive the new state from
	// RunEffectsAndState (added in Wave 1 for emit_intent support).
	RunEffects(ctx context.Context, state app.StatePath, w world.World, effects []app.Effect) (world.World, []HostInvocation, string, []store.Event, error)

	// RunEffectsAndState is the emit_intent-aware variant of RunEffects.
	// It additionally returns the leaf-state path after any synthetic
	// emit_intent dispatches have settled. When no emit_intent fires,
	// the returned state equals the input state. Callers that drive
	// the session forward (oncomplete / timeout) use this to learn
	// where the chain landed.
	RunEffectsAndState(ctx context.Context, state app.StatePath, w world.World, effects []app.Effect) (app.StatePath, world.World, []HostInvocation, string, []store.Event, error)
	RunEffectsAndStateWithOptions(ctx context.Context, state app.StatePath, w world.World, effects []app.Effect, opts RunEffectsOptions) (app.StatePath, world.World, []HostInvocation, string, []store.Event, error)

	// DispatchPostBindEmits re-evaluates the emit_intent: effects on
	// the entered state's on_enter chain against a post-bind world
	// snapshot, then dispatches any whose `when:` guard now passes.
	// This is the bridge that makes emit_intent: composable with
	// host.* invocations that bind into world AFTER machine.Turn
	// returns — the canonical case being the bugfix story's LLM-judge
	// step (host.agent.ask_with_mcp binds llm_verdict, the next
	// effect's `when:` reads it).
	//
	// The pass walks only emit_intent: entries; set/increment/say/invoke
	// effects have already fired at machine.Turn time and are NOT
	// re-applied. Returns the new leaf state, post-emit world, host
	// calls collected by the synthetic dispatches, accumulated
	// say-text, the events to append (TransitionApplied / EffectApplied /
	// StateExited / StateEntered for each fired emit), and any error.
	//
	// When the state has no emit_intent: effects (or none whose guard
	// passes), returns the inputs unchanged with empty event/hostCall
	// slices — callers can guard on len(events) before doing anything.
	//
	// When `staged` is true, the synthetic chain STOPS before firing any
	// emit at a state that is a multi-way decision gate (>1 advancing
	// intent currently available) — a phase boundary ends the turn so a
	// human can decide. A GateDecided event records the stop. In one-shot
	// mode (staged=false) the chain advances exactly as before. See the
	// "turn loop" section of docs/stories/state-machine.md for how staged
	// and one-shot execution modes drive the orchestrator.
	//
	// onEnter, when non-nil, is fired once per synthetic hop with the room
	// entered and that hop's say-text, so callers can stream per-room
	// progress breadcrumbs live during a one-shot chain. Pass nil to
	// disable (the say-text is still returned merged in the string result).
	DispatchPostBindEmits(ctx context.Context, state app.StatePath, w world.World, staged bool, onEnter onRoomEnterFn) (app.StatePath, world.World, []HostInvocation, string, []store.Event, error)

	// ResolveInitialLeaf descends a compound state to its initial leaf,
	// recursively. For non-compound states (or unknown paths) it returns
	// the input unchanged. The world is needed because a compound's
	// `initial:` field can be a pongo template (e.g. cloak's
	// `{% if world.wearing_cloak %}dark{% else %}lit{% endif %}`).
	//
	// Used by the orchestrator on session start when the app's root is
	// itself a compound (e.g. an import-alias root in a dogfood instance:
	// `root: core` where `core` is an import wrapper with `initial: main`).
	// Without this descent, the first frame renders against the bare
	// compound wrapper — which has no view block — and the operator sees
	// an empty intro with no intents.
	ResolveInitialLeaf(cur app.StatePath, w world.World) (app.StatePath, error)
}

// RunEffectsOptions tunes an explicit effect replay. Normal story turns use the
// zero value; callers such as `/reload --force` can opt into authoring-only
// behavior without changing ordinary story semantics.
type RunEffectsOptions struct {
	// ForceOnce ignores `once: true` cache skips for this effect run. It is used
	// by `/reload --force` when an author intentionally wants to re-run an
	// on_enter call whose bind target is already populated.
	ForceOnce bool
}

// GuardDryRunResult is the result of [Machine.TryGuards] — a read-only
// "what would happen if I tried this intent now" probe used to build the menu
// without mutating state or world. The zero value reads as "blocked, no
// destination, no reason": every bool is false and both string fields empty,
// which is the safe default for a guard that could not be resolved. Primary and
// Blocked are mutually exclusive; Unresolved is reported as Primary (it passes
// by default and is re-checked at submission time when all slots are present).
type GuardDryRunResult struct {
	// Primary is true when a guard arm matched (or is a default/no-guard branch).
	Primary bool
	// Blocked is true when all guard arms were evaluated and none matched (and no default).
	Blocked bool
	// Unresolved is true when at least one guard could not be evaluated because
	// a referenced slot was missing from the prefill map. Treat as primary.
	Unresolved bool
	// MatchedDefault is true when Primary is true because a default: branch fired
	// (i.e. no when: branch matched). Menu-building code uses this to decide
	// whether to surface, omit, or demote the entry.
	MatchedDefault bool
	// WhenArmFailed is true when at least one when: arm was evaluated and
	// returned false during the walk. Combined with MatchedDefault this means
	// "the player can type the intent but only the catch-all fires" — menu
	// code surfaces such entries as blocked so the player sees why their
	// "real" arc isn't available yet (with BlockedReason carrying the hint).
	WhenArmFailed bool
	// DestinationHint is the resolved target state path when Primary is true.
	DestinationHint string
	// BlockedReason is the guard_hint from the first failing when: arm. Set
	// whenever WhenArmFailed is true — whether or not a default eventually
	// matched.
	BlockedReason string
}

// ─── compiled guards ─────────────────────────────────────────────────────────

// compiledTransition holds a Transition with its pre-compiled guard program.
type compiledTransition struct {
	tr    app.Transition
	guard *expr.Program // nil when no When guard
}

// compiledState is a State with pre-compiled guard programs on every transition.
type compiledState struct {
	s    *app.State
	on   map[string][]compiledTransition // intent name -> compiled transitions
	view *expr.Program                   // nil when no view
}

// machineImpl is the concrete Machine implementation.
type machineImpl struct {
	appDef *app.AppDef
	states map[string]*compiledState // dot-separated path -> compiled state
	logger *slog.Logger
	// renderer is the host-app renderer (rooted at appDef.BaseDir/views/).
	// Used when a state path has no alias prefix or its alias has no
	// dedicated sub-story renderer.
	renderer *render.AppRenderer
	// importRenderers maps an import alias (the first segment of a state
	// path, e.g. "bandits", "frontier") to a pongo2 renderer rooted at
	// that sub-story's <BaseDir>/views/. Built by New from the AppDef's
	// ImportWrappers metadata so child-app states that ship their own
	// extends:/include: targets resolve against the child's own views/
	// (Issue 3). A state path with no alias prefix or an alias not in
	// this map falls back to renderer.
	importRenderers map[string]*render.AppRenderer
}

// MachineOption is a functional option for Machine construction. Options exist
// so tests and specialised callers can customise the logger or renderer without
// widening New's signature or exposing the unexported machineImpl — production
// call sites pass none and get the documented defaults.
type MachineOption func(*machineImpl)

// WithMachineLogger sets the logger for guard/effect trace events.
func WithMachineLogger(l *slog.Logger) MachineOption {
	return func(m *machineImpl) {
		if l != nil {
			m.logger = l
		}
	}
}

// WithMachineRenderer overrides the default per-app renderer. Used by
// tests that want a cached renderer (the default New builds an uncached
// renderer per ideas.md L37's "edits take effect without restart"
// goal). Production call sites should leave the default.
func WithMachineRenderer(r *render.AppRenderer) MachineOption {
	return func(m *machineImpl) {
		if r != nil {
			m.renderer = r
		}
	}
}

// New creates a new Machine from a validated AppDef.
// It pre-compiles all guards, view templates, and guard hints.
// Returns an error (via errors.Join) listing every compilation failure.
// Returns an error if any parallel state is malformed.
func New(def *app.AppDef, opts ...MachineOption) (Machine, error) {
	m := &machineImpl{
		appDef:          def,
		states:          make(map[string]*compiledState),
		logger:          slog.Default(),
		importRenderers: map[string]*render.AppRenderer{},
	}
	// Build the per-app pongo2 renderer rooted at <BaseDir>/views/.
	// Uncached by default so app-author edits to .pongo files take effect
	// without a restart (ideas.md L37). The renderer is tolerant of a
	// missing views/ directory — apps without templates still build a
	// renderer that handles inline rendering via the fast path
	// identically to the package-level render.Pongo function.
	//
	// When def has no BaseDir (the LoadBytes() path used by tests and
	// the inspect/test_intents commands) we still build a renderer; the
	// empty appDir means the renderer's loader is a no-op, but inline
	// {{ … }} expansion via Render() works unchanged.
	if def != nil {
		r, rerr := render.NewAppRenderer(def.BaseDir)
		if rerr != nil {
			// Construction errors here are limited to "views path is a
			// file, not a directory" — surface them at machine-build
			// time rather than at the first render.
			return nil, fmt.Errorf("machine.New: build renderer for %q: %w", def.BaseDir, rerr)
		}
		m.renderer = r

		// Per-import-alias renderers. Each imported child manifest may
		// ship its own views/ tree (e.g. stories/robbery/views/base.pongo);
		// states folded under that alias (`bandits.encounter`, ...) must
		// resolve `extends: "base"` against the *child's* views/, not the
		// host's. ImportWrappers carries SourcePath (the child's app.yaml);
		// the directory of that path is the child's BaseDir.
		for alias, w := range def.ImportWrappers {
			if w == nil || w.SourcePath == "" {
				continue
			}
			childDir := filepath.Dir(w.SourcePath)
			cr, cerr := render.NewAppRenderer(childDir)
			if cerr != nil {
				return nil, fmt.Errorf("machine.New: build renderer for import alias %q at %q: %w", alias, childDir, cerr)
			}
			m.importRenderers[alias] = cr
		}
	}
	for _, opt := range opts {
		opt(m)
	}

	var errs []error

	// Validate parallel-state shape (≥2 regions, no parent initial:,
	// no nested parallel).
	if err := validateParallelStates("", def.States); err != nil {
		return nil, err
	}

	// Pre-compile all states.
	compileStatesInto("", def.States, m.states, &errs)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return m, nil
}

// compileStatesInto pre-compiles every state's guards and views into the dst map.
func compileStatesInto(prefix string, states map[string]*app.State, dst map[string]*compiledState, errs *[]error) {
	for name, s := range states {
		if s == nil {
			continue
		}
		path := joinStatePath(prefix, name)
		cs := &compiledState{s: s, on: make(map[string][]compiledTransition)}

		// Compile view template.
		if !s.View.IsEmpty() {
			// Views are rendered with Render(); no pre-compilation needed since
			// our custom template parser is lazy. We store nil and call Render() at runtime.
			// (Pre-compilation is possible but the Render function handles caching internally.)
			cs.view = nil
		}

		// Compile transitions.
		for intentName, transitions := range s.On {
			cts := make([]compiledTransition, 0, len(transitions))
			for _, tr := range transitions {
				ct := compiledTransition{tr: tr}
				// Compile guard.
				if tr.When != "" {
					p, err := expr.CompileBool(tr.When)
					if err != nil {
						*errs = append(*errs, fmt.Errorf("state %q intent %q guard %q: %w", path, intentName, tr.When, err))
					} else {
						ct.guard = p
					}
				}
				cts = append(cts, ct)
			}
			cs.on[intentName] = cts
		}

		dst[path] = cs

		// Recurse into child states.
		if len(s.States) > 0 {
			compileStatesInto(path, s.States, dst, errs)
		}
	}
}

// joinStatePath joins a prefix and a name with a dot separator.
func joinStatePath(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

// ─── Machine.Validate ────────────────────────────────────────────────────────

// Validate checks whether an intent call is permissible in the current state.
// It does NOT apply any transition — that is Turn's job.
func (m *machineImpl) Validate(cur app.StatePath, w world.World, call intent.IntentCall) ValidationResult {
	allowed := m.allowedIntentNames(cur)

	// 1. Check intent is allowed in this state.
	// An intent is allowed if:
	//   a) It is explicitly handled in the state's on: block (or an ancestor's), OR
	//   b) The state (or an ancestor) has a "*" wildcard handler.
	if !isAllowed(call.Intent, allowed) && !m.hasWildcard(cur) {
		return ValidationResult{
			Err: &intent.ValidationError{
				Code:           intent.ErrIntentNotAllowed,
				Message:        fmt.Sprintf("intent %q is not allowed in state %q", call.Intent, cur),
				AllowedIntents: allowed,
			},
		}
	}

	// 2. Validate slots against the intent's schema (only if the intent is defined).
	intentDef, ok := m.lookupIntent(cur, call.Intent)
	if !ok {
		// Intent not defined anywhere but wildcard will handle it; skip slot validation.
		return ValidationResult{OK: true, Accepted: call}
	}
	if err := validateSlots(intentDef, call.Slots); err != nil {
		return ValidationResult{Err: err}
	}

	return ValidationResult{OK: true, Accepted: call}
}

// hasWildcard returns true if the state or any ancestor has a "*" wildcard handler.
// Parallel-encoded paths return true if ANY region (or its ancestor chain) has one.
func (m *machineImpl) hasWildcard(cur app.StatePath) bool {
	if par := parseParallel(string(cur)); par.IsParallel {
		for _, leaf := range par.RegionLeaves {
			if m.hasWildcard(app.StatePath(leaf)) {
				return true
			}
		}
		return false
	}
	path := string(cur)
	for {
		cs, ok := m.states[path]
		if ok {
			if _, hasWC := cs.on["*"]; hasWC {
				return true
			}
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
	return false
}

// allowedIntentNames returns the names of intents that are handled in the
// given state (including inherited handlers from compound-state ancestors,
// but for PoC we only look at the leaf state and its direct parent chain).
//
// Parallel-encoded paths return the union of allowed intents
// across every region leaf, so the orchestrator's menu and Validate code
// see the full surface without needing to know about parallel encoding.
func (m *machineImpl) allowedIntentNames(cur app.StatePath) []string {
	seen := make(map[string]struct{})
	if par := parseParallel(string(cur)); par.IsParallel {
		for _, leaf := range par.RegionLeaves {
			for _, n := range m.allowedIntentNames(app.StatePath(leaf)) {
				seen[n] = struct{}{}
			}
		}
	} else {
		// Walk the path from leaf to root, collecting on: keys.
		path := string(cur)
		for {
			cs, ok := m.states[path]
			if ok {
				for name := range cs.on {
					if name != "*" { // wildcard is not a public intent name
						seen[name] = struct{}{}
					}
				}
			}
			// Move up one level.
			idx := strings.LastIndexByte(path, '.')
			if idx < 0 {
				break
			}
			path = path[:idx]
		}
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// isAllowed returns true if name is in the allowed list.
func isAllowed(name string, allowed []string) bool {
	for _, a := range allowed {
		if a == name {
			return true
		}
	}
	return false
}

// LookupIntent is the exported wrapper over lookupIntent for read-only callers
// (the runstatus web surface) that need an allowed intent's resolved slot
// schema. It applies the same state-local-shadows-global resolution.
func (m *machineImpl) LookupIntent(cur app.StatePath, name string) (app.Intent, bool) {
	return m.lookupIntent(cur, name)
}

// lookupIntent looks up an intent definition by name scoped to the given state.
// Parallel-encoded paths probe each region leaf in turn; the first match wins
// (regions are alphabetical so the result is deterministic).
func (m *machineImpl) lookupIntent(cur app.StatePath, name string) (app.Intent, bool) {
	if par := parseParallel(string(cur)); par.IsParallel {
		for _, leaf := range par.RegionLeaves {
			if i, ok := m.lookupIntent(app.StatePath(leaf), name); ok {
				return i, true
			}
		}
		if i, ok := m.appDef.Intents[name]; ok {
			return i, true
		}
		return app.Intent{}, false
	}
	// Check state-local intents first, then global library.
	path := string(cur)
	for {
		cs, ok := m.states[path]
		if ok && cs.s != nil {
			if i, found := cs.s.Intents[name]; found {
				return i, true
			}
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
	if i, ok := m.appDef.Intents[name]; ok {
		return i, true
	}
	return app.Intent{}, false
}

// validateSlots validates the provided slot values against the intent's slot schema.
func validateSlots(intentDef app.Intent, slots world.Slots) *intent.ValidationError {
	if slots == nil {
		slots = world.Slots{}
	}
	var missing []string
	for slotName, slotDef := range intentDef.Slots {
		val, present := slots[slotName]
		if slotDef.Required && (!present || val == nil) {
			missing = append(missing, slotName)
			continue
		}
		// Fill in the declared default for an absent optional slot so
		// downstream effects (set/say templates) can read slots.<name>
		// without needing per-effect `?? <default>` guards. The Slot.Default
		// field was previously documentation-only; this makes it real.
		if !present && slotDef.Default != nil {
			slots[slotName] = slotDef.Default
			val = slotDef.Default
			present = true
		}
		if !present {
			continue
		}
		// Enum validation.
		if len(slotDef.Values) > 0 {
			valStr := fmt.Sprintf("%v", val)
			valid := false
			for _, ev := range slotDef.Values {
				if ev == valStr {
					valid = true
					break
				}
			}
			if !valid {
				return &intent.ValidationError{
					Code:    intent.ErrInvalidSlotValue,
					Message: fmt.Sprintf("slot %q value %q is not one of %v", slotName, valStr, slotDef.Values),
				}
			}
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return &intent.ValidationError{
			Code:         intent.ErrMissingSlots,
			Message:      fmt.Sprintf("intent requires slots: %v", missing),
			MissingSlots: missing,
		}
	}
	return nil
}

// ─── Machine.Turn ────────────────────────────────────────────────────────────

// Turn applies one accepted intent call and returns the result.
// All state mutations are on a cloned world — the caller's world is not mutated.
func (m *machineImpl) Turn(ctx context.Context, cur app.StatePath, w world.World, call intent.IntentCall) (TurnResult, error) {
	// 0. Parallel-encoded path? Dispatch to the parallel-state turn handler.
	if par := parseParallel(string(cur)); par.IsParallel {
		return m.turnParallel(ctx, par, w, call)
	}

	// 1. Validate first.
	vr := m.Validate(cur, w, call)
	if !vr.OK {
		return TurnResult{
			NewState:        cur,
			World:           w,
			ValidationError: vr.Err,
			Events: []store.Event{
				newEvent(store.ValidationFailed, map[string]any{
					"code":    string(vr.Err.Code),
					"message": vr.Err.Message,
					"intent":  call.Intent,
					"state":   string(cur),
				}),
			},
		}, nil
	}

	// 2. Build eval env.
	env := expr.Env{
		Slots: slotsToMap(call.Slots),
		World: w.Vars,
		Event: make(map[string]any),
	}

	// 3. Find the active state and walk the intent's transition arms.
	leafPath := string(cur)
	winningTr, winningPath, hint, err := m.findTransitionTraced(ctx, leafPath, call.Intent, env)
	if err != nil {
		return TurnResult{}, err
	}

	if winningTr == nil {
		// All guards failed, no default.
		m.logger.DebugContext(ctx, trace.EvMachineValidationRejected,
			slog.String("intent", call.Intent),
			slog.String("state", string(cur)),
			slog.String("error_code", string(intent.ErrGuardFailed)),
		)
		ve := &intent.ValidationError{
			Code:      intent.ErrGuardFailed,
			Message:   fmt.Sprintf("no transition matched for intent %q in state %q", call.Intent, cur),
			GuardHint: hint,
		}
		return TurnResult{
			NewState:        cur,
			World:           w,
			ValidationError: ve,
			Events: []store.Event{
				newEvent(store.ValidationFailed, map[string]any{
					"code":       string(intent.ErrGuardFailed),
					"intent":     call.Intent,
					"state":      string(cur),
					"guard_hint": hint,
				}),
			},
		}, nil
	}

	// 4. Resolve the target state path.
	// Target may be a template expression like "{{ world.prev_state }}"; evaluate it first.
	rawTarget := winningTr.tr.Target
	if strings.Contains(rawTarget, "{{") {
		rendered, renderErr := render.Pongo(rawTarget, env)
		if renderErr != nil {
			return TurnResult{}, fmt.Errorf("render transition target %q: %w", rawTarget, renderErr)
		}
		rawTarget = strings.TrimSpace(rendered)
	}
	targetPath := resolveTarget(winningPath, rawTarget)

	// 5. For compound states, resolve the initial child. resolveInitialAware
	//    additionally expands a parallel target into its encoded composite
	//    leaf-set.
	resolvedTarget, err := m.resolveInitialAware(targetPath, env)
	if err != nil {
		return TurnResult{}, fmt.Errorf("resolve initial for %q: %w", targetPath, err)
	}

	// emit machine.transition before applying effects
	m.logger.DebugContext(ctx, trace.EvMachineTransition,
		slog.String("from", string(cur)),
		slog.String("to", resolvedTarget),
		slog.String("intent", call.Intent),
	)

	// 6. Apply transition effects.
	newWorld, hostCalls, saySB, effectEvents, emits, err := m.applyEffectsTraced(ctx, winningTr.tr.Effects, w, env)
	if err != nil {
		return TurnResult{}, err
	}

	// 6b. Apply on_enter effects of the target state (and any entered ancestors).
	//
	// on_enter fires whenever a state is newly entered. "Newly entered"
	// means either:
	//   - resolvedTarget differs from cur (a real transition), OR
	//   - resolvedTarget equals cur but the author wrote an EXPLICIT
	//     target (i.e. rawTarget != "."), which is the "re-enter this
	//     same state with fresh on_enter" idiom used by the bugfix
	//     `refine:` arcs:
	//
	//       refine:
	//         - target: reproducing      # ← explicit, fires on_enter
	//           effects: { set: cycle++ }
	//
	//       look:
	//         - target: .                # ← "stay here", on_enter skipped
	//
	// Pre-2026-05-20 the test was just `resolvedTarget != cur`, which
	// silently swallowed the refine arc's intent: the user typed
	// `refine` in reproducing, the cycle counter bumped, but the agent
	// never re-fired and the artifact text stayed identical. The user
	// described it as "the refinement came back immediately and didn't
	// change the artifact text."
	isReEntry := resolvedTarget == string(cur) && strings.TrimSpace(rawTarget) != "." && strings.TrimSpace(rawTarget) != ""
	if resolvedTarget != string(cur) || isReEntry {
		var entered []string
		if isReEntry {
			// Self-reentry: the regular stateEnterPaths walks oldPath →
			// newPath and returns nothing when they're equal. Manually
			// list the state itself (plus any compound ancestors that
			// would be re-entered — same as the cur path).
			entered = []string{string(cur)}
		} else {
			entered = stateEnterPathsAware(string(cur), resolvedTarget)
		}
		for _, enteredPath := range entered {
			cs, ok := m.states[enteredPath]
			if !ok || cs.s == nil || len(cs.s.OnEnter) == 0 {
				continue
			}
			// Build an updated env with the latest world state.
			enterEnv := expr.Env{
				Slots: env.Slots,
				World: newWorld.Vars,
				Event: env.Event,
				Run:   env.Run,
			}
			newWorld2, enterHostCalls, enterSaySB, enterEffEvents, enterEmits, enterErr := m.applyEffectsTraced(ctx, cs.s.OnEnter, newWorld, enterEnv)
			if enterErr != nil {
				return TurnResult{}, fmt.Errorf("on_enter effects for %q: %w", enteredPath, enterErr)
			}
			newWorld = newWorld2
			hostCalls = append(hostCalls, enterHostCalls...)
			if enterSaySB.Len() > 0 {
				if saySB.Len() > 0 {
					saySB.WriteString("\n")
				}
				saySB.WriteString(enterSaySB.String())
			}
			effectEvents = append(effectEvents, enterEffEvents...)
			emits = append(emits, enterEmits...)
		}
	}

	// 8. Build event sequence (deferred view render to after emit chain
	//    so the view reflects the FINAL settled state):
	//    IntentAccepted → TransitionApplied → EffectApplied* → StateExited* → StateEntered*
	var events []store.Event

	events = append(events, newEvent(store.TransitionApplied, map[string]any{
		"from":   string(cur),
		"to":     resolvedTarget,
		"intent": call.Intent,
		// Persist the slot bag so replay-derived back-reference summaries
		// (orchestrator.extractRecentTurns) can re-surface the exact values
		// the user supplied — necessary for "yes — like I said before"
		// style anaphora on slotted intents like propose_purchase.
		"slots": map[string]any(call.Slots),
	}))

	events = append(events, effectEvents...)

	// Emit StateExited for each level of the old path that is not shared.
	exited := stateExitPathsAware(string(cur), resolvedTarget)
	for _, p := range exited {
		events = append(events, newEvent(store.StateExited, map[string]any{"state": p}))
	}

	// Emit StateEntered for each new level of the new path.
	entered := stateEnterPathsAware(string(cur), resolvedTarget)
	for _, p := range entered {
		events = append(events, newEvent(store.StateEntered, map[string]any{"state": p}))
	}

	// 6c. Dispatch any emit_intent: effects captured during the transition
	//     effects or on_enter chain. Each emitted intent fires a synthetic
	//     turn against the current resolved leaf, advancing the state path
	//     and appending events / host calls / say text into the SAME
	//     externally-initiated turn. Depth-capped at EmitIntentMaxDepth.
	finalState := resolvedTarget
	if len(emits) > 0 {
		// staged=false: pre-bind emit chains on the Turn path stay one-shot
		// in this slice. The staged gate-stop lives in the post-bind path
		// (DispatchPostBindEmits), which is where the docs-review / bugfix
		// decision emits actually fire (they gate on host-bound world keys).
		// Threading the run mode through Machine.Turn is deferred.
		ds, dw, dhc, dssb, devs, derr := m.dispatchEmittedIntents(ctx, finalState, newWorld, emits, env, 0, false, nil)
		if derr != nil {
			return TurnResult{}, derr
		}
		finalState = ds
		newWorld = dw
		hostCalls = append(hostCalls, dhc...)
		if dssb != "" {
			if saySB.Len() > 0 {
				saySB.WriteString("\n")
			}
			saySB.WriteString(dssb)
		}
		events = append(events, devs...)
	}

	// 7. Render view. When the target is parallel-encoded, compose region
	//    leaf views; otherwise normal single-state render. Use finalState
	//    so the view reflects the settled state after any emit_intent
	//    dispatches have transitioned further.
	//
	//    Render-after-bind contract: when the turn queued host calls with
	//    `bind:` directives, the orchestrator will re-render against the
	//    POST-bind world after dispatch (orchestrator.dispatchHostCalls →
	//    machine.RenderState).  Rendering here against the pre-bind world
	//    is both wasted work AND a footgun — view templates that read a
	//    bound field (e.g. `world.reproduction_artifact.summary_title`)
	//    would error against the still-nil snapshot and abort the turn,
	//    forcing authors to scatter `?? "(pending)"` defaults purely to
	//    survive a render the user will never see.  Skip the render in
	//    that case; the orchestrator owns the final view.
	var (
		renderedView   string
		typedView      *app.View
		typedRenderEnv expr.Env
		typedRenderer  *render.AppRenderer
	)
	if !hostCallsWillBind(hostCalls) {
		if par := parseParallel(finalState); par.IsParallel {
			// Parallel composition stays on the legacy single-string
			// path. Typed-view reflow for parallel composition is out
			// of scope for this pass — the parallel view stitches
			// multiple region views together at machine-time.
			renderedView, err = m.renderViewParallel(winningTr.tr, par.Root, sortedRegionNames(m.states[par.Root].s.States), finalState, newWorld, env, saySB.String(), false, finalState)
		} else {
			renderedView, typedView, typedRenderEnv, typedRenderer, err = m.renderViewWithTyped(winningTr.tr, finalState, newWorld, env, saySB.String())
		}
		if err != nil {
			return TurnResult{}, err
		}
	}

	// 9. Build menu for new state.  For parallel targets, union region menus.
	var newMenu []string
	if par := parseParallel(finalState); par.IsParallel {
		seen := make(map[string]struct{})
		for _, leaf := range par.RegionLeaves {
			for _, n := range m.allowedIntentNames(app.StatePath(leaf)) {
				seen[n] = struct{}{}
			}
		}
		newMenu = make([]string, 0, len(seen))
		for n := range seen {
			newMenu = append(newMenu, n)
		}
		sort.Strings(newMenu)
	} else {
		newMenu = m.allowedIntentNames(app.StatePath(finalState))
	}

	return TurnResult{
		NewState:  app.StatePath(finalState),
		World:     newWorld,
		View:      renderedView,
		Menu:      newMenu,
		Events:    events,
		HostCalls: hostCalls,
		TypedView: typedView,
		RenderEnv: typedRenderEnv,
		Renderer:  typedRenderer,
	}, nil
}

// dispatchEmittedIntents walks a list of synthetic intents captured by
// emit_intent: effects and applies each one against curState as a
// self-loop within the same turn. Each dispatch:
//
//   - Validates the intent is allowed in the current state (we walk
//     the on: arcs ourselves rather than via Machine.Validate so we
//     can stay inside the machine's transition apparatus and not
//     emit ValidationFailed events for the synthetic intent — a
//     dropped emit is an authoring bug surfaced by depth-cap / no-arc
//     errors, not a user-level validation rejection).
//   - Runs findTransitionTraced to pick the winning transition arm.
//   - Applies the transition's effects (which may queue further emits).
//   - Resolves the target leaf via resolveInitialAware.
//   - Runs on_enter effects of newly-entered ancestors (recursive emit
//     chain via depth+1).
//   - Emits TransitionApplied / EffectApplied / HostInvoked /
//     StateExited / StateEntered events the same way Turn does.
//
// Depth is bounded by EmitIntentMaxDepth — exceeding it surfaces an
// error trace event (trace.EvIntentEmitDepthCap) and returns an error
// so the surrounding Turn fails loud rather than looping silently.
// onRoomEnterFn, when non-nil, is invoked once per synthetic hop with the
// room just entered and the say-text that hop produced (transition say +
// the entered room's on_enter say). It is the seam the orchestrator uses to
// stream per-room progress breadcrumbs LIVE during a one-shot chain instead
// of merging all say into one blob prepended to the final view. Fired
// synchronously; implementations MUST NOT block (the TUI sink fans out via
// tea.Program.Send).
type onRoomEnterFn func(state string, say string)

func (m *machineImpl) dispatchEmittedIntents(ctx context.Context, curState string, w world.World, emits []emittedIntent, parentEnv expr.Env, depth int, staged bool, onEnter onRoomEnterFn) (string, world.World, []HostInvocation, string, []store.Event, error) {
	if depth >= EmitIntentMaxDepth {
		m.logger.DebugContext(ctx, trace.EvIntentEmitDepthCap,
			slog.Int("depth", depth),
			slog.String("state", curState),
		)
		return "", world.World{}, nil, "", nil, fmt.Errorf("emit_intent: dispatch exceeded max depth (%d) at state %q — likely a cyclic emit chain", EmitIntentMaxDepth, curState)
	}

	// Gate check: a phase boundary ends the turn. If the chain has reached
	// a multi-way decision gate that owes a human decision (staged mode, or
	// a `decider: human` pin), stop BEFORE firing this state's emits —
	// drop them, rest here, and record why. One-shot mode (and `decider:
	// llm` pins) skip this and advance as before.
	//
	// Origin-state (depth==0) exception, scoped to internal auto-routes: the
	// origin is the state *dispatching* these emits (e.g. a router room's
	// post-bind `emit_intent: "{{ world.route }}"`), not a state the chain has
	// arrived at. A post-bind emit that resolves to a `hidden:` intent —
	// on_branch/on_main and friends, which no operator ever types — is
	// deterministic routing off a host-bound fact, so it must fire even in
	// staged mode (issue #15: once: on_enter host.run bind+emit was dropped).
	// But a post-bind emit to a FIRST-CLASS operator action (e.g. cherny-loop
	// baseline's `proceed`, shown alongside reconfigure/accept) IS a decision a
	// human owes, so the origin gate still stands. Distinguishing on the
	// emitted intent's `hidden:` flag is what lets git-ops idle auto-route while
	// importer-gate's baseline gates. depth>0 (arrived-at) states always gate.
	if (depth > 0 || !m.allEmitsHiddenRoutes(curState, emits)) &&
		m.isStagedGate(ctx, curState, w, staged) {
		m.logger.DebugContext(ctx, trace.EvIntentEmitted,
			slog.String("state", curState),
			slog.String("kind", "staged_gate_stop"),
			slog.Int("depth", depth),
		)
		gateEv := newEvent(store.GateDecided, map[string]any{
			"state":             curState,
			"available_intents": m.allowedIntentNames(app.StatePath(curState)),
			"decider":           "human",
			"chosen_intent":     "",
			"bailed_to_human":   true,
		})
		return curState, w, nil, "", []store.Event{gateEv}, nil
	}

	state := curState
	newWorld := w
	var hostCalls []HostInvocation
	var saySB strings.Builder
	var events []store.Event

	// Record an auto-advance through a real decision gate: when a guarded
	// emit_intent (a conditional default) fires at a state that IS a decision
	// gate, the engine just made the decision deterministically. Recording it
	// keeps the "every decision is a labeled datapoint" invariant. Trivial
	// single-intent / non-gate advances are NOT recorded (no decision existed).
	if len(emits) > 0 && m.isDecisionGate(ctx, curState, w) {
		events = append(events, newEvent(store.GateDecided, map[string]any{
			"state":             curState,
			"available_intents": m.allowedIntentNames(app.StatePath(curState)),
			"decider":           "default",
			"chosen_intent":     emits[0].Name,
			"bailed_to_human":   false,
		}))
	}

	for _, emit := range emits {
		// Build the dispatch env so guards / templates in the
		// destination's effects see the synthetic call's slots and
		// the up-to-date world. Run carries over from the parent.
		dispEnv := expr.Env{
			Slots: emit.Slots,
			World: newWorld.Vars,
			Event: make(map[string]any),
			Run:   parentEnv.Run,
		}

		m.logger.DebugContext(ctx, trace.EvIntentEmitted,
			slog.String("intent", emit.Name),
			slog.String("state", state),
			slog.Int("depth", depth+1),
		)

		// Parallel-encoded paths are not supported as the *origin* of
		// an emit_intent dispatch in this initial implementation —
		// parallel.go's region semantics ride a separate event-bus
		// (the propagateEmits path) and mixing the two would muddle
		// the depth-cap accounting. The common authoring shape (a
		// state's on_enter auto-firing a verdict) is unaffected.
		//
		// Behaviour: log + drop the offending emit and continue with
		// the remaining ones (rather than aborting the whole chain).
		// Three sites disagreed historically — this one used to error
		// loudly, parallel.turnParallel warn-logged silently, and
		// DispatchPostBindEmits silently no-op'd. All three now emit
		// EvIntentEmitParallelDropped through the trace logger and
		// return no error so an otherwise-valid story is not bricked.
		if parseParallel(state).IsParallel {
			m.logger.WarnContext(ctx, trace.EvIntentEmitParallelDropped,
				slog.String("site", "dispatch_emitted_intents"),
				slog.String("intent", emit.Name),
				slog.String("state", state),
			)
			continue
		}

		// Resolve the emitted name through the import alias map of the
		// active state's ancestor chain. When the LLM-judge emits a
		// bare intent name (e.g. `accept`) inside an imported child
		// (e.g. active state `core.bf.reproducing_awaiting_reply`),
		// the loader's rewriter renamed the corresponding on: arc to
		// `core__bf__accept`. resolveEmittedIntentName walks the leaf
		// → root ancestor chain looking for an IntentAliases entry
		// that maps `accept` to its renamed form. Returns the bare
		// name when no mapping applies (standalone stories — back
		// compat).
		dispatchName := m.resolveEmittedIntentName(state, emit.Name)
		if dispatchName != emit.Name {
			m.logger.DebugContext(ctx, trace.EvIntentEmitted,
				slog.String("intent", emit.Name),
				slog.String("resolved", dispatchName),
				slog.String("state", state),
				slog.String("kind", "import_alias_resolved"),
			)
		}

		winningTr, winningPath, _, err := m.findTransitionTraced(ctx, state, dispatchName, dispEnv)
		if err != nil {
			return "", world.World{}, nil, "", nil, fmt.Errorf("emit_intent %q at %q: find transition: %w", emit.Name, state, err)
		}
		if winningTr == nil {
			return "", world.World{}, nil, "", nil, fmt.Errorf("emit_intent %q at %q: no transition arm matched (intent has no on: handler, or all guards failed)", emit.Name, state)
		}

		// Resolve target.
		rawTarget := winningTr.tr.Target
		if strings.Contains(rawTarget, "{{") {
			rendered, renderErr := expr.Render(rawTarget, dispEnv)
			if renderErr != nil {
				return "", world.World{}, nil, "", nil, fmt.Errorf("emit_intent %q render target %q: %w", emit.Name, rawTarget, renderErr)
			}
			rawTarget = strings.TrimSpace(rendered)
		}
		targetPath := resolveTarget(winningPath, rawTarget)
		resolvedTarget, err := m.resolveInitialAware(targetPath, dispEnv)
		if err != nil {
			return "", world.World{}, nil, "", nil, fmt.Errorf("emit_intent %q resolve initial for %q: %w", emit.Name, targetPath, err)
		}

		// Apply transition effects.
		nw2, hc2, sb2, ev2, em2, applyErr := m.applyEffectsTraced(ctx, winningTr.tr.Effects, newWorld, dispEnv)
		if applyErr != nil {
			return "", world.World{}, nil, "", nil, fmt.Errorf("emit_intent %q apply effects: %w", emit.Name, applyErr)
		}
		newWorld = nw2
		hostCalls = append(hostCalls, hc2...)
		// hopSay accumulates the say-text for THIS hop only (transition say
		// + the entered room's on_enter say) so onEnter can stream it as one
		// per-room breadcrumb. The recursion below streams its own hops.
		var hopSay strings.Builder
		if sb2.Len() > 0 {
			if saySB.Len() > 0 {
				saySB.WriteString("\n")
			}
			saySB.WriteString(sb2.String())
			hopSay.WriteString(sb2.String())
		}

		// Fire on_enter of newly-entered ancestors. Mirror Turn's logic.
		var enterEmits []emittedIntent
		if resolvedTarget != state {
			entered := stateEnterPathsAware(state, resolvedTarget)
			for _, enteredPath := range entered {
				cs, ok := m.states[enteredPath]
				if !ok || cs.s == nil || len(cs.s.OnEnter) == 0 {
					continue
				}
				enterEnv := expr.Env{Slots: dispEnv.Slots, World: newWorld.Vars, Event: dispEnv.Event, Run: dispEnv.Run}
				nw3, hc3, sb3, ev3, em3, eErr := m.applyEffectsTraced(ctx, cs.s.OnEnter, newWorld, enterEnv)
				if eErr != nil {
					return "", world.World{}, nil, "", nil, fmt.Errorf("emit_intent %q on_enter %q: %w", emit.Name, enteredPath, eErr)
				}
				newWorld = nw3
				hostCalls = append(hostCalls, hc3...)
				if sb3.Len() > 0 {
					if saySB.Len() > 0 {
						saySB.WriteString("\n")
					}
					saySB.WriteString(sb3.String())
					if hopSay.Len() > 0 {
						hopSay.WriteString("\n")
					}
					hopSay.WriteString(sb3.String())
				}
				ev2 = append(ev2, ev3...)
				enterEmits = append(enterEmits, em3...)
			}
		}

		// Stream this hop's say live as a per-room breadcrumb.
		if onEnter != nil && hopSay.Len() > 0 {
			onEnter(resolvedTarget, hopSay.String())
		}

		// Build event sequence for this synthetic transition.
		slotBag := make(world.Slots, len(emit.Slots))
		for k, v := range emit.Slots {
			slotBag[k] = v
		}
		events = append(events, newEvent(store.TransitionApplied, map[string]any{
			"from":      state,
			"to":        resolvedTarget,
			"intent":    dispatchName,
			"slots":     map[string]any(slotBag),
			"synthetic": true,
		}))
		events = append(events, ev2...)
		for _, p := range stateExitPathsAware(state, resolvedTarget) {
			events = append(events, newEvent(store.StateExited, map[string]any{"state": p}))
		}
		for _, p := range stateEnterPathsAware(state, resolvedTarget) {
			events = append(events, newEvent(store.StateEntered, map[string]any{"state": p}))
		}

		// Combine all post-chain emits (transition + on_enter) for
		// recursion. enterEmits is already in execution order behind
		// em2 (transition emits → on_enter emits).
		chainedEmits := append([]emittedIntent{}, em2...)
		chainedEmits = append(chainedEmits, enterEmits...)

		state = resolvedTarget

		if len(chainedEmits) > 0 {
			subState, subWorld, subHC, subSay, subEvs, subErr := m.dispatchEmittedIntents(ctx, state, newWorld, chainedEmits, parentEnv, depth+1, staged, onEnter)
			if subErr != nil {
				return "", world.World{}, nil, "", nil, subErr
			}
			state = subState
			newWorld = subWorld
			hostCalls = append(hostCalls, subHC...)
			if subSay != "" {
				if saySB.Len() > 0 {
					saySB.WriteString("\n")
				}
				saySB.WriteString(subSay)
			}
			events = append(events, subEvs...)
		}
	}

	return state, newWorld, hostCalls, saySB.String(), events, nil
}

// resolveEmittedIntentName resolves a bare intent name emitted by an
// LLM-judge into the import-alias-rewritten form that the runtime's
// on: arc map actually keys on at the current state (or anywhere up
// its ancestor chain).
//
// Background. The imports loader's rewriter renames every child
// state's `on:` arc keys from `<intent>` to `<alias>__<intent>` (and
// stacks the prefix on multi-layer folds — `<outer>__<inner>__…__<intent>`).
// The same intent is emitted by the LLM judge VERBATIM as the bare
// name — there's no way for the judge prompt to know which alias
// chain currently wraps the active state. Without this resolution
// step the dispatcher's findTransition lookup on the bare name
// silently no-ops inside import compounds.
//
// Resolution strategy. The rewriter records the rename mapping on
// each child state in `IntentAliases` (see imports_rewriter.go). At
// dispatch time we walk the active state's ancestor chain leaf →
// root, consulting each state's alias map. The first hit wins. When
// no ancestor knows about the name, the bare name is returned —
// preserving the existing behaviour for standalone (un-imported)
// stories.
//
// Why ancestor walk. The rewriter populates IntentAliases on every
// rewritten state; the leaf is the canonical site, but some authoring
// shapes (`on:` handler declared on a wrapping compound, not on the
// leaf) need the walk to surface the alias map from one level up.
// Mirrors findTransitionTraced's leaf-to-root walk so the two stay in
// lock-step.
func (m *machineImpl) resolveEmittedIntentName(leafPath, name string) string {
	if name == "" {
		return name
	}
	path := leafPath
	for {
		cs, ok := m.states[path]
		if ok && cs.s != nil && len(cs.s.IntentAliases) > 0 {
			if mapped, found := cs.s.IntentAliases[name]; found && mapped != "" {
				return mapped
			}
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
	return name
}

// findTransitionTraced is findTransition with trace.EvMachineGuardEval / Winner emission.
func (m *machineImpl) findTransitionTraced(ctx context.Context, leafPath, intentName string, env expr.Env) (*compiledTransition, string, string, error) {
	path := leafPath
	for {
		cs, ok := m.states[path]
		if ok {
			handlers := cs.on[intentName]
			if len(handlers) > 0 {
				ct, hint, err := m.evaluateArmsTraced(ctx, handlers, env, path, intentName)
				if err != nil {
					return nil, "", "", err
				}
				if ct != nil {
					return ct, path, "", nil
				}
				return nil, path, hint, nil
			}
			wildcardHandlers := cs.on["*"]
			if len(wildcardHandlers) > 0 {
				ct, hint, err := m.evaluateArmsTraced(ctx, wildcardHandlers, env, path, "*")
				if err != nil {
					return nil, "", "", err
				}
				if ct != nil {
					return ct, path, "", nil
				}
				return nil, path, hint, nil
			}
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
	return nil, leafPath, "", nil
}

// evaluateArmsTraced is evaluateArms with guard.eval / guard.winner trace events.
func (m *machineImpl) evaluateArmsTraced(ctx context.Context, arms []compiledTransition, env expr.Env, statePath, intentName string) (*compiledTransition, string, error) {
	hint := ""
	for i := range arms {
		arm := &arms[i]
		if arm.tr.Default {
			m.logger.DebugContext(ctx, trace.EvMachineGuardEval,
				slog.String("expr", "default"),
				slog.String("state", statePath),
				slog.String("intent", intentName),
				slog.Bool("result", true),
			)
			m.logger.DebugContext(ctx, trace.EvMachineGuardWinner,
				slog.String("expr", "default"),
				slog.String("target", resolveTarget(statePath, arm.tr.Target)),
			)
			return arm, "", nil
		}
		if arm.guard == nil {
			m.logger.DebugContext(ctx, trace.EvMachineGuardEval,
				slog.String("expr", "(no guard)"),
				slog.String("state", statePath),
				slog.String("intent", intentName),
				slog.Bool("result", true),
			)
			m.logger.DebugContext(ctx, trace.EvMachineGuardWinner,
				slog.String("expr", "(no guard)"),
				slog.String("target", resolveTarget(statePath, arm.tr.Target)),
			)
			return arm, "", nil
		}
		ok, err := expr.EvalBool(arm.guard, env)
		if err != nil {
			m.logger.DebugContext(ctx, trace.EvMachineGuardEval,
				slog.String("expr", arm.guard.Source()),
				slog.String("state", statePath),
				slog.String("intent", intentName),
				slog.String("error", err.Error()),
			)
			return nil, "", err
		}
		m.logger.DebugContext(ctx, trace.EvMachineGuardEval,
			slog.String("expr", arm.guard.Source()),
			slog.String("state", statePath),
			slog.String("intent", intentName),
			slog.Bool("result", ok),
		)
		if ok {
			m.logger.DebugContext(ctx, trace.EvMachineGuardWinner,
				slog.String("expr", arm.guard.Source()),
				slog.String("target", resolveTarget(statePath, arm.tr.Target)),
			)
			return arm, "", nil
		}
		if hint == "" && arm.tr.GuardHint != "" {
			hint = arm.tr.GuardHint
		}
	}
	return nil, hint, nil
}

// emittedIntent is one synthetic-intent dispatch captured while walking
// an effect chain. The intent name + slot values are resolved against
// the world AT THE TIME the effect fired (post any preceding set: in
// the same chain). The machine's emit_intent dispatcher consumes a
// slice of these after the chain completes and walks each one
// sequentially as a self-loop within the same turn.
type emittedIntent struct {
	Name  string
	Slots map[string]any
}

// applyEffectsTraced is applyEffects with machine.effect.applied trace events.
// It additionally collects emit_intent: effects into a slice of
// emittedIntent records; the surrounding Turn / on_enter logic
// dispatches them after the chain completes.
// Returns saySB as *strings.Builder, not a value: a strings.Builder must never
// be copied after its first write (it self-references its own address; copying
// then writing panics with "illegal use of non-zero Builder copied by value").
// Callers append more say text onto the returned builder, so a value return
// would be a latent panic the moment a transition effect emits a non-empty
// `say:` and the on_enter / emit-dispatch chain writes more onto it.
func (m *machineImpl) applyEffectsTraced(ctx context.Context, effects []app.Effect, w world.World, env expr.Env) (world.World, []HostInvocation, *strings.Builder, []store.Event, []emittedIntent, error) {
	return m.applyEffectsTracedWithOptions(ctx, effects, w, env, RunEffectsOptions{})
}

func (m *machineImpl) applyEffectsTracedWithOptions(ctx context.Context, effects []app.Effect, w world.World, env expr.Env, opts RunEffectsOptions) (world.World, []HostInvocation, *strings.Builder, []store.Event, []emittedIntent, error) {
	newWorld := cloneWorld(w)
	var hostCalls []HostInvocation
	saySB := &strings.Builder{}
	var effectEvents []store.Event
	var emits []emittedIntent

	for _, eff := range effects {
		// Optional per-effect guard. An effect whose
		// `when:` expression evaluates false is silently skipped so
		// authors can branch on_enter chains on world flags (e.g.
		// `when: world.narration` vs `when: not world.narration`)
		// without restructuring states into compound shapes. The
		// guard sees the post-prior-effect world via env, so an
		// earlier `set:` in the same chain can steer a later
		// branch — symmetric with the host-call rerender semantics.
		if strings.TrimSpace(eff.When) != "" {
			env.World = newWorld.Vars
			prog, cerr := expr.CompileBool(eff.When)
			if cerr != nil {
				return world.World{}, nil, saySB, nil, nil, fmt.Errorf("effect when %q: compile: %w", eff.When, cerr)
			}
			ok, eerr := expr.EvalBool(prog, env)
			if eerr != nil {
				// emit_intent effects routinely reference world keys that
				// only get populated by host-call binds (e.g. the bugfix
				// story's `world.llm_verdict.confidence`, bound by
				// host.agent.ask_with_mcp later in the same on_enter
				// chain). Binds happen at orchestrator-dispatch time, not
				// machine-time, so the When eval can error against nil
				// references here even though the post-bind eval would
				// succeed. For emit_intent specifically, we DEFER the
				// effect silently — the orchestrator's
				// DispatchPostBindEmits pass re-evaluates against the
				// post-bind world. For other effects (set/say/invoke)
				// the error is a real authoring bug — propagate it.
				if eff.EmitIntent != "" {
					m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
						slog.String("type", "emit_intent_deferred"),
						slog.String("when", eff.When),
						slog.String("reason", eerr.Error()),
					)
					continue
				}
				return world.World{}, nil, saySB, nil, nil, fmt.Errorf("effect when %q: eval: %w", eff.When, eerr)
			}
			if !ok {
				m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
					slog.String("type", "skip"),
					slog.String("when", eff.When),
				)
				continue
			}
		}
		switch {
		case len(eff.Set) > 0:
			// Freeze the env for the whole block so every key in one `set:`
			// renders against the SAME pre-block world. This makes a single `set:`
			// map atomic and order-independent (its keys are a Go map → unordered),
			// while cross-entry order stays progressive: the next effect entry sees
			// these writes because we commit them before moving on.
			blockEnv := env
			blockEnv.World = newWorld.Vars // pre-block snapshot (read-only during render)
			keys := make([]string, 0, len(eff.Set))
			for k := range eff.Set {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			resolvedSet := make(map[string]any, len(eff.Set))
			for _, k := range keys {
				resolved, err := resolveEffectValue(eff.Set[k], blockEnv, newWorld)
				if err != nil {
					return world.World{}, nil, saySB, nil, nil, fmt.Errorf("effect set %q: %w", k, err)
				}
				resolvedSet[k] = resolved
			}
			for _, k := range keys {
				before := newWorld.Vars[k]
				newWorld.Vars[k] = m.coerceSetValue(k, resolvedSet[k])
				m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
					slog.String("type", "set"),
					slog.String("key", k),
					slog.Any("before", before),
					slog.Any("after", resolvedSet[k]),
				)
				effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
					"set": map[string]any{k: resolvedSet[k]},
				}))
			}
			env.World = newWorld.Vars // expose this block's commits to the next entry

		case len(eff.Increment) > 0:
			for k, delta := range eff.Increment {
				cur := toInt64(newWorld.Vars[k])
				newWorld.Vars[k] = cur + int64(delta)
				env.World = newWorld.Vars
				m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
					slog.String("type", "increment"),
					slog.String("key", k),
					slog.Int64("delta", int64(delta)),
					slog.Int64("after", cur+int64(delta)),
				)
				effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
					"increment": map[string]any{k: delta},
				}))
			}

		case eff.Say != "":
			text, err := render.Pongo(eff.Say, env)
			if err != nil {
				return world.World{}, nil, saySB, nil, nil, fmt.Errorf("effect say: %w", err)
			}
			if saySB.Len() > 0 {
				saySB.WriteString("\n")
			}
			saySB.WriteString(text)
			m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
				slog.String("type", "say"),
				slog.String("text", text),
			)
			// narration is its own event kind so
			// world.update means only a world mutation. Payload key is `text`.
			effectEvents = append(effectEvents, newEvent(store.MachineSay, map[string]any{
				"text": text,
			}))

		case eff.Invoke != "":
			// once: idempotent re-entry guard. When every bind target world
			// key is already SET (non-empty), the invoke is a no-op — its
			// result is already cached in world, so re-entry (/reload,
			// self-transition, on_error) re-renders from the cache instead of
			// recomputing an expensive, non-idempotent call. Skipping does NOT
			// abort the rest of the on_enter chain; the engine continues to the
			// next effect. The skip is recorded on EffectApplied so a trace
			// shows the elision and why. See app.Effect.Once.
			if eff.Once && !opts.ForceOnce && allBindTargetsSet(eff.Bind, newWorld.Vars) {
				m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
					slog.String("type", "invoke"),
					slog.String("namespace", eff.Invoke),
					slog.String("skipped", "cached"),
				)
				effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
					"namespace": eff.Invoke,
					"skipped":   "cached",
				}))
				continue
			}
			// Resolve with: args (templated values).  `resolvedArgs` is the
			// best-effort up-front resolution against the world snapshot at
			// machine-time; the orchestrator re-renders RawWith using the
			// post-bind world before each invocation, so a downstream step
			// in the same `on_enter:` can see an earlier step's binds.
			resolvedArgs := make(map[string]any, len(eff.With))
			for k, v := range eff.With {
				resolved, err := resolveEffectValue(v, env, newWorld)
				if err != nil {
					return world.World{}, nil, saySB, nil, nil, fmt.Errorf("effect invoke %q with %q: %w", eff.Invoke, k, err)
				}
				resolvedArgs[k] = resolved
			}
			// Snapshot the raw `with:` block so dispatch can re-render it.
			rawWith := make(map[string]any, len(eff.With))
			for k, v := range eff.With {
				rawWith[k] = v
			}
			// Thread the author-assigned call-site id (effect-level `id:`) into
			// the args under the reserved `call` key, so flow stubs (`by_call:`)
			// and cassettes (`match: { call: <id> }`) can address two calls that
			// share a handler name. A plain string with no template, so the
			// dispatch-time re-render of rawWith returns it unchanged. Mirrors
			// the `op` arg that `by_op:` keys on.
			if eff.Id != "" {
				resolvedArgs["call"] = eff.Id
				rawWith["call"] = eff.Id
			}
			// Snapshot the world as of THIS invoke's position so the
			// orchestrator can re-render RawWith without a later `set:` in
			// the same chain leaking back into these args. See
			// HostInvocation.WorldSnapshot.
			worldSnapshot := make(map[string]any, len(newWorld.Vars))
			for k, v := range newWorld.Vars {
				worldSnapshot[k] = v
			}
			hc := HostInvocation{
				Namespace:     eff.Invoke,
				Args:          resolvedArgs,
				RawWith:       rawWith,
				Env:           env,
				WorldSnapshot: worldSnapshot,
				Bind:          eff.Bind,
				OnError:       eff.OnError,
				EmitEvent:     eff.Emit,
				Background:    eff.Background,
				OnComplete:    eff.OnComplete,
				AgentPlugin:   eff.AgentPlugin,
			}
			hostCalls = append(hostCalls, hc)
			m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
				slog.String("type", "invoke"),
				slog.String("namespace", eff.Invoke),
			)
			effectEvents = append(effectEvents, newEvent(store.HostInvoked, map[string]any{
				"namespace":  eff.Invoke,
				"args":       resolvedArgs,
				"background": eff.Background,
			}))
		}

		// emit_intent: capture the resolved intent name + slot values for
		// post-chain dispatch. Template values are rendered against the
		// world AT THE TIME the effect fires (post any preceding set: in
		// the same chain). The actual dispatch happens once the entire
		// effect chain (and any on_enter chain in the same call) has
		// applied — see Turn / on_enter call sites for the dispatch
		// invocation.
		if eff.EmitIntent != "" {
			env.World = newWorld.Vars
			rendered := eff.EmitIntent
			if strings.Contains(rendered, "{{") {
				out, rerr := expr.Render(rendered, env)
				if rerr != nil {
					return world.World{}, nil, saySB, nil, nil, fmt.Errorf("effect emit_intent: render %q: %w", eff.EmitIntent, rerr)
				}
				rendered = strings.TrimSpace(out)
			}
			if rendered == "" {
				// Treat empty-after-render as a no-op rather than an
				// error — authors can guard with `when:` to make this
				// explicit (the bugfix story does so on judge confidence).
				m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
					slog.String("type", "emit_intent_skipped"),
					slog.String("source", eff.EmitIntent),
				)
			} else {
				resolvedSlots := make(map[string]any, len(eff.EmitSlots))
				for k, v := range eff.EmitSlots {
					rv, rerr := resolveEffectValue(v, env, newWorld)
					if rerr != nil {
						return world.World{}, nil, saySB, nil, nil, fmt.Errorf("effect emit_intent %q slot %q: %w", rendered, k, rerr)
					}
					resolvedSlots[k] = rv
				}
				emits = append(emits, emittedIntent{Name: rendered, Slots: resolvedSlots})
				m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
					slog.String("type", "emit_intent"),
					slog.String("intent", rendered),
				)
			}
		}
	}
	return newWorld, hostCalls, saySB, effectEvents, emits, nil
}

// resolveTarget resolves a transition target relative to its owning state path.
// Handles: "." (self), ".." relative refs, absolute refs.
func resolveTarget(statePath, target string) string {
	if target == "" || target == "." {
		return statePath
	}
	if !strings.HasPrefix(target, "..") {
		// Absolute reference; normalise slashes to dots.
		return strings.ReplaceAll(target, "/", ".")
	}
	// Relative reference.
	parts := strings.Split(statePath, ".")
	segs := strings.Split(target, "/")
	for _, seg := range segs {
		switch seg {
		case "..":
			if len(parts) > 0 {
				parts = parts[:len(parts)-1]
			}
		case ".", "":
			// skip
		default:
			parts = append(parts, seg)
		}
	}
	return strings.Join(parts, ".")
}

// resolveInitial resolves a (possibly compound) state path to its leaf,
// following initial: expressions recursively.
func (m *machineImpl) resolveInitial(path string, env expr.Env) (string, error) {
	cs, ok := m.states[path]
	if !ok {
		return path, nil
	}
	if cs.s.Type != "compound" || cs.s.Initial == "" {
		return path, nil
	}
	// Evaluate initial expression.
	childName, err := render.Pongo(cs.s.Initial, env)
	if err != nil {
		return "", fmt.Errorf("evaluate initial expression %q: %w", cs.s.Initial, err)
	}
	childName = strings.TrimSpace(childName)
	childPath := joinStatePath(path, childName)
	// Recurse in case the child is itself a compound state.
	return m.resolveInitial(childPath, env)
}

// DispatchPostBindEmits — see Machine interface doc-comment.
//
// Walks the leaf state's on_enter chain (parallel-encoded paths are
// unsupported and return inputs unchanged, mirroring the rest of the
// emit_intent dispatch path). For each effect that declares
// emit_intent: (with its optional When: guard), evaluates the guard
// against the post-bind world and dispatches the synthetic intent via
// the shared dispatchEmittedIntents pipeline.
func (m *machineImpl) DispatchPostBindEmits(ctx context.Context, state app.StatePath, w world.World, staged bool, onEnter onRoomEnterFn) (app.StatePath, world.World, []HostInvocation, string, []store.Event, error) {
	if par := parseParallel(string(state)); par.IsParallel {
		// Parallel-encoded paths can't host emit_intent dispatch
		// (parallel.go's region semantics ride a separate event-bus
		// — see dispatchEmittedIntents for the long-form rationale).
		// Surface the no-op via the trace logger so operators get a
		// signal that a queued emit was discarded; this matches the
		// log+drop shape used at the other two emit-rejection sites.
		// Walk the parallel parent's on_enter AND each region leaf's
		// on_enter for any emit_intent and log one event per finding.
		logDroppedEmit := func(statePath string, effs []app.Effect) {
			for _, eff := range effs {
				if eff.EmitIntent != "" {
					m.logger.WarnContext(ctx, trace.EvIntentEmitParallelDropped,
						slog.String("site", "dispatch_post_bind_emits"),
						slog.String("intent", eff.EmitIntent),
						slog.String("state", statePath),
					)
				}
			}
		}
		if cs, ok := m.states[par.Root]; ok && cs.s != nil {
			logDroppedEmit(string(state), cs.s.OnEnter)
		}
		for _, leaf := range par.RegionLeaves {
			if cs, ok := m.states[leaf]; ok && cs.s != nil {
				logDroppedEmit(string(state), cs.s.OnEnter)
			}
		}
		return state, w, nil, "", nil, nil
	}
	cs, ok := m.states[string(state)]
	if !ok || cs.s == nil || len(cs.s.OnEnter) == 0 {
		return state, w, nil, "", nil, nil
	}

	// Build an env that sees the post-bind world.
	env := expr.Env{
		Slots: map[string]any{},
		World: w.Vars,
		Event: map[string]any{},
	}

	var emits []emittedIntent
	for _, eff := range cs.s.OnEnter {
		if eff.EmitIntent == "" {
			continue
		}
		if strings.TrimSpace(eff.When) != "" {
			prog, cerr := expr.CompileBool(eff.When)
			if cerr != nil {
				return state, w, nil, "", nil, fmt.Errorf("post-bind emit_intent when %q: compile: %w", eff.When, cerr)
			}
			ok, eerr := expr.EvalBool(prog, env)
			if eerr != nil {
				// A guard that errors against the post-bind world is
				// (almost always) an authoring bug — the loader's
				// when-eval check doesn't run pre-Turn either. Surface
				// it so authors catch the case where the LLM-judge
				// didn't bind at all and the guard reads through nil.
				return state, w, nil, "", nil, fmt.Errorf("post-bind emit_intent when %q: eval: %w", eff.When, eerr)
			}
			if !ok {
				continue
			}
		}
		rendered := eff.EmitIntent
		if strings.Contains(rendered, "{{") {
			out, rerr := expr.Render(rendered, env)
			if rerr != nil {
				return state, w, nil, "", nil, fmt.Errorf("post-bind emit_intent: render %q: %w", eff.EmitIntent, rerr)
			}
			rendered = strings.TrimSpace(out)
		}
		if rendered == "" {
			continue
		}
		slots := make(map[string]any, len(eff.EmitSlots))
		for k, v := range eff.EmitSlots {
			rv, rerr := resolveEffectValue(v, env, w)
			if rerr != nil {
				return state, w, nil, "", nil, fmt.Errorf("post-bind emit_intent %q slot %q: %w", rendered, k, rerr)
			}
			slots[k] = rv
		}
		emits = append(emits, emittedIntent{Name: rendered, Slots: slots})
	}

	if len(emits) == 0 {
		return state, w, nil, "", nil, nil
	}

	finalState, finalWorld, hostCalls, sayText, events, derr := m.dispatchEmittedIntents(ctx, string(state), w, emits, env, 0, staged, onEnter)
	if derr != nil {
		return state, w, nil, "", nil, derr
	}
	return app.StatePath(finalState), finalWorld, hostCalls, sayText, events, nil
}

// allEmitsHiddenRoutes reports whether EVERY emit in `emits` resolves to an
// intent declared `hidden:` — an internal auto-route (a router room's
// `emit_intent: "{{ world.route }}"` → on_branch/on_main, never operator-typed)
// rather than a first-class operator action. Such a post-bind emit is a
// deterministic consequence of a host-bound fact, so the staged gate must let
// it fire even at the origin state; a single non-hidden emit (e.g. cherny-loop
// baseline's `proceed`) means a real decision is pending and the gate stands.
// Aliases are resolved first so the lookup works across an import boundary
// (e.g. `proceed` → `maker__proceed`). An empty list or an unresolvable name is
// treated as NOT a pure route (false), so the gate is preserved by default.
func (m *machineImpl) allEmitsHiddenRoutes(state string, emits []emittedIntent) bool {
	if len(emits) == 0 {
		return false
	}
	for _, e := range emits {
		name := m.resolveEmittedIntentName(state, e.Name)
		def, ok := m.lookupIntent(app.StatePath(state), name)
		if !ok || !def.Hidden {
			return false
		}
	}
	return true
}

// isDecisionGate reports whether `state` represents a genuine branch the
// operator must choose between — as opposed to deterministic outcome
// routing the engine resolves on its own.
//
// The rule: a state is a decision gate when it has at least one currently-
// available forward intent (arm advances to a different, non-`@exit` state)
// that is NOT itself the target of one of the state's `emit_intent:`
// auto-advances. Such an intent is reachable ONLY by operator/decider
// input, so leaving it to auto-advance would silently make a choice.
//
// Worked examples (docs-review):
//   - reviewing: forward intents {done, no_submit} are BOTH emit_intent
//     targets (success/failure outcome routing) → not a gate → auto-advance.
//   - reviewed:  forward intents {fix_docs, review_again}; only fix_docs is
//     an emit target, review_again is operator-only → gate → staged stops.
//   - fixing:    forward {fix_done} is an emit target → not a gate.
//
// A templated emit name (e.g. `emit_intent: "{{ world.verdict.intent }}"`,
// the bugfix LLM-judge shape) matches no static intent, so every real
// forward intent stays "operator-only" → the *_awaiting_reply room is a
// gate. That is correct: in staged mode a human decides; in one-shot mode
// this check is skipped and the templated emit fires.
func (m *machineImpl) isDecisionGate(ctx context.Context, state string, w world.World) bool {
	cs, ok := m.states[state]
	if !ok || cs.s == nil {
		return false
	}
	emitTargets := make(map[string]struct{}, len(cs.s.OnEnter))
	for _, eff := range cs.s.OnEnter {
		if n := strings.TrimSpace(eff.EmitIntent); n != "" {
			// Under an import, the on_enter `emit_intent:` value stays the
			// bare child name (e.g. `mark_achieved`) while the state's `on:`
			// arc keys — what allowedIntentNames returns — are rewritten to
			// the aliased form (`maker__mark_achieved`). Resolve the emit
			// name through the same IntentAliases walk the dispatcher uses so
			// the membership test below matches; otherwise every forward emit
			// is misclassified "operator-only" and the chain wrongly stalls at
			// the gate (e.g. cherny-loop's `gating` imported into ship-it).
			emitTargets[m.resolveEmittedIntentName(state, n)] = struct{}{}
		}
	}
	env := expr.Env{Slots: map[string]any{}, World: w.Vars, Event: map[string]any{}}
	for _, name := range m.allowedIntentNames(app.StatePath(state)) {
		if _, isEmit := emitTargets[name]; isEmit {
			continue // auto-advance outcome, not an operator choice
		}
		tr, path, _, err := m.findTransitionTraced(ctx, state, name, env)
		if err != nil || tr == nil {
			continue // guard failed / errored → not available this turn
		}
		raw := strings.TrimSpace(tr.tr.Target)
		// Exit escapes (quit-style) are not forward branches. `@exit:<name>`
		// is rewritten to the synthesised terminal `__exit__<name>` at load
		// time, so match both the raw and rewritten forms.
		if strings.HasPrefix(raw, "@exit") || strings.HasPrefix(raw, "__exit__") {
			continue
		}
		if strings.Contains(raw, "{{") {
			if rendered, rerr := expr.Render(raw, env); rerr == nil {
				raw = strings.TrimSpace(rendered)
			}
			// On render failure, fall through and treat as forward
			// (conservative: prefer stopping over auto-advancing through
			// an unresolved target).
		}
		if resolveTarget(path, raw) == state {
			continue // self / recycle (look), not a branch choice
		}
		return true // operator-only forward intent exists → decision gate
	}
	return false
}

// IsDecisionGate is the exported wrapper around isDecisionGate, used by the
// orchestrator's engine decider to detect a gate post-settle.
func (m *machineImpl) IsDecisionGate(cur app.StatePath, w world.World) bool {
	return m.isDecisionGate(context.Background(), string(cur), w)
}

// isStagedGate reports whether the synthetic emit chain should STOP at
// `state` because a human decision is owed. It stops when the state is a
// decision gate (isDecisionGate) AND either staged mode is active or the
// state pins `decider: human`. A `decider: llm` pin forces auto-advance
// (the emit fires) even in staged mode — the "mix" override.
func (m *machineImpl) isStagedGate(ctx context.Context, state string, w world.World, staged bool) bool {
	decider := ""
	if cs, ok := m.states[state]; ok && cs.s != nil {
		decider = strings.TrimSpace(cs.s.Decider)
	}
	switch decider {
	case "human":
		return m.isDecisionGate(ctx, state, w)
	case "llm":
		return false
	}
	return staged && m.isDecisionGate(ctx, state, w)
}

// DecisionCandidates returns the intents a decider may choose between at a
// gate: those currently available (a firable arm) whose target advances to a
// different, non-`@exit` state. Self-loops (look) and exit escapes (quit) are
// excluded; unlike isDecisionGate this INCLUDES emit-target intents, because
// when an LLM decider runs (one-shot, the conditional-default emit did not
// fire) the author's intended action is itself a legitimate choice. Metadata
// (title/description/examples) rides along so the engine can build a decision
// prompt from the state machine's own vocabulary.
func (m *machineImpl) DecisionCandidates(cur app.StatePath, w world.World) []AllowedIntent {
	ctx := context.Background()
	env := expr.Env{Slots: map[string]any{}, World: w.Vars, Event: map[string]any{}}
	var out []AllowedIntent
	for _, name := range m.allowedIntentNames(cur) {
		tr, path, _, err := m.findTransitionTraced(ctx, string(cur), name, env)
		if err != nil || tr == nil {
			continue
		}
		raw := strings.TrimSpace(tr.tr.Target)
		if strings.HasPrefix(raw, "@exit") || strings.HasPrefix(raw, "__exit__") {
			continue
		}
		if strings.Contains(raw, "{{") {
			if rendered, rerr := expr.Render(raw, env); rerr == nil {
				raw = strings.TrimSpace(rendered)
			}
		}
		if resolveTarget(path, raw) == string(cur) {
			continue
		}
		intentDef, _ := m.lookupIntent(cur, name)
		out = append(out, AllowedIntent{
			Name:        name,
			Title:       intentDef.Title,
			Description: intentDef.Description,
			Examples:    intentDef.Examples,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// RenderState renders the view for a state+world snapshot. Used by the
// orchestrator to refresh the on-screen view after host-call bindings write
// new values into world on the same turn.
func (m *machineImpl) RenderState(cur app.StatePath, w world.World) (string, error) {
	text, _, _, _, err := m.RenderStateTyped(cur, w)
	return text, err
}

// RenderStateTyped is RenderState with the typed-view payload surfaced.
// Returns (text, typed, env, rr, err). typed is non-nil only when the
// resolved leaf's view is a pure element-array form (the same shape
// renderViewWithTyped surfaces for per-turn renders) — the TUI uses it
// to drive AppendSystemTyped at the initial-paint seam so the rendered
// ANSI doesn't get double-processed through Glamour. Parallel views
// and legacy string/extends/template_file shapes return typed=nil and
// the existing string-rendering path is used.
func (m *machineImpl) RenderStateTyped(cur app.StatePath, w world.World) (string, *app.View, expr.Env, *render.AppRenderer, error) {
	env := expr.Env{
		Slots: map[string]any{},
		World: w.Vars,
		Menu:  MenuToTemplateMap(m.Menu(cur, w)),
		State: stateMetaFor(m, cur),
	}
	expr.PopulateMenuHelpers(&env)
	if par := parseParallel(string(cur)); par.IsParallel {
		// Parallel composition: parent view (if any) + each leaf view.
		// Each leaf renders with its own State metadata so the per-region
		// view sees its own `{{ state.path }}` / `{{ state.description }}`.
		// Typed-view payload is not surfaced for parallel composites —
		// the composition is per-leaf and there is no single typed View
		// to re-render at viewport width.
		var sb strings.Builder
		if parCS, ok := m.states[par.Root]; ok && parCS.s != nil && !parCS.s.View.IsEmpty() {
			parentEnv := env
			parentEnv.State = stateMetaFor(m, app.StatePath(par.Root))
			v, err := m.renderViewBody(parCS.s.View, parentEnv, par.Root)
			if err != nil {
				return "", nil, env, nil, fmt.Errorf("render parallel parent view %q: %w", par.Root, err)
			}
			sb.WriteString(v)
		}
		for _, leaf := range par.RegionLeaves {
			cs, ok := m.states[leaf]
			if !ok || cs.s == nil || cs.s.View.IsEmpty() {
				continue
			}
			leafEnv := env
			leafEnv.State = stateMetaFor(m, app.StatePath(leaf))
			v, err := m.renderViewBody(cs.s.View, leafEnv, leaf)
			if err != nil {
				return "", nil, env, nil, fmt.Errorf("render region leaf view %q: %w", leaf, err)
			}
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(v)
		}
		return sb.String(), nil, env, nil, nil
	}
	cs, ok := m.states[string(cur)]
	if !ok || cs.s == nil || cs.s.View.IsEmpty() {
		return "", nil, env, nil, nil
	}
	text, err := m.renderViewBody(cs.s.View, env, string(cur))
	if err != nil {
		return "", nil, env, nil, err
	}
	rr := m.rendererFor(string(cur))
	var typed *app.View
	if typedViewIsElementArray(cs.s.View) {
		v := cs.s.View
		typed = &v
	}
	return text, typed, env, rr, nil
}

// stateMetaFor returns the `state` metadata map populated for pongo
// view templates: {"path": <state path>, "description": <state desc>}.
// Authors reference `{{ state.path }}` / `{{ state.description }}` in
// view bodies and shared base.pongo layouts (e.g. cloak/oregon-trail's
// default heading block is `{{ state.description }}`). Missing or
// undeclared states surface an empty map so undefined references
// render as empty string rather than erroring.
func stateMetaFor(m *machineImpl, p app.StatePath) map[string]any {
	out := map[string]any{
		"path":        string(p),
		"description": "",
	}
	if cs, ok := m.states[string(p)]; ok && cs.s != nil {
		out["description"] = cs.s.Description
	}
	return out
}

// ResolveInitialLeaf descends a compound state to its initial leaf. See
// the Machine interface doc-comment for motivation.
func (m *machineImpl) ResolveInitialLeaf(cur app.StatePath, w world.World) (app.StatePath, error) {
	env := expr.Env{
		Slots: map[string]any{},
		World: w.Vars,
	}
	leaf, err := m.resolveInitialAware(string(cur), env)
	if err != nil {
		return cur, err
	}
	return app.StatePath(leaf), nil
}

// RunEffects walks effects and returns the new world, host calls collected,
// accumulated say-text, effect events, and an error. It builds the same
// expr.Env that applyEffectsTraced uses (empty slots, empty event), so
// on_complete effects have access to world.* as expected.
//
// If the chain emits any synthetic intents via emit_intent: effects,
// they are dispatched against `state` and their events / host calls /
// say-text are folded into the returned values. Callers that need to
// know the post-dispatch leaf state should use RunEffectsAndState.
func (m *machineImpl) RunEffects(ctx context.Context, state app.StatePath, w world.World, effects []app.Effect) (world.World, []HostInvocation, string, []store.Event, error) {
	_, nw, hc, say, evts, err := m.RunEffectsAndState(ctx, state, w, effects)
	return nw, hc, say, evts, err
}

// RunEffectsAndState is the emit_intent-aware variant: see RunEffects for
// the common semantics, with the added guarantee that the returned
// app.StatePath reflects any synthetic intent dispatches the chain
// triggered.
func (m *machineImpl) RunEffectsAndState(ctx context.Context, state app.StatePath, w world.World, effects []app.Effect) (app.StatePath, world.World, []HostInvocation, string, []store.Event, error) {
	return m.RunEffectsAndStateWithOptions(ctx, state, w, effects, RunEffectsOptions{})
}

func (m *machineImpl) RunEffectsAndStateWithOptions(ctx context.Context, state app.StatePath, w world.World, effects []app.Effect, opts RunEffectsOptions) (app.StatePath, world.World, []HostInvocation, string, []store.Event, error) {
	env := expr.Env{
		Slots: map[string]any{},
		World: w.Vars,
		Event: map[string]any{},
	}
	newWorld, hostCalls, saySB, evts, emits, err := m.applyEffectsTracedWithOptions(ctx, effects, w, env, opts)
	if err != nil {
		return state, newWorld, hostCalls, saySB.String(), evts, err
	}
	finalState := string(state)
	sayOut := saySB.String()
	if len(emits) > 0 && finalState != "" {
		// staged=false: see the Turn-path note above. RunEffectsAndState
		// drives synthetic / timeout / on_complete turns that are one-shot
		// by nature in this slice.
		ds, dw, dhc, dsay, devs, derr := m.dispatchEmittedIntents(ctx, finalState, newWorld, emits, env, 0, false, nil)
		if derr != nil {
			return state, newWorld, hostCalls, sayOut, evts, derr
		}
		finalState = ds
		newWorld = dw
		hostCalls = append(hostCalls, dhc...)
		if dsay != "" {
			if sayOut != "" {
				sayOut = sayOut + "\n" + dsay
			} else {
				sayOut = dsay
			}
		}
		evts = append(evts, devs...)
	}
	return app.StatePath(finalState), newWorld, hostCalls, sayOut, evts, nil
}

// resolveEffectValue evaluates an effect value.
//
//   - String values run through RenderValue so single-expression templates
//     preserve their typed result (e.g. bool from "{{ expr }}").
//   - Lists and maps recurse so templated string elements inside structured
//     values (e.g. host.run's `args: [...]`) are rendered too.  Without this,
//     `{{ world.jira_query }}` inside a list passes through verbatim and the
//     handler receives an unexpanded template.
//   - Other scalars are returned as-is.
//
// coerceSetValue coerces a set-effect value to the declared type of world key
// k. set-effect RHS values are expression strings (a bare YAML `true` in a
// world_in projection arrives as the string "true"), so a rendered scalar may
// land as a string even when the world schema declares the key bool/int/float.
// Without this, a later guard like `&& world.flag` would fail eval with
// `bool(string)`. Keys not declared in the schema, or values that don't need
// coercion, pass through unchanged.
func (m *machineImpl) coerceSetValue(k string, v any) any {
	if m.appDef == nil {
		return v
	}
	vd, ok := m.appDef.World[k]
	if !ok {
		return v
	}
	s, isStr := v.(string)
	if !isStr {
		return v
	}
	switch vd.Type {
	case "bool":
		switch s {
		case "true":
			return true
		case "false":
			return false
		}
	case "int":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	case "float":
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f
		}
	}
	return v
}

func resolveEffectValue(v any, env expr.Env, w world.World) (any, error) {
	switch val := v.(type) {
	case string:
		if !strings.Contains(val, "{{") {
			return val, nil
		}
		return expr.RenderValue(val, env)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			r, err := resolveEffectValue(item, env, w)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			r, err := resolveEffectValue(item, env, w)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	default:
		return v, nil
	}
}

// rendererFor returns the loader-aware AppRenderer keyed by the first
// path segment of statePath. If the segment matches an import alias
// (e.g. `bandits.encounter` → "bandits"), the sub-story renderer is
// returned so `extends: "base"` / `{% include %}` in that state's view
// resolve against the *child's* views/ directory. Host-level states
// and unknown aliases fall back to the host renderer.
//
// Issue 3: imported sub-stories ship their own views/ tree (e.g.
// stories/robbery/views/base.pongo) which would otherwise be dead
// code under a single host-rooted renderer.
func (m *machineImpl) rendererFor(statePath string) *render.AppRenderer {
	if statePath == "" {
		return m.renderer
	}
	// First segment is the alias under our flat-folded namespace.
	// Parallel-encoded paths look like "$par(root|r1=...|r2=...)"; the
	// `$par(` token is host-level (no alias). Fall through to the
	// host renderer for those.
	if strings.HasPrefix(statePath, "$par(") {
		return m.renderer
	}
	idx := strings.IndexByte(statePath, '.')
	first := statePath
	if idx >= 0 {
		first = statePath[:idx]
	}
	if r, ok := m.importRenderers[first]; ok && r != nil {
		return r
	}
	return m.renderer
}

// renderViewBody renders one view payload — legacy scalar string, the
// typed {extends, blocks} block-inheritance form, an element array, or
// a standalone template_file: reference — through an app-scoped
// AppRenderer. The renderer is selected by rendererFor(statePath) so
// imported sub-stories resolve {% extends %} / {% include %} against
// their own views/ directory (Issue 3).
//
// For the element-array form (no extends, no template_file), the
// dispatcher in internal/render/elements is invoked at the stable
// blockRenderWidth default. The TUI re-renders typed views at the
// real viewport width via the typed-view path on TurnOutcome
// (Issue 4 / option (a)); the pre-rendered text here is the
// width-80 fallback for trace dumps, OneShot, RenderState (used by
// teleport/oncomplete) and any caller that bypasses the typed seam.
func (m *machineImpl) renderViewBody(v app.View, env expr.Env, statePath string) (string, error) {
	if v.IsEmpty() {
		return "", nil
	}
	rr := m.rendererFor(statePath)

	switch {
	case v.TemplateFile != "":
		if rr == nil {
			return "", fmt.Errorf("renderViewBody: template_file %q requires a per-app renderer; none configured", v.TemplateFile)
		}
		return rr.RenderFile(v.TemplateFile, env)

	case v.Extends != "":
		if rr == nil {
			return "", fmt.Errorf("renderViewBody: extends %q requires a per-app renderer; none configured", v.Extends)
		}
		// Pre-render each block body through the elements dispatcher
		// so block content honours typed-element semantics (when:
		// guards, leaf-string pongo substitution, list/kv layout).
		blocks, err := m.renderBlocks(v.Blocks, env, rr)
		if err != nil {
			return "", err
		}
		return rr.RenderExtended(v.Extends, blocks, env)

	case v.Source != "":
		// Legacy scalar string form. Route through the per-app
		// renderer so {% extends %} / {% include %} inside the source
		// resolve against <appDir>/views/. The renderer's fast-path
		// (no delimiters → return verbatim) and inline FromString
		// behaviour are identical to the package-level render.Pongo,
		// so apps without a views/ directory render unchanged.
		if rr != nil {
			return rr.Render(v.Source, env)
		}
		return render.Pongo(v.Source, env)

	case len(v.Elements) > 0:
		// Pure element-array form (no extends, no template_file).
		// Dispatch the typed-element pipeline at the stable
		// blockRenderWidth default. The TUI re-renders the same View
		// at viewport width via TurnOutcome.TypedView (Issue 4 / 2).
		return elements.RenderAll(v, env, blockRenderWidth, elements.IdentityGlamour, rr)
	}

	return "", nil
}

// blockRenderWidth is the layout width used when rendering typed
// elements outside the TUI viewport seam. The machine has no
// viewport awareness; tests, trace dumps, and OneShot output bind to
// this default. The TUI re-renders typed views at the real terminal
// width via the typed-view path on TurnOutcome (Issue 4).
const blockRenderWidth = 80

// renderBlocks pre-renders each named block's element array into a
// flat string keyed by block name. Each block is rendered with an
// "identity" glamour callback (the post-pongo source returned
// verbatim) — the wrapping template's base layout is the only entity
// allowed to apply terminal styling, and it does so by re-parsing the
// composite via the AppRenderer. The rr argument carries the per-app
// renderer for leaf-substitution (Issue 1).
func (m *machineImpl) renderBlocks(blocks map[string][]app.ViewElement, env expr.Env, rr *render.AppRenderer) (map[string]string, error) {
	if len(blocks) == 0 {
		return map[string]string{}, nil
	}
	out := make(map[string]string, len(blocks))
	for name, els := range blocks {
		// Wrap the block's elements as a synthetic View so we can
		// reuse the dispatcher. Width 80 is the orchestrator-side
		// default; lipgloss-aware reflow happens later in the TUI
		// pipeline. The block body is plain text at this seam.
		sub := app.View{Elements: els}
		body, err := elements.RenderAll(sub, env, blockRenderWidth, elements.IdentityGlamour, rr)
		if err != nil {
			return nil, fmt.Errorf("render block %q: %w", name, err)
		}
		out[name] = body
	}
	return out, nil
}

// renderView computes the view text for a turn per the view-precedence rule:
//   - Transition view wins (if declared).
//   - Otherwise, target state view.
//   - If say text exists, prepend it.
func (m *machineImpl) renderView(tr app.Transition, targetPath string, w world.World, env expr.Env, sayText string) (string, error) {
	text, _, _, _, err := m.renderViewWithTyped(tr, targetPath, w, env, sayText)
	return text, err
}

// renderViewWithTyped is renderView with the typed payload exposed for
// the TUI's resize-time re-render path (Issue 4 / option (a)). It
// returns the rendered text (identical to renderView), plus the typed
// View used (nil for legacy/extends/template_file shapes), the
// renderEnv captured at the seam, and the per-namespace AppRenderer
// (Issue 3).
func (m *machineImpl) renderViewWithTyped(tr app.Transition, targetPath string, w world.World, env expr.Env, sayText string) (string, *app.View, expr.Env, *render.AppRenderer, error) {
	// Build a new env with the updated world for rendering. Populate
	// env.Menu (and the helper-fn closures) so view templates can render
	// the "what can I do right now" surface inline — primary/blocked
	// intents with reasons. The menu is computed against the resolved
	// target path and the post-effect world so the on-screen menu reflects
	// the state the user is about to see.
	renderEnv := expr.Env{
		Slots: env.Slots,
		World: w.Vars,
		Event: env.Event,
		Run:   env.Run,
		Menu:  MenuToTemplateMap(m.Menu(app.StatePath(targetPath), w)),
		State: stateMetaFor(m, app.StatePath(targetPath)),
	}
	expr.PopulateMenuHelpers(&renderEnv)

	var (
		viewText string
		typed    *app.View
	)

	switch {
	case !tr.View.IsEmpty():
		v, err := m.renderViewBody(tr.View, renderEnv, targetPath)
		if err != nil {
			return "", nil, renderEnv, nil, fmt.Errorf("render transition view: %w", err)
		}
		viewText = v
		if typedViewIsElementArray(tr.View) {
			vv := tr.View
			typed = &vv
		}
	default:
		// Use target state's view.
		cs, ok := m.states[targetPath]
		if ok && !cs.s.View.IsEmpty() {
			v, err := m.renderViewBody(cs.s.View, renderEnv, targetPath)
			if err != nil {
				return "", nil, renderEnv, nil, fmt.Errorf("render state view for %q: %w", targetPath, err)
			}
			viewText = v
			if typedViewIsElementArray(cs.s.View) {
				vv := cs.s.View
				typed = &vv
			}
		}
	}

	rr := m.rendererFor(targetPath)

	if sayText != "" && viewText != "" {
		return sayText + "\n\n" + viewText, typed, renderEnv, rr, nil
	}
	if sayText != "" {
		return sayText, typed, renderEnv, rr, nil
	}
	return viewText, typed, renderEnv, rr, nil
}

// typedViewIsElementArray reports whether the View is a pure
// element-array form (no extends, no template_file, no legacy scalar
// source — just an Elements slice). Only this shape benefits from the
// TUI's typed-view resize path: extends/template_file render via the
// pongo2 loader at machine time and have no per-element layout to
// reflow; legacy strings flow through Glamour.
func typedViewIsElementArray(v app.View) bool {
	return v.TemplateFile == "" && v.Extends == "" && v.Source == "" && len(v.Elements) > 0
}

// ─── Machine.TryGuards ───────────────────────────────────────────────────────

// TryGuards performs a guard dry-run without applying any transition.
// It walks the transition arms for the intent in declaration order (leaf first)
// using the provided prefillSlots. If a guard evaluation errors (e.g. because
// a referenced slot is absent), the result is Unresolved — callers should treat
// this as primary (the guard will be re-evaluated at submission time).
func (m *machineImpl) TryGuards(cur app.StatePath, w world.World, intentName string, prefillSlots map[string]any) GuardDryRunResult {
	env := expr.Env{
		Slots: prefillSlots,
		World: w.Vars,
		Event: make(map[string]any),
	}

	// Walk from leaf to root, same as findTransition.
	path := string(cur)
	for {
		cs, ok := m.states[path]
		if ok {
			handlers := cs.on[intentName]
			if len(handlers) > 0 {
				return tryEvaluateArms(handlers, env, path)
			}
			// Wildcard handlers are not expanded (per spec: wildcards are not enumerated).
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
	// Intent not found; treat as unresolved.
	return GuardDryRunResult{Unresolved: true}
}

// tryEvaluateArms evaluates transition arms for a guard dry-run.
// If a guard evaluation errors (unresolved slot), returns Unresolved=true.
//
// MatchedDefault is set when the only arm that fires is a default: branch (i.e.
// no when: guard matched). Callers that build menus use MatchedDefault to omit
// the row entirely: the default arm is a runtime safety net, not a real
// transition the author intends to surface in the menu.
func tryEvaluateArms(arms []compiledTransition, env expr.Env, statePath string) GuardDryRunResult {
	hint := ""
	whenFailed := false
	for i := range arms {
		arm := &arms[i]
		if arm.tr.Default {
			// Default arm always fires, but mark MatchedDefault so menu-builders
			// can distinguish this from a real when: match. Carry a hint so
			// menu code can surface "you can type this but it won't do what
			// you want" as a blocked entry. Prefer the default arm's own
			// guard_hint (the canonical place authors document the unmet
			// precondition); fall back to any hint captured from a failing
			// when arm earlier in the list.
			target := resolveTarget(statePath, arm.tr.Target)
			reason := arm.tr.GuardHint
			if reason == "" {
				reason = hint
			}
			return GuardDryRunResult{
				Primary:         true,
				MatchedDefault:  true,
				WhenArmFailed:   whenFailed,
				DestinationHint: target,
				BlockedReason:   reason,
			}
		}
		if arm.guard == nil {
			// No guard = always true (not a default: branch).
			target := resolveTarget(statePath, arm.tr.Target)
			return GuardDryRunResult{Primary: true, DestinationHint: target}
		}
		ok, err := expr.EvalBool(arm.guard, env)
		if err != nil {
			// Guard referenced an unresolved slot; treat as unresolved (primary by default).
			return GuardDryRunResult{Unresolved: true}
		}
		if ok {
			target := resolveTarget(statePath, arm.tr.Target)
			return GuardDryRunResult{Primary: true, DestinationHint: target}
		}
		// Guard failed; capture the hint from the first failing guard.
		whenFailed = true
		if hint == "" && arm.tr.GuardHint != "" {
			hint = arm.tr.GuardHint
		}
	}
	// All guards failed, no default.
	return GuardDryRunResult{Blocked: true, BlockedReason: hint, WhenArmFailed: true}
}

// ─── Machine.AllowedIntents ──────────────────────────────────────────────────

// AllowedIntents returns the list of intents currently allowed in the state,
// populated with metadata for progressive disclosure.
func (m *machineImpl) AllowedIntents(cur app.StatePath, w world.World) []AllowedIntent {
	names := m.allowedIntentNames(cur)
	allowed := make([]AllowedIntent, 0, len(names))
	for _, name := range names {
		intentDef, ok := m.lookupIntent(cur, name)
		if !ok {
			continue
		}
		allowed = append(allowed, AllowedIntent{
			Name:        name,
			Title:       intentDef.Title,
			Description: intentDef.Description,
			Examples:    intentDef.Examples,
			Priority:    intentDef.Priority,
			Hidden:      intentDef.Hidden,
		})
	}
	// Sort by priority desc, then name asc for stability.
	sort.Slice(allowed, func(i, j int) bool {
		if allowed[i].Priority != allowed[j].Priority {
			return allowed[i].Priority > allowed[j].Priority
		}
		return allowed[i].Name < allowed[j].Name
	})
	return allowed
}

// ─── Event helpers ───────────────────────────────────────────────────────────

var eventSeq atomic.Int64 // package-level monotonic seq; safe for concurrent use

// allBindTargetsSet reports whether every LHS world key in an invoke's
// bind: map is already "set" (non-empty) in the given world vars. It backs
// the `once:` idempotent-invoke guard (app.Effect.Once): when it returns
// true the engine skips the invoke because its result is already cached in
// world. An empty bind map returns false so a misconfigured once: (caught at
// load time) never silently skips. A value counts as UNSET when it is nil,
// an empty string "", an empty map, or an empty slice; anything else is SET.
// Scalar int/bool binds are intentionally NOT special-cased — a real 0 /
// false reads as SET (non-empty), so authors should guard scalars by hand
// with When rather than once:.
func allBindTargetsSet(bind map[string]string, vars map[string]any) bool {
	if len(bind) == 0 {
		return false
	}
	for worldKey := range bind {
		if !worldValueSet(vars[worldKey]) {
			return false
		}
	}
	return true
}

// worldValueSet reports whether a world value counts as SET for once:.
// UNSET: nil, "", empty map, empty slice. Everything else is SET.
func worldValueSet(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case map[string]any:
		return len(t) > 0
	case []any:
		return len(t) > 0
	default:
		rv := reflect.ValueOf(v)
		switch rv.Kind() {
		case reflect.Map, reflect.Slice, reflect.Array:
			return rv.Len() > 0
		case reflect.Ptr, reflect.Interface:
			return !rv.IsNil()
		default:
			return true
		}
	}
}

func newEvent(kind store.EventKind, payload map[string]any) store.Event {
	b, _ := json.Marshal(payload)
	seq := eventSeq.Add(1)
	return store.Event{
		Kind:    kind,
		Seq:     int(seq),
		Payload: b,
	}
}

// stateExitPaths returns the state paths that will be exited when moving from
// oldPath to newPath. Only paths not shared with newPath are included.
// Order: deepest first (leaf to root).
func stateExitPaths(oldPath, newPath string) []string {
	oldParts := strings.Split(oldPath, ".")
	newParts := strings.Split(newPath, ".")
	shared := commonPrefixLen(oldParts, newParts)
	var out []string
	for i := len(oldParts) - 1; i >= shared; i-- {
		out = append(out, strings.Join(oldParts[:i+1], "."))
	}
	return out
}

// stateEnterPaths returns the state paths that will be entered when moving from
// oldPath to newPath. Only paths not shared with oldPath are included.
// Order: shallowest first (root to leaf).
func stateEnterPaths(oldPath, newPath string) []string {
	oldParts := strings.Split(oldPath, ".")
	newParts := strings.Split(newPath, ".")
	shared := commonPrefixLen(oldParts, newParts)
	var out []string
	for i := shared; i < len(newParts); i++ {
		out = append(out, strings.Join(newParts[:i+1], "."))
	}
	return out
}

// stateExitPathsAware extends stateExitPaths to gracefully handle parallel-
// encoded paths. For exits, we use the structural-parent of either side
// (`StripParallel`) — entering or leaving a parallel state at the parent
// level is what matters for outer ancestors; region-internal exits are
// emitted separately by the parallel turn handler.
func stateExitPathsAware(oldPath, newPath string) []string {
	return stateExitPaths(stripParallel(oldPath), stripParallel(newPath))
}

// stateEnterPathsAware extends stateEnterPaths to expand a parallel-encoded
// target into per-region leaf chains. For an entry transition into a
// parallel state, we emit: outer compound ancestors (if any), the parallel
// parent itself, then each region's leaf chain (shared parent already
// included). This drives on_enter to fire on every region as it "enters".
func stateEnterPathsAware(oldPath, newPath string) []string {
	if !strings.Contains(newPath, parallelSigil) {
		return stateEnterPaths(stripParallel(oldPath), newPath)
	}
	par := parseParallel(newPath)
	oldStripped := stripParallel(oldPath)
	// 1. ancestor entries up to and including the parallel parent.
	out := stateEnterPaths(oldStripped, par.Root)
	// 2. each region leaf chain — entries below the parallel parent.
	for _, leaf := range par.RegionLeaves {
		if leaf == par.Root || leaf == "" {
			continue
		}
		out = append(out, stateEnterPaths(par.Root, leaf)...)
	}
	return out
}

func commonPrefixLen(a, b []string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

// ─── Utility helpers ─────────────────────────────────────────────────────────

func cloneWorld(w world.World) world.World {
	nw := world.World{Vars: make(map[string]any, len(w.Vars))}
	for k, v := range w.Vars {
		nw.Vars[k] = v
	}
	return nw
}

// hostCallsWillBind reports whether any of the queued host invocations declares
// a `bind:` directive — i.e. the orchestrator's post-dispatch step will mutate
// world and re-render the view.  Used by Turn / turnParallel to skip a wasted
// pre-bind render whose only purpose would be to throw on un-bound fields.
func hostCallsWillBind(hcs []HostInvocation) bool {
	for _, hc := range hcs {
		if len(hc.Bind) > 0 {
			return true
		}
	}
	return false
}

func slotsToMap(slots world.Slots) map[string]any {
	if slots == nil {
		return make(map[string]any)
	}
	m := make(map[string]any, len(slots))
	for k, v := range slots {
		m[k] = v
	}
	return m
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case int:
		return int64(x)
	case int64:
		return x
	case int32:
		return int64(x)
	case uint:
		return int64(x)
	case uint32:
		return int64(x)
	case uint64:
		return int64(x)
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	}
	return 0
}

// WorldFromSchema initialises a World from the app's world schema defaults.
// Each call returns a fresh, independent, mutable World — callers may safely
// mutate the result without affecting the schema or any other World. Vars are
// populated only for schema entries that declare a non-nil Default; a nil or
// empty schema yields an empty (but usable) World, never a nil one.
func WorldFromSchema(schema app.WorldSchema) world.World {
	w := world.New()
	for k, def := range schema {
		if def.Default != nil {
			w.Vars[k] = def.Default
		}
	}
	// Reserved, engine-managed cost vars. Seeded to 0 so a story can guard on
	// them (`when: "world.session_cost_usd >= world.cost_budget"`) without
	// declaring them, and so a guard that runs before any agent call reads a
	// number rather than nil. The orchestrator overwrites these from real agent
	// spend each turn (see foldAgentCost). A story that declares its own default
	// for either key keeps it — the seed only fills an absent key.
	if _, ok := w.Vars["session_cost_usd"]; !ok {
		w.Vars["session_cost_usd"] = 0.0
	}
	if _, ok := w.Vars["turn_cost_usd"]; !ok {
		w.Vars["turn_cost_usd"] = 0.0
	}
	// Reserved, engine-owned string globals (last_error, write_mode_scope; see
	// app.ReservedWorldKeys). Seeded to "" for the same reason as the cost vars:
	// a view condition or guard that runs before the engine ever writes the key
	// (`when: "world.last_error == ''"`) must read "" rather than nil — `nil ==
	// ''` is false, which silently suppresses every banner/prose gated on the
	// no-error state. This MUST be seeded here rather than left to the story's
	// own world block: import folding deliberately drops a child's declaration of
	// a reserved key (it stays bare at every depth), so a room that declares
	// `last_error: {default: ""}` and renders correctly standalone would diverge
	// when imported under an alias (e.g. dev-story's landing under kitsoki-dev).
	// host_error is a map, guarded only via `?? ''` / `|default:`, so it is
	// left nil. The seed only fills an absent key.
	if _, ok := w.Vars["last_error"]; !ok {
		w.Vars["last_error"] = ""
	}
	if _, ok := w.Vars[app.WriteModeScopeWorldKey]; !ok {
		w.Vars[app.WriteModeScopeWorldKey] = ""
	}
	return w
}
