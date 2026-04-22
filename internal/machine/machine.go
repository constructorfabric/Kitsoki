// Package machine implements the pure deterministic state machine core (§4, §12.1).
// No I/O; consumers are the MCP server, the replay harness, and tests.
//
// # Parallel states
//
// Parallel-region support is OUT OF SCOPE for this PoC (§14 open question).
// Machine construction rejects any app whose root or any state declares
// type: "parallel" with a clear error. Authors must restructure to compound
// states for the PoC.
//
// # Event ordering within a turn
//
// Natural ordering:
//
//	IntentAccepted → ValidationFailed (if rejected, stop) |
//	TransitionApplied → EffectApplied* → StateExited* → StateEntered*
//
// §8 lists the canonical event kinds. We do not emit TurnStarted / TurnEnded
// here; those are orchestrator-level events. The machine emits only the events
// that result from evaluating a single IntentCall.
//
// # Guard-hint policy (§7.5 ambiguity)
//
// When multiple guarded transitions fail, we return the guard_hint from the
// *first* failing transition (most specific in declaration order). This follows
// "first-guard-wins" ordering — the first branch that was tried and failed is
// the most relevant for the author to explain.
//
// # View precedence (§7.6)
//
// If the winning transition declares a view:, it is rendered and returned.
// The target state's view is NOT additionally appended (it would be shown on
// the next "look" or re-entry, not on the current transition). This keeps the
// turn output unambiguous. Authors who want both should write both in the
// transition view. If the transition has no view:, only the target state's
// view is rendered.
package machine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"hally/internal/app"
	"hally/internal/expr"
	"hally/internal/intent"
	"hally/internal/store"
	"hally/internal/trace"
	"hally/internal/world"
)

// AllowedIntent describes one intent that is currently valid for the user,
// as produced by Machine.AllowedIntents for the §7.2 progressive-disclosure menu.
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
// dispatch outside the pure machine (§11).
type HostInvocation struct {
	Namespace string         `json:"namespace"`
	Args      map[string]any `json:"args,omitempty"`
	EmitEvent string         `json:"emit_event,omitempty"`
}

// TurnResult is returned by Machine.Turn after a successful transition.
type TurnResult struct {
	NewState  app.StatePath `json:"new_state"`
	World     world.World   `json:"world"`
	View      string        `json:"view"`
	Menu      []string      `json:"menu"`
	Events    []store.Event `json:"events,omitempty"`
	HostCalls []HostInvocation `json:"host_calls,omitempty"`
	// ValidationError is set when the intent was rejected (no transition fired).
	// In that case NewState equals the input state and World is unchanged.
	ValidationError *intent.ValidationError `json:"validation_error,omitempty"`
}

// Machine is the pure deterministic core (§12.1).
type Machine interface {
	Turn(ctx context.Context, cur app.StatePath, w world.World, call intent.IntentCall) (TurnResult, error)
	AllowedIntents(cur app.StatePath, w world.World) []AllowedIntent
	Validate(cur app.StatePath, w world.World, call intent.IntentCall) ValidationResult
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
}

// GuardDryRunResult is the result of TryGuards.
type GuardDryRunResult struct {
	// Primary is true when a guard arm matched (or is a default/no-guard branch).
	Primary bool
	// Blocked is true when all guard arms were evaluated and none matched (and no default).
	Blocked bool
	// Unresolved is true when at least one guard could not be evaluated because
	// a referenced slot was missing from the prefill map. Treat as primary.
	Unresolved bool
	// MatchedDefault is true when Primary is true because a default: branch fired
	// (i.e. no when: branch matched). Menu-building code uses this to omit the
	// row entirely: the default arm is a runtime catch-all, not a real transition
	// the author intended to surface.
	MatchedDefault bool
	// DestinationHint is the resolved target state path when Primary is true.
	DestinationHint string
	// BlockedReason is the guard_hint from the first failing transition when Blocked is true.
	BlockedReason string
}

// ─── compiled guards ─────────────────────────────────────────────────────────

// compiledTransition holds a Transition with its pre-compiled guard program.
type compiledTransition struct {
	tr       app.Transition
	guard    *expr.Program // nil when no When guard
	viewProg *expr.Program // nil when no View template
}

// compiledState is a State with pre-compiled guard programs on every transition.
type compiledState struct {
	s    *app.State
	on   map[string][]compiledTransition // intent name -> compiled transitions
	view *expr.Program                   // nil when no view
}

// machineImpl is the concrete Machine implementation.
type machineImpl struct {
	appDef  *app.AppDef
	states  map[string]*compiledState // dot-separated path -> compiled state
	logger  *slog.Logger
}

// MachineOption is a functional option for Machine construction.
type MachineOption func(*machineImpl)

