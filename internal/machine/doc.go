// Package machine implements the pure, deterministic state-machine core that
// sits at the centre of the orchestrator. It owns nothing but the rules: given
// a current [app.StatePath], a [world.World] snapshot, and an
// [intent.IntentCall], it computes the next state, the events that turn
// produced, and the side-effect calls the orchestrator must dispatch — without
// performing any I/O itself. Its consumers are the orchestrator (which wraps it
// for the MCP server and TUI), the replay harness, and tests.
//
// # Algorithm
//
// The machine is a pure function of (state, world, intent). A [Machine.Turn]
// proceeds in a fixed sequence:
//
//  1. Validate the call against the current state's allowed intents and slot
//     schema. A rejected call returns a [TurnResult] whose ValidationError is
//     set and whose NewState/World equal the inputs unchanged.
//  2. Pick the winning transition. Guard arms (`when:` / `default:`) are
//     evaluated in declaration order; the first that passes wins.
//  3. Walk the winning transition's effect chain on a single mutable world
//     clone: `set:`/`increment:` mutate the world, `say:` accumulates text,
//     `invoke:` is collected as a [HostInvocation] (never dispatched here),
//     `emit_intent:` may chain a synthetic transition within the same turn.
//  4. Resolve the target state to its leaf (descending compound `initial:`
//     and expanding parallel regions) and render its view.
//
// Because the machine never touches the network, filesystem, clock, or LLM, a
// turn is fully reproducible: the same inputs always yield the same
// [TurnResult] and event sequence. That is what makes replay and audit
// possible — see [Non-goals] for the deliberate boundary.
//
// # Invariants
//
// Event ordering within a turn follows the natural causal order:
//
//	IntentAccepted → ValidationFailed (if rejected, stop) |
//	TransitionApplied → EffectApplied* → StateExited* → StateEntered*
//
// The machine emits only the events that result from evaluating a single
// IntentCall. It does NOT emit TurnStarted / TurnEnded — those are
// orchestrator-level events.
//
// Guard-hint policy (ambiguity): when multiple guarded transitions fail, the
// machine returns the guard_hint from the *first* failing transition. This
// "first-guard-wins" ordering surfaces the most-specific branch (the one the
// author wrote first) as the explanation for why the player is stuck.
//
// View precedence: if the winning transition declares a `view:`, it is
// rendered and returned, and the target state's view is NOT additionally
// appended (it would otherwise be shown again on the next look or re-entry).
// If the transition has no `view:`, only the target state's view is rendered.
// Authors who want both write both into the transition view.
//
// # Worked example
//
// A two-state app with a single forward intent, driven one turn:
//
//	def := &app.AppDef{
//	    Root: "start",
//	    Intents: {"proceed": {Title: "Proceed"}},
//	    States: {
//	        "start":  {On: {"proceed": [{Target: "finish"}]}},
//	        "finish": {Terminal: true, View: "You have finished."},
//	    },
//	}
//	m, _ := machine.New(def)
//	res, _ := m.Turn(ctx, "start", world.New(), intent.IntentCall{Intent: "proceed"})
//
//	res.NewState        == "finish"
//	res.View            == "You have finished."
//	res.ValidationError == nil
//	res.Events          == [IntentAccepted, TransitionApplied, StateExited, StateEntered]
//
// A runnable form of this trace lives in [ExampleNew]. The guarded-menu trace
// lives in [ExampleMachine_menu], and parallel-path detection in
// [ExampleIsParallelPath].
//
// # Lifecycle
//
// [New] compiles an [app.AppDef] into a [Machine] once, at machine-load time:
// it pre-compiles every guard, view template, and guard hint, and validates
// every parallel state, returning a joined error that lists all compilation
// failures at once. The returned Machine holds no per-turn mutable state — its
// query methods ([Machine.AllowedIntents], [Machine.Menu], [Machine.TryGuards])
// are read-only over the (state, world) snapshot they are handed and so are
// safe to call concurrently. [Machine.Turn] likewise reads the AppDef and the
// passed world clone; it returns a new world rather than mutating the caller's.
//
// # Parallel states
//
// `type: parallel` is supported with minimum-viable semantics: state-path
// encoding, first-region-wins intent dispatch, and depth-capped emit
// propagation across sibling regions. See parallel.go for the full design
// notes, [IsParallelPath]/[StripParallel] for the encoding helpers, and the
// "Parallel state" section of docs/stories/state-machine.md for the
// author-facing model.
//
// # Non-goals
//
//   - No I/O. The machine never reads or writes the filesystem, the network,
//     the clock, or the LLM. `invoke:` effects are returned as
//     [HostInvocation] values for the orchestrator to dispatch — keeping the
//     core pure is what makes turns reproducible for replay and audit.
//   - No state persistence. The machine returns the next state and world; it
//     is the orchestrator and store that journal and reload them. A Machine
//     keeps nothing between turns.
//   - No LLM integration. Semantic intent matching is the routing tier's job
//     (see [kitsoki/internal/semroute]); the machine receives an already-
//     resolved [intent.IntentCall].
//   - No turn-level lifecycle events. TurnStarted / TurnEnded are owned by the
//     orchestrator; the machine emits only the events caused by evaluating one
//     IntentCall.
//   - No full SCXML parallel semantics. Parallel dispatch is first-region-wins,
//     deliberately weaker than SCXML, because that is sufficient for the
//     non-overlapping-region use cases the PoC targets.
//
// # Reference
//
// The author-facing model for rooms, transitions, guards, effects, parallel
// states, and the turn loop is docs/stories/state-machine.md. The orchestrator
// boundary the machine sits behind — including how host calls flow back in via
// the chained-rerender contract — is in docs/architecture/overview.md.
package machine
