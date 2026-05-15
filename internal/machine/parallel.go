// parallel.go implements `type: parallel` runtime support (proposal §9.4).
//
// # State-path encoding
//
// Atomic and compound state paths are dotted strings unchanged from the
// PoC: e.g. `"bar.lit"`, `"leg_a_executing.traveling"`.
//
// A parallel-state path uses a sigil-based composite encoding:
//
//	"<parallel_root>#<region_a_leaf>|<region_b_leaf>|..."
//
//   - `<parallel_root>` is the absolute dotted path of the parallel parent.
//   - Each `<region_n_leaf>` is the *absolute* dotted path of that region's
//     current leaf (i.e. starts with `<parallel_root>.<region_name>` and may
//     drill deeper through nested compounds).
//   - Regions are listed in alphabetical order — the same deterministic order
//     used everywhere else for stable-when-replayed semantics.
//
// `#` and `|` are not legal characters in YAML state keys, so the encoding
// is unambiguous. Single string everywhere (orchestrator, store, replay,
// flow tests) — no struct changes to `app.StatePath`.
//
// Example: a parallel state `world_clock` with regions `calendar` and
// `weather` (the canonical OT use case, §5.1) currently in `calendar.day1`
// and `weather.dry` encodes as:
//
//	"world_clock#world_clock.calendar.day1|world_clock.weather.dry"
//
// # Intent dispatch
//
// First-region-wins: regions are walked in alphabetical order; the first
// region whose current-state-and-ancestors contains a matching `on:` binding
// (and whose guard evaluates true) is the winner. The other regions do not
// transition on this intent. This is intentionally weaker than full SCXML
// parallel semantics but is sufficient for OT's `world_clock` use case
// where regions have non-overlapping intent surfaces.
//
// # Emit propagation
//
// When a transition's effect chain emits an event (via `emit:` on an Invoke
// effect, captured as HostInvocation.EmitEvent) or via the wholly-internal
// "auto-emit" path (when a transition fires and its target state's name —
// or any user-supplied transition `Emit:` field — should propagate), every
// OTHER region's current state is inspected. If a region has an `on:`
// binding for the emitted event name, that arm fires as a chained transition
// inside the same turn — single mutable world is shared; `set:` effects are
// applied left-to-right. Emit chains are depth-capped at 8 to prevent
// runaway. The emitting region does NOT receive its own emit.
//
// # View composition
//
// Parallel parent's `view:` (if any) renders first; each region's leaf view
// (in alphabetical order) is then appended with `\n\n` separators.
//
// # Effect propagation
//
// World is single-and-shared across regions. Regional transitions mutate
// it sequentially; `say:` outputs are concatenated.
package machine

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/expr"
	"kitsoki/internal/intent"
	"kitsoki/internal/render"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
	"kitsoki/internal/world"
)

// parallelSigil and regionSep are the encoding separators.  See the package
// doc-comment for the full grammar.
const (
	parallelSigil = "#"
	regionSep     = "|"
)

// maxEmitDepth caps how deeply emit-driven transitions may chain within a
// single turn before the machine bails out with an error.  Set generously
// so legitimate ~3-step cross-region chains pass; small enough that a YAML
// bug forming a cycle fails loudly.
const maxEmitDepth = 8

// EmitIntentMaxDepth bounds re-entrant emit_intent dispatches within one
// turn to prevent infinite chains. Matches the depth cap used for Emit
// (event) propagation above.
const EmitIntentMaxDepth = 8

// parsedParallel is the decoded form of a parallel-encoded state path.
//
// IsParallel is false for ordinary dotted paths — callers can fall through
// to the existing single-leaf logic.
type parsedParallel struct {
	IsParallel  bool
	Root        string   // parallel parent path (dotted)
	RegionLeaves []string // absolute dotted leaf path per region, alphabetical order
}

// parseParallel decodes a possibly parallel-encoded path.
//
// For an ordinary path "a.b.c" returns {IsParallel: false}.
// For "root#root.r1.x|root.r2.y" returns the decoded struct.
func parseParallel(p string) parsedParallel {
	idx := strings.Index(p, parallelSigil)
	if idx < 0 {
		return parsedParallel{IsParallel: false}
	}
	out := parsedParallel{
		IsParallel: true,
		Root:       p[:idx],
	}
	tail := p[idx+1:]
	if tail != "" {
		out.RegionLeaves = strings.Split(tail, regionSep)
	}
	return out
}