// WithMachineLogger sets the logger for guard/effect trace events.
func WithMachineLogger(l *slog.Logger) MachineOption {
	return func(m *machineImpl) {
		if l != nil {
			m.logger = l
		}
	}
}

// New creates a new Machine from a validated AppDef.
// It pre-compiles all guards, view templates, and guard hints.
// Returns an error (via errors.Join) listing every compilation failure.
// Returns an error if the app declares any parallel regions.
func New(def *app.AppDef, opts ...MachineOption) (Machine, error) {
	m := &machineImpl{
		appDef: def,
		states: make(map[string]*compiledState),
		logger: slog.Default(),
	}
	for _, opt := range opts {
		opt(m)
	}

	var errs []error

	// Check for parallel regions (out of scope for PoC).
	if err := rejectParallelStates("", def.States); err != nil {
		return nil, err
	}

	// Pre-compile all states.
	compileStatesInto("", def.States, m.states, &errs)

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return m, nil
}

// rejectParallelStates walks the state tree and returns an error if any state
// declares type: "parallel". Parallel support is deferred to a future stage.
func rejectParallelStates(prefix string, states map[string]*app.State) error {
	for name, s := range states {
		if s == nil {
			continue
		}
		path := joinStatePath(prefix, name)
		if s.Type == "parallel" {
			return fmt.Errorf("machine: state %q declares type: parallel, which is not supported in this PoC (§14)", path)
		}
		if err := rejectParallelStates(path, s.States); err != nil {
			return err
		}
	}
	return nil
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
		if s.View != "" {
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
func (m *machineImpl) hasWildcard(cur app.StatePath) bool {
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
func (m *machineImpl) allowedIntentNames(cur app.StatePath) []string {
	seen := make(map[string]struct{})
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

// lookupIntent looks up an intent definition by name scoped to the given state.
func (m *machineImpl) lookupIntent(cur app.StatePath, name string) (app.Intent, bool) {
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
	var missing []string
	for slotName, slotDef := range intentDef.Slots {
		val, present := slots[slotName]
		if slotDef.Required && (!present || val == nil || val == "") {
			missing = append(missing, slotName)
			continue
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
					"code":      string(intent.ErrGuardFailed),
					"intent":    call.Intent,
					"state":     string(cur),
					"guard_hint": hint,
				}),
			},
		}, nil
	}

	// 4. Resolve the target state path.
	targetPath := resolveTarget(winningPath, winningTr.tr.Target)

	// 5. For compound states, resolve the initial child.
	resolvedTarget, err := m.resolveInitial(targetPath, env)
	if err != nil {
		return TurnResult{}, fmt.Errorf("resolve initial for %q: %w", targetPath, err)
	}

	// emit machine.transition before applying effects
	m.logger.DebugContext(ctx, trace.EvMachineTransition,
		slog.String("from", string(cur)),
		slog.String("to", resolvedTarget),
		slog.String("intent", call.Intent),
	)

	// 6. Apply effects.
	newWorld, hostCalls, saySB, effectEvents, err := m.applyEffectsTraced(ctx, winningTr.tr.Effects, w, env)
	if err != nil {
		return TurnResult{}, err
	}

	// 7. Render view.
	renderedView, err := m.renderView(winningTr.tr, resolvedTarget, newWorld, env, saySB.String())
	if err != nil {
		return TurnResult{}, err
	}

	// 8. Build event sequence:
	//    IntentAccepted → TransitionApplied → EffectApplied* → StateExited* → StateEntered*
	var events []store.Event

	events = append(events, newEvent(store.TransitionApplied, map[string]any{
		"from":   string(cur),
		"to":     resolvedTarget,
		"intent": call.Intent,
	}))

	events = append(events, effectEvents...)

	// Emit StateExited for each level of the old path that is not shared.
	exited := stateExitPaths(string(cur), resolvedTarget)
	for _, p := range exited {
		events = append(events, newEvent(store.StateExited, map[string]any{"state": p}))
	}

	// Emit StateEntered for each new level of the new path.
	entered := stateEnterPaths(string(cur), resolvedTarget)
	for _, p := range entered {
		events = append(events, newEvent(store.StateEntered, map[string]any{"state": p}))
	}

	// 9. Build menu for new state.
	newMenu := m.allowedIntentNames(app.StatePath(resolvedTarget))

	return TurnResult{
		NewState:  app.StatePath(resolvedTarget),
		World:     newWorld,
		View:      renderedView,
		Menu:      newMenu,
		Events:    events,
		HostCalls: hostCalls,
	}, nil
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

// applyEffectsTraced is applyEffects with machine.effect.applied trace events.
func (m *machineImpl) applyEffectsTraced(ctx context.Context, effects []app.Effect, w world.World, env expr.Env) (world.World, []HostInvocation, strings.Builder, []store.Event, error) {
	newWorld := cloneWorld(w)
	var hostCalls []HostInvocation
	var saySB strings.Builder
	var effectEvents []store.Event

	for _, eff := range effects {
		switch {
		case len(eff.Set) > 0:
			for k, v := range eff.Set {
				resolved, err := resolveEffectValue(v, env, newWorld)
				if err != nil {
					return world.World{}, nil, saySB, nil, fmt.Errorf("effect set %q: %w", k, err)
				}
				before := newWorld.Vars[k]
				newWorld.Vars[k] = resolved
				env.World = newWorld.Vars
				m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
					slog.String("type", "set"),
					slog.String("key", k),
					slog.Any("before", before),
					slog.Any("after", resolved),
				)
				effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
					"set": map[string]any{k: resolved},
				}))
			}

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
			text, err := expr.Render(eff.Say, env)
			if err != nil {
				return world.World{}, nil, saySB, nil, fmt.Errorf("effect say: %w", err)
			}
			if saySB.Len() > 0 {
				saySB.WriteString("\n")
			}
			saySB.WriteString(text)
			m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
				slog.String("type", "say"),
				slog.String("text", text),
			)
			effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
				"say": text,
			}))

		case eff.Invoke != "":
			hc := HostInvocation{
				Namespace: eff.Invoke,
				Args:      eff.With,
				EmitEvent: eff.Emit,
			}
			hostCalls = append(hostCalls, hc)
			m.logger.DebugContext(ctx, trace.EvMachineEffectApplied,
				slog.String("type", "invoke"),
				slog.String("namespace", eff.Invoke),
			)
			effectEvents = append(effectEvents, newEvent(store.HostInvoked, map[string]any{
				"namespace": eff.Invoke,
				"args":      eff.With,
			}))
		}
	}
	return newWorld, hostCalls, saySB, effectEvents, nil
}

