package orchestrator

import (
	"sort"
	"strings"

	"kitsoki/internal/app"
)

// workbenchGateSignal is room-workbench Task 1.4(b): the S6 usable-kitsoki-gate
// producer contract (docs/tracing/usable-kitsoki-gate.md, "Producer contract —
// what S1 must emit"). Per that doc's Decision-recording philosophy (no new
// event kind), this is carried as an additional key on the EXISTING
// store.TurnEnded ("turn.end") payload — usable_kitsoki_gate — rather than a
// new trace event.
//
// Field names deliberately mirror
// tools/arena/arena/plugins/usable_kitsoki_gate_schema.json's per-scenario
// parity-verdict-record properties (candidate_completed, silent_bounce,
// misroute_adjacent, evidence_refs) so a downstream S6 rollup can read this
// turn's signal with the same field names it already knows, per that schema's
// candidate_completed doc: "Read off S1's per-scenario-turn completion signal
// keyed to the scenario IR's expected_effects list — see
// docs/tracing/usable-kitsoki-gate.md '#producer-contract'".
//
// dispatchingState identifies which room's on_enter this turn's host calls
// belonged to (the machine's transition target BEFORE any on_error redirect —
// callers capture result.NewState right after machine.Turn, before
// dispatchHostCalls's hostRedirect return value can reassign it). This
// function looks up that state directly rather than scanning this turn's
// store.Event slice for agent.call.* events, because every agent.call.* event
// (both live dispatch, internal/host/agent_event_sink.go, and cassette replay,
// internal/testrunner/cassette.go's writeCassetteAgentEvents) is appended
// straight to the run's EventSink out-of-band from the batch orchestrator
// assembles into result.Events/successEvents — it is never present in the
// slice a turn.end builder has in hand. State identity is the one thing that
// IS reliably in scope at every transitionedTurnEndWithGateSignal call site.
//
// HONESTY NOTE (was a documented gap; now a real, engine-computed join for
// workbench rooms that opt in): the full producer contract asks S1 to key
// candidate_completed to a scenario's expected_effects list (S4's scenario
// IR, docs/proposals/scenario-foundry.md). This function now performs that
// join itself when the dispatching room's world carries the convention key
// `<noteKey-without-"_note">_expected_effects` (a `[]string`-shaped world var
// — see room-workbench's S6 projection, tools/session-mining/
// flow_fixture_compiler.py's real-workbench target) with at least one entry:
// candidate_completed is then true iff dispatchFailed is false AND every
// entry is found (case-insensitively) as a substring somewhere in the
// workbench's own bound close-out note (the `submitted` object bound to
// `<room>_note` by the synthesized on_enter, walked recursively over its
// string leaves) — i.e. the SAME note content a real workbench dispatch (or
// its no-LLM cassette stand-in) actually produced, not a value fabricated
// here. A scenario whose stub note omits an expected effect fails this check
// honestly (candidate_completed = false) even when dispatchFailed is false.
//
// When no such `_expected_effects` world var is present (the overwhelming
// majority of turns — ordinary workbench use, and every pre-S6 fixture),
// this falls back to the original, narrower proxy, UNCHANGED:
//
//   - candidate_completed: true iff dispatchFailed is false — i.e. the
//     workbench room's synthesized on_enter host.agent.task call this turn
//     did not take its on_error redirect. This is a necessary-but-not-
//     sufficient proxy for "the source session's ask got done" — it answers
//     "did the workbench's own dispatch succeed", not "did it satisfy the
//     scenario's expected_effects".
//   - silent_bounce: true iff dispatchFailed is true AND the turn's final
//     rendered view is empty. In this codebase that should always evaluate
//     false, by construction, thanks to the RunIntent/RunIntentWithInput
//     on_error safety net (see the "Usability safety-net" comment near each
//     of this function's call sites) which forces a rendered explanation
//     onto every on_error redirect before turn.end is built — this field is
//     S2's regression test at scale, per the tracing doc's own framing, not
//     something S1 is meant to make happen.
//   - misroute_adjacent: always false. NOT computed — there is no way to know
//     "the scenario's expected command" without S4's IR. This is an explicit,
//     documented absence (an honest "not detected here"), never a fabricated
//     true/false claim about routing quality.
//   - evidence_refs: always empty. A real parity record's evidence_refs point
//     at durable per-run artifacts (trace.jsonl / rrweb.json paths) that only
//     exist once a scenario harness assembles a run directory; a single
//     mid-session turn.end payload has no such path to point at. The schema's
//     own minItems:1 constraint is NOT satisfied by this turn-level signal on
//     purpose — it is raw per-turn material for S6's rollup to combine with
//     real evidence paths, not a standalone parity record.
//
// Returns nil when dispatchingState is not a workbench: room (the
// overwhelming majority of turns in any app, workbench or not) so ordinary
// turn.end payloads are byte-identical to before this change.
//
// world is the post-dispatch world snapshot (result.World.Vars) — used ONLY
// to look up the optional `<room>_expected_effects` join input and the
// workbench's own bound close-out note; never mutated. def.LookupState
// (rather than a flat def.States[...] index) is required here because a
// workbench room imported into a parent story (pets-dev, slidey-dev — see
// stories/dev-story/rooms/landing.yaml's import-folding doc) lives at a
// dotted, compound-wrapped path like "core.landing", not a top-level key.
func workbenchGateSignal(def *app.AppDef, dispatchingState app.StatePath, dispatchFailed bool, view string, world map[string]any) map[string]any {
	if def == nil || dispatchingState == "" {
		return nil
	}
	st, ok := def.LookupState(dispatchingState)
	if !ok || st == nil || st.Workbench == nil {
		return nil
	}

	candidateCompleted := !dispatchFailed
	if noteKey, ok := workbenchNoteKey(st); ok {
		if expected := expectedEffectsFor(noteKey, world); len(expected) > 0 {
			// Real join: candidate_completed requires BOTH a clean dispatch
			// AND every expected effect actually showing up in the note the
			// dispatch (or its no-LLM cassette stand-in) produced — never
			// hardcoded true. See the doc comment above for the full
			// contract and its documented fallback.
			candidateCompleted = !dispatchFailed && noteCoversEffects(world[noteKey], expected)
		}
	}
	silentBounce := dispatchFailed && strings.TrimSpace(view) == ""

	return map[string]any{
		"usable_kitsoki_gate": map[string]any{
			"candidate_completed": candidateCompleted,
			"silent_bounce":       silentBounce,
			"misroute_adjacent":   false,
			"evidence_refs":       []string{},
		},
	}
}

