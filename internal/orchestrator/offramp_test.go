package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/chathost"
	"kitsoki/internal/chats"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/intent"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// offRampNoopHarness is a zero-behavior Harness; maybeOffRamp is exercised
// directly, so the harness is never asked to route. (Sibling of the black-box
// noopHarness in hostdispatch_test.go, redeclared for the white-box package.)
type offRampNoopHarness struct{}

func (offRampNoopHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	return mcp.CallToolParams{}, nil
}
func (offRampNoopHarness) Close() error { return nil }

// offRampRoutingHarness is a Harness that always routes to a fixed intent name,
// modeling the LLM's classification deterministically. Pointing it at an intent
// the app declares nowhere drives a genuine UNKNOWN_INTENT no-match through the
// real Turn() entry point — the exact flow maybeOffRamp intercepts.
// offRampNoMatchIntent is the sentinel intent name the routing harness emits
// for an unmappable utterance; offRampNoMatchMachine translates a Turn() for it
// into a genuine INTENT_UNKNOWN no-match (the code the off-ramp intercepts).
const offRampNoMatchIntent = "__no_match__"

// offRampRoutingHarness routes the priming utterance "look" to the real `look`
// intent (so the first foreground turn materializes the root state into the
// journey) and every other utterance to offRampNoMatchIntent — which the
// paired offRampNoMatchMachine turns into an INTENT_UNKNOWN no-match.
type offRampRoutingHarness struct{}

func (offRampRoutingHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	name := offRampNoMatchIntent
	if in.UserText == "look" {
		name = "look"
	}
	return mcp.CallToolParams{
		Name: "transition",
		Arguments: map[string]any{
			"intent":     name,
			"confidence": 0.9,
		},
	}, nil
}
func (offRampRoutingHarness) Close() error { return nil }

// offRampNoMatchMachine wraps the real machine so that a Turn() routed to
// offRampNoMatchIntent surfaces a genuine INTENT_UNKNOWN no-match ValidationError
// — the code an unmappable free-text utterance produces in the live router/LLM
// path. Every other intent delegates to the embedded machine unchanged. This is
// what lets the Turn()-driven wiring tests reach maybeOffRamp through the real
// orchestrator default branch (the machine never emits a no-match code on its
// own — it rejects unknown names as INTENT_NOT_ALLOWED_IN_STATE — so the no-match
// must be modeled here, standing in for the upstream router/LLM verdict).
type offRampNoMatchMachine struct {
	machine.Machine
}

func (m offRampNoMatchMachine) Turn(ctx context.Context, cur app.StatePath, w world.World, call intent.IntentCall) (machine.TurnResult, error) {
	if call.Intent == offRampNoMatchIntent {
		return machine.TurnResult{
			NewState: cur,
			World:    w,
			ValidationError: &intent.ValidationError{
				Code:    intent.ErrIntentUnknown,
				Message: "the router could not map the utterance to any allowed intent",
			},
		}, nil
	}
	return m.Machine.Turn(ctx, cur, w, call)
}

// offRampClarifyHarness models the dominant free-text no-match: the router/LLM
// answered but could not map the utterance to any allowed intent, so RunTurn
// returns a *harness.ClarifyResponse. The priming utterance "look" still routes
// to the real `look` intent so the first foreground turn materializes the root
// state; every other utterance clarifies. This drives the clarify branch in
// orchestrator.go — the entry point the off-ramp now intercepts — without ever
// reaching machine.Turn (the clarify is returned before the machine runs).
type offRampClarifyHarness struct{}

func (offRampClarifyHarness) RunTurn(ctx context.Context, in harness.TurnInput) (mcp.CallToolParams, error) {
	if in.UserText == "look" {
		return mcp.CallToolParams{
			Name: "transition",
			Arguments: map[string]any{
				"intent":     "look",
				"confidence": 0.9,
			},
		}, nil
	}
	return mcp.CallToolParams{}, &harness.ClarifyResponse{
		Message: "I'm not sure which action you mean. Try rephrasing.",
	}
}
func (offRampClarifyHarness) Close() error { return nil }