// findTransition walks the transition arms for a given intent in the state
// path (leaf first, then ancestors for compound states) and returns the first
// winning compiledTransition, the state path it belongs to, and any guard hint.
func (m *machineImpl) findTransition(leafPath, intentName string, env expr.Env) (*compiledTransition, string, string, error) {
	// Walk from leaf to root.
	path := leafPath
	for {
		cs, ok := m.states[path]
		if ok {
			// Try the intent's handlers first.
			handlers := cs.on[intentName]
			if len(handlers) > 0 {
				ct, hint, err := evaluateArms(handlers, env)
				if err != nil {
					return nil, "", "", err
				}
				if ct != nil {
					return ct, path, "", nil
				}
				// All guards failed; return hint from first failing guard.
				return nil, path, hint, nil
			}
			// Try wildcard "*" handlers.
			wildcardHandlers := cs.on["*"]
			if len(wildcardHandlers) > 0 {
				ct, hint, err := evaluateArms(wildcardHandlers, env)
				if err != nil {
					return nil, "", "", err
				}
				if ct != nil {
					return ct, path, "", nil
				}
				return nil, path, hint, nil
			}
		}
		// Move up one level.
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
	return nil, leafPath, "", nil
}

// evaluateArms walks a list of compiledTransitions in order and returns the
// first one whose guard evaluates true (or which is a default branch).
// Returns nil if no arm matched, along with the guard hint from the first
// failing guarded transition.
func evaluateArms(arms []compiledTransition, env expr.Env) (*compiledTransition, string, error) {
	hint := ""
	for i := range arms {
		arm := &arms[i]
		if arm.tr.Default {
			return arm, "", nil
		}
		if arm.guard == nil {
			// No guard = always true.
			return arm, "", nil
		}
		ok, err := expr.EvalBool(arm.guard, env)
		if err != nil {
			return nil, "", err
		}
		if ok {
			return arm, "", nil
		}
		// Guard failed; capture the hint from the first failing guard.
		if hint == "" && arm.tr.GuardHint != "" {
			hint = arm.tr.GuardHint
		}
	}
	return nil, hint, nil
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
	childName, err := expr.Render(cs.s.Initial, env)
	if err != nil {
		return "", fmt.Errorf("evaluate initial expression %q: %w", cs.s.Initial, err)
	}
	childName = strings.TrimSpace(childName)
	childPath := joinStatePath(path, childName)
	// Recurse in case the child is itself a compound state.
	return m.resolveInitial(childPath, env)
}

// applyEffects applies an ordered list of effects to a world snapshot and
// returns the new world, any host calls, a "say" string builder, and effect events.
func (m *machineImpl) applyEffects(effects []app.Effect, w world.World, env expr.Env) (world.World, []HostInvocation, strings.Builder, []store.Event, error) {
	newWorld := cloneWorld(w)
	var hostCalls []HostInvocation
	var saySB strings.Builder
	var effectEvents []store.Event

	for _, eff := range effects {
		switch {
		case len(eff.Set) > 0:
			for k, v := range eff.Set {
				// Values may be expr-lang template strings.
				resolved, err := resolveEffectValue(v, env, newWorld)
				if err != nil {
					return world.World{}, nil, saySB, nil, fmt.Errorf("effect set %q: %w", k, err)
				}
				newWorld.Vars[k] = resolved
				// Update env.World so subsequent effects see the new value.
				env.World = newWorld.Vars
				effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
					"set": map[string]any{k: resolved},
				}))
			}

		case len(eff.Increment) > 0:
			for k, delta := range eff.Increment {
				cur := toInt64(newWorld.Vars[k])
				newWorld.Vars[k] = cur + int64(delta)
				env.World = newWorld.Vars
				effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
					"increment": map[string]any{k: delta},
				}))
			}

		case eff.Say != "":
			text, err := expr.Render(eff.Say, env)
			if err != nil {
				return world.World{}, nil, saySB, nil, fmt.Errorf("effect say: %w", err)
			}
			if saySB.Len() > 0 {
				saySB.WriteString("\n")
			}
			saySB.WriteString(text)
			effectEvents = append(effectEvents, newEvent(store.EffectApplied, map[string]any{
				"say": text,
			}))

		case eff.Invoke != "":
			hc := HostInvocation{
				Namespace: eff.Invoke,
				Args:      eff.With,
				EmitEvent: eff.Emit,
			}
			hostCalls = append(hostCalls, hc)
			effectEvents = append(effectEvents, newEvent(store.HostInvoked, map[string]any{
				"namespace": eff.Invoke,
				"args":      eff.With,
			}))
		}
	}
	return newWorld, hostCalls, saySB, effectEvents, nil
}