// encodeParallel builds a parallel-encoded path from a root and per-region leaves.
// Region leaves are sorted alphabetically for determinism.
func encodeParallel(root string, leaves []string) string {
	sorted := make([]string, len(leaves))
	copy(sorted, leaves)
	sort.Strings(sorted)
	return root + parallelSigil + strings.Join(sorted, regionSep)
}

// stripParallel returns the parallel-root for a parallel-encoded path,
// or the path itself for a plain dotted path.  Used by code that only
// needs the structural parent (e.g. lookupStateByPath, terminal checks).
func stripParallel(p string) string {
	if idx := strings.Index(p, parallelSigil); idx >= 0 {
		return p[:idx]
	}
	return p
}

// StripParallel is the exported form of stripParallel for orchestrator /
// store callers that need to resolve a possibly-parallel state path to
// the structural parent (for Terminal: checks etc.).
func StripParallel(p app.StatePath) app.StatePath {
	return app.StatePath(stripParallel(string(p)))
}

// IsParallelPath reports whether a path is parallel-encoded.
func IsParallelPath(p app.StatePath) bool {
	return strings.Contains(string(p), parallelSigil)
}

// regionNameForLeaf returns the region name (first segment under parallelRoot)
// for a given leaf path.  Helper for emit propagation (we want to skip the
// emitting region in cross-region delivery).
func regionNameForLeaf(parallelRoot, leaf string) string {
	if !strings.HasPrefix(leaf, parallelRoot+".") {
		return ""
	}
	rest := leaf[len(parallelRoot)+1:]
	if dot := strings.IndexByte(rest, '.'); dot >= 0 {
		return rest[:dot]
	}
	return rest
}

// ─── construction-time helpers ───────────────────────────────────────────────

// validateParallelStates walks the state tree and ensures every parallel
// state declares at least 2 children, has no `initial:` field, and each
// child is itself compound/atomic (not nested parallel — for the minimum
// viable PoC; nested parallel is a follow-up).
func validateParallelStates(prefix string, states map[string]*app.State) error {
	for name, s := range states {
		if s == nil {
			continue
		}
		path := joinStatePath(prefix, name)
		if s.Type == "parallel" {
			if len(s.States) < 2 {
				return fmt.Errorf("machine: parallel state %q must declare at least 2 child regions (got %d)", path, len(s.States))
			}
			if s.Initial != "" {
				return fmt.Errorf("machine: parallel state %q must not declare an initial: field — each region picks its own initial", path)
			}
			for childName, child := range s.States {
				if child == nil {
					continue
				}
				if child.Type == "parallel" {
					return fmt.Errorf("machine: parallel state %q child %q is itself type: parallel — nested parallel regions are not supported", path, childName)
				}
			}
		}
		if err := validateParallelStates(path, s.States); err != nil {
			return err
		}
	}
	return nil
}

// ─── runtime helpers ─────────────────────────────────────────────────────────

// findParallelAncestor walks from leafPath up the dotted path and returns
// the deepest parallel-typed ancestor (if any).
//
// Returns ("", false) when no ancestor is parallel.
func (m *machineImpl) findParallelAncestor(leafPath string) (string, bool) {
	path := leafPath
	for path != "" {
		cs, ok := m.states[path]
		if ok && cs.s != nil && cs.s.Type == "parallel" {
			return path, true
		}
		idx := strings.LastIndexByte(path, '.')
		if idx < 0 {
			break
		}
		path = path[:idx]
	}
	return "", false
}

// resolveParallelEntry takes a parallel state's path and returns the
// encoded parallel state-path with each region resolved to its initial leaf.
func (m *machineImpl) resolveParallelEntry(parallelPath string, env expr.Env) (string, error) {
	cs, ok := m.states[parallelPath]
	if !ok || cs.s == nil || cs.s.Type != "parallel" {
		return "", fmt.Errorf("resolveParallelEntry: %q is not a parallel state", parallelPath)
	}
	regionNames := sortedRegionNames(cs.s.States)
	leaves := make([]string, 0, len(regionNames))
	for _, name := range regionNames {
		regionPath := joinStatePath(parallelPath, name)
		leaf, err := m.resolveInitial(regionPath, env)
		if err != nil {
			return "", fmt.Errorf("region %q initial: %w", regionPath, err)
		}
		leaves = append(leaves, leaf)
	}
	return encodeParallel(parallelPath, leaves), nil
}