// offRampFakeAgentPath resolves the shared fake-agent.sh stub (the converse
// stand-in) by absolute path. White-box sibling of offpath_test.go's
// fakeAgentPath, replicated because that helper lives in the orchestrator_test
// package and isn't visible here.
func offRampFakeAgentPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	path := filepath.Join(filepath.Dir(thisFile), "..", "host", "testdata", "fake-agent.sh")
	info, err := os.Stat(path)
	require.NoErrorf(t, err, "fake-agent.sh not found at %s", path)
	require.NotZerof(t, info.Mode()&0111, "fake-agent.sh is not executable")
	return path
}

// offRampApp is a two-room app: `idea` declares the off-ramp, `menu` does not.
// Both carry a self-transition so the rooms are valid resting states.
func offRampApp() *app.AppDef { return offRampAppRootedAt("idea") }

// offRampAppRootedAt builds offRampApp with a caller-chosen root so the
// non-off-ramp wiring test can rest the session in `menu` from the start
// (the orchestrator exposes no force-state hook).
func offRampAppRootedAt(root string) *app.AppDef {
	return &app.AppDef{
		App:  app.AppMeta{ID: "offramp-test", Version: "1"},
		Root: root,
		Intents: map[string]app.Intent{
			"look": {Title: "Look", Description: "Look around."},
		},
		States: map[string]*app.State{
			"idea": {
				View:         app.LegacyView("Tell me about your idea."),
				AgentOffRamp: &app.OffRampDef{}, // enabled, bare-form voice
				On:           map[string][]app.Transition{"look": {{Target: "idea"}}},
			},
			"menu": {
				View: app.LegacyView("Pick something."),
				On:   map[string][]app.Transition{"look": {{Target: "menu"}}},
			},
		},
	}
}

// setupOffRampOrch wires a white-box orchestrator with a real chats.Store and
// the fake-agent stub as the converse backend, returning the orchestrator,
// raw store, and a fresh session id.
func setupOffRampOrch(t *testing.T) (*Orchestrator, store.Store, app.SessionID) {
	return setupOffRampOrchWith(t, offRampNoopHarness{})
}

// setupOffRampOrchWith is setupOffRampOrch with a caller-supplied Harness so
// the Turn()-driven integration tests can plug in a routing harness that forces
// a no-match, while the helper-level tests keep the inert noop harness.
func setupOffRampOrchWith(t *testing.T, h harness.Harness) (*Orchestrator, store.Store, app.SessionID) {
	return setupOffRampOrchDef(t, offRampApp(), h, false)
}

// setupOffRampOrchDef is the underlying builder, parameterized on the AppDef so
// the non-off-ramp wiring test can rest the session in a `menu`-rooted variant.
// When noMatchMachine is set the real machine is wrapped in
// offRampNoMatchMachine so a Turn() routed to offRampNoMatchIntent surfaces a
// genuine INTENT_UNKNOWN — the path the off-ramp wiring intercepts.
func setupOffRampOrchDef(t *testing.T, def *app.AppDef, h harness.Harness, noMatchMachine bool) (*Orchestrator, store.Store, app.SessionID) {
	t.Helper()
	t.Setenv(host.AgentBinEnv, offRampFakeAgentPath(t))

	// The runtime treats a non-nil State.AgentOffRamp as "the off-ramp
	// fires" — that's the loader's post-normalize contract (it nils the
	// pointer for a disabled `false`). Building &OffRampDef{} in Go therefore
	// models an enabled bare-form off-ramp without needing the YAML loader.
	var m machine.Machine
	m, err := machine.New(def)
	require.NoError(t, err)
	if noMatchMachine {
		m = offRampNoMatchMachine{Machine: m}
	}

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	rawChatStore, err := chats.NewStore(s.DB())
	require.NoError(t, err)

	orch := New(def, m, s, h, WithChatStore(chathost.NewAdapter(rawChatStore)))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)
	return orch, s, sid
}