// resolveEffectValue evaluates an effect value. If it's a string template, run
// RenderValue to preserve the typed result (e.g. bool from "{{ expr }}").
// Otherwise, use the literal value.
func resolveEffectValue(v any, env expr.Env, w world.World) (any, error) {
	s, ok := v.(string)
	if !ok {
		return v, nil
	}
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	// Use RenderValue to preserve typed results from single-expression templates.
	return expr.RenderValue(s, env)
}

// renderView computes the view text for a turn per §7.6 precedence:
//   - Transition view wins (if declared).
//   - Otherwise, target state view.
//   - If say text exists, prepend it.
func (m *machineImpl) renderView(tr app.Transition, targetPath string, w world.World, env expr.Env, sayText string) (string, error) {
	// Build a new env with the updated world for rendering.
	renderEnv := expr.Env{
		Slots: env.Slots,
		World: w.Vars,
		Event: env.Event,
		Run:   env.Run,
	}

	var viewText string

	if tr.View != "" {
		v, err := expr.Render(tr.View, renderEnv)
		if err != nil {
			return "", fmt.Errorf("render transition view: %w", err)
		}
		viewText = v
	} else {
		// Use target state's view.
		cs, ok := m.states[targetPath]
		if ok && cs.s.View != "" {
			v, err := expr.Render(cs.s.View, renderEnv)
			if err != nil {
				return "", fmt.Errorf("render state view for %q: %w", targetPath, err)
			}
			viewText = v
		}
	}

	if sayText != "" && viewText != "" {
		return sayText + "\n\n" + viewText, nil
	}
	if sayText != "" {
		return sayText, nil
	}
	return viewText, nil
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
	for i := range arms {
		arm := &arms[i]
		if arm.tr.Default {
			// Default arm always fires, but mark MatchedDefault so menu-builders
			// can distinguish this from a real when: match.
			target := resolveTarget(statePath, arm.tr.Target)
			return GuardDryRunResult{Primary: true, MatchedDefault: true, DestinationHint: target}
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
		if hint == "" && arm.tr.GuardHint != "" {
			hint = arm.tr.GuardHint
		}
	}
	// All guards failed, no default.
	return GuardDryRunResult{Blocked: true, BlockedReason: hint}
}

// ─── Machine.AllowedIntents ──────────────────────────────────────────────────

// AllowedIntents returns the list of intents currently allowed in the state,
// populated with metadata for progressive disclosure (§7.2).
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

var eventSeq int // package-level monotonic seq; tests reset this if needed

func newEvent(kind store.EventKind, payload map[string]any) store.Event {
	b, _ := json.Marshal(payload)
	eventSeq++
	return store.Event{
		Kind:    kind,
		Seq:     eventSeq,
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
	case float64:
		return int64(x)
	}
	return 0
}

// WorldFromSchema initialises a World from the app's world schema defaults.
func WorldFromSchema(schema app.WorldSchema) world.World {
	w := world.New()
	for k, def := range schema {
		if def.Default != nil {
			w.Vars[k] = def.Default
		}
	}
	return w
}