// workbenchNoteKey returns the world key the workbench's synthesized
// on_enter binds its close-out note to (`<room>_note` before any import
// folding, `<alias>__<room>_note` after — see imports_rewriter.go's Bind
// rewrite). Rather than recomputing that name from the state's own leaf
// path (which would need to know whether/how far this state was folded
// under an import alias), this reads it straight off the already-folded
// synthesized effect itself: internal/app/workbench.go always appends
// exactly one on_enter Effect invoking host.agent.task with a single-entry
// Bind map — whatever survived folding is the real, current name.
func workbenchNoteKey(st *app.State) (string, bool) {
	for _, e := range st.OnEnter {
		if e.Invoke != "host.agent.task" || len(e.Bind) == 0 {
			continue
		}
		for k := range e.Bind {
			return k, true
		}
	}
	return "", false
}

// expectedEffectsFor looks up the optional S6 join input: a world var named
// by convention `<room>_expected_effects` (derived from noteKey by swapping
// its "_note" suffix), declared as a `type: array` of strings alongside a
// workbench room's other world vars (see stories/dev-story/app.yaml). Import
// folding renames both `<room>_note` and `<room>_expected_effects` by the
// same alias__ prefix, so deriving one from the other stays correct at any
// nesting depth. Returns nil when absent/empty/wrong-shaped — the signal
// falls back to the pre-S6 dispatch-only proxy in that case.
func expectedEffectsFor(noteKey string, world map[string]any) []string {
	if world == nil || !strings.HasSuffix(noteKey, "_note") {
		return nil
	}
	key := strings.TrimSuffix(noteKey, "_note") + "_expected_effects"
	raw, ok := world[key]
	if !ok || raw == nil {
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		if strs, ok := raw.([]string); ok {
			items = make([]any, len(strs))
			for i, s := range strs {
				items[i] = s
			}
		} else {
			return nil
		}
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// noteCoversEffects reports whether every entry in expected is present
// (case-insensitively, substring match) somewhere among note's string
// leaves. note is whatever the workbench's Bind landed in world at noteKey
// — normally the `submitted` object a real agent (or its cassette stand-in)
// returned, an arbitrary-shaped map. This never invents a verdict: an effect
// missing from the note's own text fails the check, honestly.
func noteCoversEffects(note any, expected []string) bool {
	haystack := strings.ToLower(collectStrings(note))
	for _, want := range expected {
		if !strings.Contains(haystack, strings.ToLower(want)) {
			return false
		}
	}
	return true
}

// collectStrings flattens every string leaf reachable from v (maps, slices,
// scalars) into one newline-joined blob for a cheap, deterministic substring
// search — no schema knowledge of the note's shape required.
func collectStrings(v any) string {
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case string:
			b.WriteString(t)
			b.WriteByte('\n')
		case map[string]any:
			for _, k := range sortedMapKeys(t) {
				walk(t[k])
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return b.String()
}

// sortedMapKeys returns m's keys sorted, for a deterministic walk order.
func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