// TestMaybeOffRamp_NoMatchInOffRampRoom asserts Task 2.1: a genuine no-match in
// an off-ramp room routes to the converse stub and returns a ModeOffPath
// outcome carrying the answer, with state unchanged and no rejection persisted.
func TestMaybeOffRamp_NoMatchInOffRampRoom(t *testing.T) {
	orch, raw, sid := setupOffRampOrch(t)
	ctx := context.Background()

	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)

	for _, code := range []intent.ErrorCode{intent.ErrUnknownIntent, intent.ErrIntentUnknown} {
		outcome, ok := orch.maybeOffRamp(ctx, sid, app.StatePath("idea"), world.World{Vars: map[string]any{}},
			"how do you spell discovery?", code, 0, []string{"look"}, jBefore.Turn+1)
		require.Truef(t, ok, "off-ramp should fire on %s in an off-ramp room", code)
		require.NotNil(t, outcome)
		require.Equal(t, ModeOffPath, outcome.Mode)
		require.Contains(t, outcome.View, "ANSWER for q=[how do you spell discovery?]",
			"the converse answer should be surfaced as the outcome view")
		require.Equal(t, app.StatePath("idea"), outcome.NewState, "off-ramp must not advance state")
		require.Equal(t, []string{"look"}, outcome.AllowedIntents, "the room menu must persist")
	}

	// State and world unchanged across both off-ramp calls.
	jAfter, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, jBefore.State, jAfter.State, "off-ramp must not mutate state")
	require.Equal(t, len(jBefore.World.Vars), len(jAfter.World.Vars), "off-ramp must not mutate world")

	// No TransitionApplied was emitted; an OffPathEntered with reason off_ramp was.
	hist, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	var sawOffRampEntered, sawQuestion, sawAnswer bool
	for _, ev := range hist {
		switch ev.Kind {
		case store.TransitionApplied, store.StateEntered, store.StateExited:
			t.Fatalf("off-ramp must not emit %s", ev.Kind)
		case store.OffPathEntered:
			if reasonOf(ev) == "off_ramp" {
				sawOffRampEntered = true
			}
		case store.OffPathQuestion:
			sawQuestion = true
		case store.OffPathAnswer:
			sawAnswer = true
		}
	}
	require.True(t, sawOffRampEntered, "expected an OffPathEntered labeled reason: off_ramp")
	require.True(t, sawQuestion, "expected OffPathQuestion recorded")
	require.True(t, sawAnswer, "expected OffPathAnswer recorded")
}

// TestMaybeOffRamp_ScopeGuard asserts Task 2.2: recognized-but-blocked codes in
// an off-ramp room do NOT off-ramp — the helper is inert for every code that
// is not a genuine no-match, so the caller proceeds with the normal rejection.
func TestMaybeOffRamp_ScopeGuard(t *testing.T) {
	orch, _, sid := setupOffRampOrch(t)
	ctx := context.Background()

	blocked := []intent.ErrorCode{
		intent.ErrGuardFailed,
		intent.ErrMissingSlots,
		intent.ErrIntentNotAllowed,
		intent.ErrInvalidSlotValue,
		intent.ErrAmbiguousIntent,
	}
	for _, code := range blocked {
		outcome, ok := orch.maybeOffRamp(ctx, sid, app.StatePath("idea"), world.World{Vars: map[string]any{}},
			"some input", code, 0, []string{"look"}, 1)
		require.Falsef(t, ok, "off-ramp must stay inert for %s (scope guard)", code)
		require.Nilf(t, outcome, "no outcome for %s", code)
	}
}

// TestMaybeOffRamp_NonOffRampRoom asserts Task 2.4: a no-match in a room that
// did NOT declare the off-ramp returns (nil, false) — the caller falls through
// to ModeRejected, unchanged from today.
func TestMaybeOffRamp_NonOffRampRoom(t *testing.T) {
	orch, _, sid := setupOffRampOrch(t)
	ctx := context.Background()

	outcome, ok := orch.maybeOffRamp(ctx, sid, app.StatePath("menu"), world.World{Vars: map[string]any{}},
		"anything", intent.ErrIntentUnknown, 0, []string{"look"}, 1)
	require.False(t, ok, "a non-off-ramp room must not off-ramp")
	require.Nil(t, outcome)
}

// TestMaybeOffRamp_EmptyInputInert asserts the empty-input guard: the
// slot-continuation path (which carries no fresh utterance) does not off-ramp
// even on a no-match in an off-ramp room.
func TestMaybeOffRamp_EmptyInputInert(t *testing.T) {
	orch, _, sid := setupOffRampOrch(t)
	ctx := context.Background()

	outcome, ok := orch.maybeOffRamp(ctx, sid, app.StatePath("idea"), world.World{Vars: map[string]any{}},
		"", intent.ErrIntentUnknown, 0, []string{"look"}, 1)
	require.False(t, ok, "empty input has nothing to converse over")
	require.Nil(t, outcome)
}