// sortedRegionNames returns child-state names of a parallel state in
// alphabetical order — the canonical evaluation order.
func sortedRegionNames(states map[string]*app.State) []string {
	names := make([]string, 0, len(states))
	for k := range states {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// ─── parallel-aware overrides used from Turn ─────────────────────────────────

// resolveInitialAware resolves a target state to its leaf, handling parallel
// states by descending into each region.  For non-parallel targets it
// delegates to resolveInitial.
func (m *machineImpl) resolveInitialAware(path string, env expr.Env) (string, error) {
	cs, ok := m.states[path]
	if !ok {
		return path, nil
	}
	if cs.s != nil && cs.s.Type == "parallel" {
		return m.resolveParallelEntry(path, env)
	}
	// Walk down: if the literal `initial:` resolved leaf is itself a parallel
	// state, expand it too.
	resolved, err := m.resolveInitial(path, env)
	if err != nil {
		return "", err
	}
	if resolved != path {
		// Could be parallel — recheck.
		if rcs, ok := m.states[resolved]; ok && rcs.s != nil && rcs.s.Type == "parallel" {
			return m.resolveParallelEntry(resolved, env)
		}
	}
	return resolved, nil
}

// ─── parallel-state Turn ─────────────────────────────────────────────────────

// turnParallel handles intent dispatch when the current state is parallel.
//
// Walk regions in alphabetical order; the first region whose state chain has
// a binding for the intent (with a passing guard) wins. The other regions
// keep their current leaves. After the winning region transitions, propagate
// any emitted events to the OTHER regions (one round, depth-capped).
func (m *machineImpl) turnParallel(ctx context.Context, par parsedParallel, w world.World, call intent.IntentCall) (TurnResult, error) {
	parRoot := par.Root
	parCS, ok := m.states[parRoot]
	if !ok || parCS.s == nil || parCS.s.Type != "parallel" {
		return TurnResult{}, fmt.Errorf("turnParallel: state %q is not parallel", parRoot)
	}

	// Index leaves by region for quick lookup/update.
	regionNames := sortedRegionNames(parCS.s.States)
	leavesByRegion := make(map[string]string, len(par.RegionLeaves))
	for _, leaf := range par.RegionLeaves {
		rn := regionNameForLeaf(parRoot, leaf)
		if rn != "" {
			leavesByRegion[rn] = leaf
		}
	}
	encodedCur := encodeParallel(parRoot, par.RegionLeaves)

	// Standard Validate: now parallel-aware (allowedIntentNames /
	// lookupIntent walk every region leaf for an encoded path).
	vr := m.Validate(app.StatePath(encodedCur), w, call)
	if !vr.OK {
		return TurnResult{
			NewState:        app.StatePath(encodedCur),
			World:           w,
			ValidationError: vr.Err,
			Events: []store.Event{
				newEvent(store.ValidationFailed, map[string]any{
					"code":    string(vr.Err.Code),
					"message": vr.Err.Message,
					"intent":  call.Intent,
					"state":   parRoot,
				}),
			},
		}, nil
	}

	// Build the eval env.
	env := expr.Env{
		Slots: slotsToMap(call.Slots),
		World: w.Vars,
		Event: make(map[string]any),
	}

	// ─── First-region-wins guard walk ─────────────────────────────────────
	var winningRegion string
	var winningTr *compiledTransition
	var winningPath string
	var firstHint string
	for _, rn := range regionNames {
		leaf := leavesByRegion[rn]
		if leaf == "" {
			continue
		}
		tr, path, hint, err := m.findTransitionTraced(ctx, leaf, call.Intent, env)
		if err != nil {
			return TurnResult{}, err
		}
		if tr != nil {
			winningRegion = rn
			winningTr = tr
			winningPath = path
			break
		}
		if firstHint == "" && hint != "" {
			firstHint = hint
		}
	}
	if winningTr == nil {
		ve := &intent.ValidationError{
			Code:      intent.ErrGuardFailed,
			Message:   fmt.Sprintf("no transition matched for intent %q in parallel state %q", call.Intent, parRoot),
			GuardHint: firstHint,
		}
		return TurnResult{
			NewState:        app.StatePath(encodeParallel(parRoot, par.RegionLeaves)),
			World:           w,
			ValidationError: ve,
			Events: []store.Event{
				newEvent(store.ValidationFailed, map[string]any{
					"code":       string(ve.Code),
					"intent":     call.Intent,
					"state":      parRoot,
					"guard_hint": firstHint,
				}),
			},
		}, nil
	}

	// Resolve the target.
	rawTarget := winningTr.tr.Target
	if strings.Contains(rawTarget, "{{") {
		rendered, rerr := render.Pongo(rawTarget, env)
		if rerr != nil {
			return TurnResult{}, fmt.Errorf("render transition target %q: %w", rawTarget, rerr)
		}
		rawTarget = strings.TrimSpace(rendered)
	}
	targetPath := resolveTarget(winningPath, rawTarget)
	resolvedTarget, err := m.resolveInitialAware(targetPath, env)
	if err != nil {
		return TurnResult{}, fmt.Errorf("resolve initial for %q: %w", targetPath, err)
	}

	// The winning region's leaf becomes `resolvedTarget` (which must still
	// belong inside the same parallel parent — we don't allow a regional
	// transition to escape the parallel state in this MV implementation).
	// If a region transitions outside its parallel parent the new leaf path
	// loses the parallel encoding; this is permitted (the whole parallel
	// state is exited).
	exitedParallel := !strings.HasPrefix(resolvedTarget, parRoot+".") && resolvedTarget != parRoot

	m.logger.DebugContext(ctx, "machine.parallel.transition",
		slog.String("region", winningRegion),
		slog.String("from", leavesByRegion[winningRegion]),
		slog.String("to", resolvedTarget),
		slog.String("intent", call.Intent),
	)

	// Apply the winning transition's effects.
	// emit_intent: synthetic dispatch from a parallel-encoded state is
	// not supported in this initial implementation (parallel regions
	// have their own event-bus via propagateEmits; mixing the two
	// would muddle depth-cap accounting). Any emits captured here are
	// dropped with a warning trace — the loader still validates the
	// intent reference, but the dispatch is a no-op.
	newWorld, hostCalls, saySB, effectEvents, parEmits, err := m.applyEffectsTraced(ctx, winningTr.tr.Effects, w, env)
	if err != nil {
		return TurnResult{}, err
	}
	if len(parEmits) > 0 {
		for _, em := range parEmits {
			m.logger.WarnContext(ctx, trace.EvIntentEmitParallelDropped,
				slog.String("site", "turn_parallel_transition"),
				slog.String("intent", em.Name),
				slog.String("state", parRoot),
			)
		}
	}

	// Build the new state-path (handling the exitedParallel case).
	var newStatePath string
	updatedLeavesByRegion := make(map[string]string, len(leavesByRegion))
	for k, v := range leavesByRegion {
		updatedLeavesByRegion[k] = v
	}
	if exitedParallel {
		newStatePath = resolvedTarget
	} else {
		updatedLeavesByRegion[winningRegion] = resolvedTarget
		newLeaves := make([]string, 0, len(updatedLeavesByRegion))
		for _, rn := range regionNames {
			if l, ok := updatedLeavesByRegion[rn]; ok && l != "" {
				newLeaves = append(newLeaves, l)
			}
		}
		newStatePath = encodeParallel(parRoot, newLeaves)
	}

	// Fire on_enter for newly-entered states within the winning region.
	if !exitedParallel && resolvedTarget != leavesByRegion[winningRegion] {
		entered := stateEnterPaths(leavesByRegion[winningRegion], resolvedTarget)
		for _, ep := range entered {
			cs, ok := m.states[ep]
			if !ok || cs.s == nil || len(cs.s.OnEnter) == 0 {
				continue
			}
			enterEnv := expr.Env{Slots: env.Slots, World: newWorld.Vars, Event: env.Event, Run: env.Run}
			nw2, hcs2, ssb2, ev2, parEnterEmits, eerr := m.applyEffectsTraced(ctx, cs.s.OnEnter, newWorld, enterEnv)
			if eerr != nil {
				return TurnResult{}, fmt.Errorf("on_enter effects for %q: %w", ep, eerr)
			}
			if len(parEnterEmits) > 0 {
				for _, em := range parEnterEmits {
					m.logger.WarnContext(ctx, trace.EvIntentEmitParallelDropped,
						slog.String("site", "turn_parallel_on_enter"),
						slog.String("intent", em.Name),
						slog.String("state", ep),
					)
				}
			}
			newWorld = nw2
			hostCalls = append(hostCalls, hcs2...)
			if ssb2.Len() > 0 {
				if saySB.Len() > 0 {
					saySB.WriteString("\n")
				}
				saySB.WriteString(ssb2.String())
			}
			effectEvents = append(effectEvents, ev2...)
		}
	}

	// ─── Emit propagation ────────────────────────────────────────────────
	// Collect explicit emit names from the winning transition.
	emitNames := collectEmitNames(winningTr.tr.Effects)
	for _, name := range winningTr.tr.Emit {
		if name != "" {
			emitNames = append(emitNames, name)
		}
	}

	if !exitedParallel && len(emitNames) > 0 {
		propagated, npw, nhcs, ssbExtra, evExtra, perr := m.propagateEmits(
			ctx, parRoot, regionNames, updatedLeavesByRegion, winningRegion,
			emitNames, newWorld, env, 0,
		)
		if perr != nil {
			return TurnResult{}, perr
		}
		newWorld = npw
		hostCalls = append(hostCalls, nhcs...)
		if ssbExtra != "" {
			if saySB.Len() > 0 {
				saySB.WriteString("\n")
			}
			saySB.WriteString(ssbExtra)
		}
		effectEvents = append(effectEvents, evExtra...)
		// Re-encode now that other regions may have moved.
		newLeaves := make([]string, 0, len(propagated))
		for _, rn := range regionNames {
			if l, ok := propagated[rn]; ok && l != "" {
				newLeaves = append(newLeaves, l)
			}
		}
		newStatePath = encodeParallel(parRoot, newLeaves)
	}

	// Render the view.  For parallel states, compose parent + each region's
	// leaf view (alphabetical).  Skip the render when host calls with binds
	// are queued; the orchestrator will re-render against the post-bind
	// world (see Turn for the full rationale).
	var renderedView string
	if !hostCallsWillBind(hostCalls) {
		var rErr error
		renderedView, rErr = m.renderViewParallel(winningTr.tr, parRoot, regionNames, newStatePath, newWorld, env, saySB.String(), exitedParallel, resolvedTarget)
		if rErr != nil {
			return TurnResult{}, rErr
		}
	}

	// Build events: TransitionApplied + EffectApplied* + StateExited/Entered.
	var events []store.Event
	events = append(events, newEvent(store.TransitionApplied, map[string]any{
		"from":   encodeParallel(parRoot, par.RegionLeaves),
		"to":     newStatePath,
		"intent": call.Intent,
		"slots":  map[string]any(call.Slots),
	}))
	events = append(events, effectEvents...)

	// Compute the exit/enter event list within the winning region only.
	if !exitedParallel {
		oldLeaf := leavesByRegion[winningRegion]
		if oldLeaf != resolvedTarget && oldLeaf != "" {
			for _, p := range stateExitPaths(oldLeaf, resolvedTarget) {
				events = append(events, newEvent(store.StateExited, map[string]any{"state": p}))
			}
			for _, p := range stateEnterPaths(oldLeaf, resolvedTarget) {
				events = append(events, newEvent(store.StateEntered, map[string]any{"state": p}))
			}
		}
	} else {
		// Whole parallel state was exited.  Emit a coarse exit for the parallel root,
		// and standard enter events for the new path.
		events = append(events, newEvent(store.StateExited, map[string]any{"state": parRoot}))
		for _, p := range stateEnterPaths(parRoot, resolvedTarget) {
			events = append(events, newEvent(store.StateEntered, map[string]any{"state": p}))
		}
	}

	// Menu = union of allowed intents across all final regions (when still parallel).
	var newMenu []string
	if exitedParallel {
		newMenu = m.allowedIntentNames(app.StatePath(resolvedTarget))
	} else {
		seen := make(map[string]struct{})
		par := parseParallel(newStatePath)
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
	}

	return TurnResult{
		NewState:  app.StatePath(newStatePath),
		World:     newWorld,
		View:      renderedView,
		Menu:      newMenu,
		Events:    events,
		HostCalls: hostCalls,
	}, nil
}

// collectEmitNames extracts emit events declared on Invoke effects.
// (Used because emit currently rides on host invocations — see Effect.Emit.)
func collectEmitNames(effects []app.Effect) []string {
	var out []string
	for _, e := range effects {
		if e.Emit != "" {
			out = append(out, e.Emit)
		}
	}
	return out
}

// propagateEmits fires the named events as virtual intents in every region
// OTHER than `originRegion`.  A region only reacts to an event if its
// current leaf (or ancestor) has an `on: <event_name>:` binding.  Chains
// further emits up to maxEmitDepth deep.
func (m *machineImpl) propagateEmits(
	ctx context.Context,
	parRoot string,
	regionNames []string,
	leavesByRegion map[string]string,
	originRegion string,
	emits []string,
	w world.World,
	env expr.Env,
	depth int,
) (map[string]string, world.World, []HostInvocation, string, []store.Event, error) {
	if depth >= maxEmitDepth {
		return nil, w, nil, "", nil, fmt.Errorf("parallel emit propagation exceeded max depth (%d) — check for cyclic emits", maxEmitDepth)
	}
	updated := make(map[string]string, len(leavesByRegion))
	for k, v := range leavesByRegion {
		updated[k] = v
	}
	var allHostCalls []HostInvocation
	var saySB strings.Builder
	var events []store.Event
	curWorld := w

	for _, emitName := range emits {
		// Reset chain of further emits collected this iteration.
		var nextEmits []string
		var nextOrigins []string // parallel-track origin per emit

		for _, rn := range regionNames {
			if rn == originRegion {
				continue // skip the emitter itself
			}
			leaf := updated[rn]
			if leaf == "" {
				continue
			}
			emitEnv := expr.Env{Slots: env.Slots, World: curWorld.Vars, Event: env.Event, Run: env.Run}
			tr, path, _, err := m.findTransitionTraced(ctx, leaf, emitName, emitEnv)
			if err != nil {
				return nil, w, nil, "", nil, err
			}
			if tr == nil {
				continue
			}
			rawTarget := tr.tr.Target
			if strings.Contains(rawTarget, "{{") {
				rendered, rerr := render.Pongo(rawTarget, emitEnv)
				if rerr != nil {
					return nil, w, nil, "", nil, fmt.Errorf("emit %q: render target: %w", emitName, rerr)
				}
				rawTarget = strings.TrimSpace(rendered)
			}
			tgt := resolveTarget(path, rawTarget)
			resolved, rerr := m.resolveInitialAware(tgt, emitEnv)
			if rerr != nil {
				return nil, w, nil, "", nil, fmt.Errorf("emit %q: resolveInitial: %w", emitName, rerr)
			}
			nw, hcs, ssb, evs, _, eerr := m.applyEffectsTraced(ctx, tr.tr.Effects, curWorld, emitEnv)
			if eerr != nil {
				return nil, w, nil, "", nil, fmt.Errorf("emit %q: apply effects: %w", emitName, eerr)
			}
			curWorld = nw
			allHostCalls = append(allHostCalls, hcs...)
			if ssb.Len() > 0 {
				if saySB.Len() > 0 {
					saySB.WriteString("\n")
				}
				saySB.WriteString(ssb.String())
			}
			events = append(events, evs...)
			// on_enter for entered ancestors.
			if leaf != resolved {
				entered := stateEnterPaths(leaf, resolved)
				for _, ep := range entered {
					cs, ok := m.states[ep]
					if !ok || cs.s == nil || len(cs.s.OnEnter) == 0 {
						continue
					}
					eEnv := expr.Env{Slots: env.Slots, World: curWorld.Vars, Event: env.Event, Run: env.Run}
					nw2, hcs2, ssb2, evs2, _, e2 := m.applyEffectsTraced(ctx, cs.s.OnEnter, curWorld, eEnv)
					if e2 != nil {
						return nil, w, nil, "", nil, fmt.Errorf("emit %q on_enter for %q: %w", emitName, ep, e2)
					}
					curWorld = nw2
					allHostCalls = append(allHostCalls, hcs2...)
					if ssb2.Len() > 0 {
						if saySB.Len() > 0 {
							saySB.WriteString("\n")
						}
						saySB.WriteString(ssb2.String())
					}
					events = append(events, evs2...)
				}
				// Synthesize exit/enter events for the receiving region.
				for _, p := range stateExitPaths(leaf, resolved) {
					events = append(events, newEvent(store.StateExited, map[string]any{"state": p}))
				}
				for _, p := range stateEnterPaths(leaf, resolved) {
					events = append(events, newEvent(store.StateEntered, map[string]any{"state": p}))
				}
			}
			updated[rn] = resolved
			// Collect chained emits from this transition.
			chain := collectEmitNames(tr.tr.Effects)
			for _, n := range tr.tr.Emit {
				if n != "" {
					chain = append(chain, n)
				}
			}
			for _, c := range chain {
				nextEmits = append(nextEmits, c)
				nextOrigins = append(nextOrigins, rn)
			}
		}

		// Process chained emits one-at-a-time so each respects "skip my own region".
		for i, c := range nextEmits {
			subUpdated, subWorld, subHCs, subSay, subEvs, subErr := m.propagateEmits(
				ctx, parRoot, regionNames, updated, nextOrigins[i],
				[]string{c}, curWorld, env, depth+1,
			)
			if subErr != nil {
				return nil, w, nil, "", nil, subErr
			}
			updated = subUpdated
			curWorld = subWorld
			allHostCalls = append(allHostCalls, subHCs...)
			if subSay != "" {
				if saySB.Len() > 0 {
					saySB.WriteString("\n")
				}
				saySB.WriteString(subSay)
			}
			events = append(events, subEvs...)
		}
	}
	return updated, curWorld, allHostCalls, saySB.String(), events, nil
}

// renderViewParallel composes views for a parallel state-turn.
//
// When the turn exited the parallel state, fall back to the standard
// (transition-view-or-target-state-view) renderer.  Otherwise:
//
//	[transition view (if declared)] OR
//	[parallel parent view] +
//	  "\n\n" + region_a leaf view + "\n\n" + region_b leaf view + ...
func (m *machineImpl) renderViewParallel(tr app.Transition, parRoot string, regionNames []string, encodedNewPath string, w world.World, env expr.Env, sayText string, exitedParallel bool, resolvedTarget string) (string, error) {
	// Build a new env with the updated world and computed menu (see
	// renderView for the rationale). Menu is computed against the encoded
	// parallel path so it reflects the union of region menus.
	renderEnv := expr.Env{
		Slots: env.Slots,
		World: w.Vars,
		Event: env.Event,
		Run:   env.Run,
		Menu:  MenuToTemplateMap(m.Menu(app.StatePath(encodedNewPath), w)),
		State: stateMetaFor(m, app.StatePath(resolvedTarget)),
	}
	expr.PopulateMenuHelpers(&renderEnv)
	if exitedParallel {
		// Standard single-state render.
		return m.renderView(tr, resolvedTarget, w, env, sayText)
	}

	var sb strings.Builder

	// 1. Transition view wins outright if declared.
	if !tr.View.IsEmpty() {
		v, err := m.renderViewBody(tr.View, renderEnv, resolvedTarget)
		if err != nil {
			return "", fmt.Errorf("render transition view: %w", err)
		}
		sb.WriteString(v)
	} else {
		// 2. Otherwise, parent view (if any) + each region's leaf view.
		// Each leaf renders with its own State metadata so per-region
		// {{ state.* }} references resolve locally.
		if parCS, ok := m.states[parRoot]; ok && parCS.s != nil && !parCS.s.View.IsEmpty() {
			parentEnv := renderEnv
			parentEnv.State = stateMetaFor(m, app.StatePath(parRoot))
			v, err := m.renderViewBody(parCS.s.View, parentEnv, parRoot)
			if err != nil {
				return "", fmt.Errorf("render parallel parent view %q: %w", parRoot, err)
			}
			sb.WriteString(v)
		}
		par := parseParallel(encodedNewPath)
		// Iterate alphabetically to match encoding.
		for _, leaf := range par.RegionLeaves {
			cs, ok := m.states[leaf]
			if !ok || cs.s == nil || cs.s.View.IsEmpty() {
				continue
			}
			leafEnv := renderEnv
			leafEnv.State = stateMetaFor(m, app.StatePath(leaf))
			v, err := m.renderViewBody(cs.s.View, leafEnv, leaf)
			if err != nil {
				return "", fmt.Errorf("render region leaf view %q: %w", leaf, err)
			}
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(v)
		}
	}

	viewText := sb.String()
	if sayText != "" && viewText != "" {
		return sayText + "\n\n" + viewText, nil
	}
	if sayText != "" {
		return sayText, nil
	}
	return viewText, nil
}