// TestTurn_OffRampWiring_NoMatchInOffRampRoom drives a genuine no-match through
// the REAL Turn() entry point (not the maybeOffRamp helper) against the off-ramp
// `idea` room: the routing harness resolves to an intent the app declares
// nowhere, producing an UNKNOWN_INTENT in machine.Turn, which must reach
// maybeOffRamp at the orchestrator.go default branch BEFORE the rejection events
// are persisted. Asserts ModeOffPath and that NO TurnEnded(rejected) was logged.
// This is the wiring proof the helper-level tests cannot give: a regression that
// deleted or mis-ordered the call site (e.g. moved it after the failure-event
// persistence) would fail here.
func TestTurn_OffRampWiring_NoMatchInOffRampRoom(t *testing.T) {
	orch, raw, sid := setupOffRampOrchDef(t, offRampApp(), offRampRoutingHarness{}, true)
	ctx := context.Background()

	// Prime: one successful `look` turn materializes the root `idea` state into
	// the journey (a fresh session's journey.State is "" until the first turn).
	_, err := orch.Turn(ctx, sid, "look")
	require.NoError(t, err)
	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("idea"), jBefore.State, "resting in the off-ramp room")

	// Snapshot the history so we inspect only the events the no-match turn
	// appends — the priming `look` turn legitimately emits TransitionApplied/
	// StateEntered, which is not what we're asserting about here.
	histBefore, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	priorEvents := len(histBefore)

	outcome, err := orch.Turn(ctx, sid, "how do you spell discovery?")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	require.Equal(t, ModeOffPath, outcome.Mode,
		"a no-match in an off-ramp room must off-ramp, not reject, via Turn()")
	require.Contains(t, outcome.View, "ANSWER for q=[how do you spell discovery?]",
		"the converse answer should be surfaced as the outcome view")
	require.Equal(t, app.StatePath("idea"), outcome.NewState, "off-ramp must not advance state")

	// State unchanged.
	jAfter, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, jBefore.State, jAfter.State, "off-ramp must not mutate state")

	// No rejection was persisted by the no-match turn: among the events it
	// appended there must be no TurnEnded(outcome: rejected) and no transition
	// events. An OffPathEntered(reason: off_ramp) must be present.
	hist, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	var sawOffRampEntered bool
	for _, ev := range hist[priorEvents:] {
		switch ev.Kind {
		case store.TransitionApplied, store.StateEntered, store.StateExited:
			t.Fatalf("off-ramp must not emit %s", ev.Kind)
		case store.TurnEnded:
			require.NotEqual(t, "rejected", outcomeOf(ev),
				"off-ramp must not persist a TurnEnded(rejected); the rejection was intercepted")
		case store.OffPathEntered:
			if reasonOf(ev) == "off_ramp" {
				sawOffRampEntered = true
			}
		}
	}
	require.True(t, sawOffRampEntered,
		"expected an OffPathEntered labeled reason: off_ramp from the wired call site")
}

// TestTurn_OffRampWiring_NoMatchInNonOffRampRoom is the sibling: the same
// no-match driven through Turn() against the `menu` room (which did NOT declare
// the off-ramp) must fall through to the ordinary ModeRejected and persist a
// TurnEnded(rejected) — proving the call site is scoped to off-ramp rooms only
// (Task 2.4) and is exercised end-to-end, not just at the helper level.
func TestTurn_OffRampWiring_NoMatchInNonOffRampRoom(t *testing.T) {
	// Rest the session in the non-off-ramp `menu` room from the start.
	orch, raw, sid := setupOffRampOrchDef(t, offRampAppRootedAt("menu"),
		offRampRoutingHarness{}, true)
	ctx := context.Background()

	// Prime: one successful `look` turn materializes the root `menu` state.
	_, err := orch.Turn(ctx, sid, "look")
	require.NoError(t, err)
	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("menu"), jBefore.State, "resting in the non-off-ramp room")

	outcome, err := orch.Turn(ctx, sid, "how do you spell discovery?")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	require.Equal(t, ModeRejected, outcome.Mode,
		"a no-match in a non-off-ramp room must reject, unchanged from today")

	hist, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	var sawRejectedTurnEnded bool
	for _, ev := range hist {
		if ev.Kind == store.OffPathEntered && reasonOf(ev) == "off_ramp" {
			t.Fatalf("a non-off-ramp room must not enter the off-ramp")
		}
		if ev.Kind == store.TurnEnded && outcomeOf(ev) == "rejected" {
			sawRejectedTurnEnded = true
		}
	}
	require.True(t, sawRejectedTurnEnded,
		"a non-off-ramp room must persist the ordinary TurnEnded(rejected)")
}

// TestTurn_OffRampWiring_ClarifyInOffRampRoom is the core correctness proof: an
// unroutable free-text utterance drives the harness to return a
// *harness.ClarifyResponse, which orchestrator.go's clarify branch must now
// route into the off-ramp (NOT return as a soft ModeRejected{LLM_CLARIFICATION})
// because the resting `idea` room opted in. Asserts ModeOffPath with the
// converse answer in the View, state+world unchanged, an
// OffPathEntered{reason: off_ramp, error_code: LLM_CLARIFICATION} persisted, and
// NO TurnEnded(rejected) for the turn. This exercises the REAL entry point the
// older helper-level tests bypass — the clarify free-text no-match that surfaces
// before any machine.Turn ve.Code, which was the inert path the rewire fixes.
func TestTurn_OffRampWiring_ClarifyInOffRampRoom(t *testing.T) {
	orch, raw, sid := setupOffRampOrchDef(t, offRampApp(), offRampClarifyHarness{}, false)
	ctx := context.Background()

	// Prime: one successful `look` turn materializes the root `idea` state.
	_, err := orch.Turn(ctx, sid, "look")
	require.NoError(t, err)
	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("idea"), jBefore.State, "resting in the off-ramp room")

	histBefore, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	priorEvents := len(histBefore)

	outcome, err := orch.Turn(ctx, sid, "how do you spell discovery?")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	require.Equal(t, ModeOffPath, outcome.Mode,
		"an unroutable free-text clarify in an off-ramp room must off-ramp, not soft-reject")
	require.Contains(t, outcome.View, "ANSWER for q=[how do you spell discovery?]",
		"the converse answer should be surfaced as the outcome view")
	require.Equal(t, app.StatePath("idea"), outcome.NewState, "off-ramp must not advance state")
	require.Equal(t, []string{"look"}, outcome.AllowedIntents, "the room menu must persist")

	// State + world unchanged.
	jAfter, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, jBefore.State, jAfter.State, "off-ramp must not mutate state")
	require.Equal(t, len(jBefore.World.Vars), len(jAfter.World.Vars), "off-ramp must not mutate world")

	// Among the events the clarify turn appended: an OffPathEntered labeled
	// reason: off_ramp with error_code: LLM_CLARIFICATION, and NO
	// TurnEnded(rejected) / transition events.
	hist, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	var sawOffRampEntered bool
	for _, ev := range hist[priorEvents:] {
		switch ev.Kind {
		case store.TransitionApplied, store.StateEntered, store.StateExited:
			t.Fatalf("off-ramp must not emit %s", ev.Kind)
		case store.TurnEnded:
			require.NotEqual(t, "rejected", outcomeOf(ev),
				"off-ramp must not persist a TurnEnded(rejected); the clarify was intercepted")
		case store.OffPathEntered:
			if reasonOf(ev) == "off_ramp" {
				sawOffRampEntered = true
				require.Equal(t, "LLM_CLARIFICATION", errorCodeOf(ev),
					"the OffPathEntered must record the triggering clarify code")
			}
		}
	}
	require.True(t, sawOffRampEntered,
		"expected an OffPathEntered{reason: off_ramp} from the wired clarify branch")
}

// TestTurn_OffRampWiring_ClarifyInNonOffRampRoom is the gate proof: the same
// unroutable clarify driven through Turn() against the non-opted-in `menu` room
// must remain byte-identical to today — ModeRejected{LLM_CLARIFICATION}, with no
// off-ramp entry. This shows the rewire is scoped strictly to off-ramp rooms.
func TestTurn_OffRampWiring_ClarifyInNonOffRampRoom(t *testing.T) {
	orch, raw, sid := setupOffRampOrchDef(t, offRampAppRootedAt("menu"),
		offRampClarifyHarness{}, false)
	ctx := context.Background()

	// Prime: one successful `look` turn materializes the root `menu` state.
	_, err := orch.Turn(ctx, sid, "look")
	require.NoError(t, err)
	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("menu"), jBefore.State, "resting in the non-off-ramp room")

	outcome, err := orch.Turn(ctx, sid, "how do you spell discovery?")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	require.Equal(t, ModeRejected, outcome.Mode,
		"a clarify in a non-off-ramp room must soft-reject, unchanged from today")
	require.Equal(t, intent.ErrorCode("LLM_CLARIFICATION"), outcome.ErrorCode,
		"the clarify code must be preserved on the non-opted-in path")

	hist, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	for _, ev := range hist {
		if ev.Kind == store.OffPathEntered && reasonOf(ev) == "off_ramp" {
			t.Fatalf("a non-off-ramp room must not enter the off-ramp")
		}
	}
}

// TestTurn_OffRampWiring_ValidIntentNotSwallowed is the complementary negative
// proof to the no-match/clarify wiring tests: a VALID intent in an off-ramp room
// must transition normally and must NOT be diverted into the off-ramp. The
// off-ramp is scoped to no-match codes (see TestMaybeOffRamp_ScopeGuard), but
// those tests drive the helper with synthetic codes; this one drives the REAL
// Turn() entry point with a genuinely routable intent (`look`, resolved by the
// routing harness to the declared `look` self-transition) against the opted-in
// `idea` room and asserts the success path is untouched. A regression that
// consulted maybeOffRamp on every outcome (not just the no-match branches) would
// swallow this transition and fail here — the gap the other wiring tests, which
// only ever feed a no-match, cannot catch.
func TestTurn_OffRampWiring_ValidIntentNotSwallowed(t *testing.T) {
	orch, raw, sid := setupOffRampOrchDef(t, offRampApp(), offRampRoutingHarness{}, true)
	ctx := context.Background()

	// Prime: one successful `look` turn materializes the root `idea` state.
	_, err := orch.Turn(ctx, sid, "look")
	require.NoError(t, err)
	jBefore, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("idea"), jBefore.State, "resting in the off-ramp room")

	// Snapshot so we inspect only the events the asserted (valid) turn appends.
	histBefore, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	priorEvents := len(histBefore)

	// A second `look` — a valid, routable intent in the off-ramp room.
	outcome, err := orch.Turn(ctx, sid, "look")
	require.NoError(t, err)
	require.NotNil(t, outcome)
	require.Equal(t, ModeTransitioned, outcome.Mode,
		"a valid intent in an off-ramp room must transition, not off-ramp")
	require.NotEqual(t, ModeOffPath, outcome.Mode,
		"the off-ramp must not swallow a successful intent")
	require.Equal(t, app.StatePath("idea"), outcome.NewState,
		"the look self-transition rests back in idea")

	// The valid turn appended a TransitionApplied and NO OffPathEntered.
	hist, err := raw.LoadHistory(sid)
	require.NoError(t, err)
	var sawTransition bool
	for _, ev := range hist[priorEvents:] {
		switch ev.Kind {
		case store.TransitionApplied:
			sawTransition = true
		case store.OffPathEntered:
			t.Fatalf("a valid intent must not enter the off-ramp (reason=%q)", reasonOf(ev))
		}
	}
	require.True(t, sawTransition,
		"a valid intent must persist the ordinary TransitionApplied")
}

// errorCodeOf extracts the `error_code` string from an event payload.
func errorCodeOf(ev store.Event) string {
	var m map[string]any
	if json.Unmarshal(ev.Payload, &m) != nil {
		return ""
	}
	c, _ := m["error_code"].(string)
	return c
}

// outcomeOf extracts the `outcome` string from a TurnEnded event payload.
func outcomeOf(ev store.Event) string {
	var m map[string]any
	if json.Unmarshal(ev.Payload, &m) != nil {
		return ""
	}
	o, _ := m["outcome"].(string)
	return o
}

// reasonOf extracts the `reason` string from an OffPathEntered event payload.
func reasonOf(ev store.Event) string {
	var m map[string]any
	if json.Unmarshal(ev.Payload, &m) != nil {
		return ""
	}
	r, _ := m["reason"].(string)
	return r
}
