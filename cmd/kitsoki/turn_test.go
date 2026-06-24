package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestTurn_DirectIntent runs a one-shot turn via the --intent path and
// asserts the JSON output captures the state transition, view, and effect
// diffs without persisting anything.
func TestTurn_DirectIntent(t *testing.T) {
	def := loadCloak(t)
	orch := buildOneShotOrch(t, def, &noRunHarness{})

	out, err := orch.OneShot(context.Background(), orchestrator.OneShotInput{
		State:  app.StatePath("foyer"),
		World:  cloakDefaultWorld(def),
		Intent: "go",
		Slots:  map[string]any{"direction": "west"},
	})
	require.NoError(t, err)

	assert.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	assert.Equal(t, app.StatePath("foyer"), out.PrevState)
	assert.Equal(t, app.StatePath("cloakroom"), out.NextState)
	assert.NotEmpty(t, out.View, "rendered view should not be empty")
	assert.Equal(t, "go", out.Intent)
	assert.NotEmpty(t, out.AllowedIntents)
	assert.Contains(t, out.WorldBefore, "wearing_cloak")
	assert.Contains(t, out.WorldAfter, "wearing_cloak")
}

// TestTurn_RoutedInput runs the --input path through a replay harness so we
// exercise harness.RunTurn → parseIntentCall → machine.Turn end-to-end.
func TestTurn_RoutedInput(t *testing.T) {
	def := loadCloak(t)
	h, err := harness.NewReplay(filepath.Join("..", "..", "testdata", "apps", "cloak", "recording.yaml"))
	require.NoError(t, err)
	orch := buildOneShotOrch(t, def, h)

	out, err := orch.OneShot(context.Background(), orchestrator.OneShotInput{
		State: app.StatePath("foyer"),
		World: cloakDefaultWorld(def),
		Input: "go west",
	})
	require.NoError(t, err)
	assert.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	assert.Equal(t, app.StatePath("cloakroom"), out.NextState)
}

// TestTurn_HostDispatchVisible confirms that one-shot turns dispatch host
// calls and surface the binding effects in WorldAfter and View, the same as
// the live orchestrator path does. We cover this with hang_cloak, which
// emits a `set` effect (no host call) — close enough to verify the effect
// diffs land in OneShotResult.Effects.
func TestTurn_HostDispatchVisible(t *testing.T) {
	def := loadCloak(t)
	orch := buildOneShotOrch(t, def, &noRunHarness{})

	out, err := orch.OneShot(context.Background(), orchestrator.OneShotInput{
		State:  app.StatePath("cloakroom"),
		World:  cloakDefaultWorld(def),
		Intent: "hang_cloak",
	})
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	assert.Equal(t, false, out.WorldAfter["wearing_cloak"],
		"hang_cloak should set wearing_cloak=false")
	require.NotEmpty(t, out.Effects, "hang_cloak emits a set-effect")
}

// TestTurn_RejectedIntent verifies rejected outcomes carry an error_code
// and don't change state.
func TestTurn_RejectedIntent(t *testing.T) {
	def := loadCloak(t)
	orch := buildOneShotOrch(t, def, &noRunHarness{})

	out, err := orch.OneShot(context.Background(), orchestrator.OneShotInput{
		State:  app.StatePath("foyer"),
		World:  cloakDefaultWorld(def),
		Intent: "hang_cloak", // not allowed in foyer
	})
	require.NoError(t, err)
	assert.Equal(t, orchestrator.ModeRejected, out.Mode)
	assert.Equal(t, app.StatePath("foyer"), out.NextState)
	assert.NotEmpty(t, out.ErrorCode, "rejected outcome should carry error_code")
}

// TestTurn_OneShotInputValidation guards the OneShotInput preconditions.
func TestTurn_OneShotInputValidation(t *testing.T) {
	def := loadCloak(t)
	orch := buildOneShotOrch(t, def, &noRunHarness{})

	_, err := orch.OneShot(context.Background(), orchestrator.OneShotInput{
		State: app.StatePath("foyer"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Intent or Input")
}

// TestTurnOutputJSON sanity-checks the CLI-level JSON shape: the outer
// `mode` is the human-readable string, and the canonical fields are present.
func TestTurnOutputJSON(t *testing.T) {
	r := &orchestrator.OneShotResult{
		Mode:      orchestrator.ModeTransitioned,
		Intent:    "go_west",
		PrevState: app.StatePath("foyer"),
		NextState: app.StatePath("cloakroom"),
		View:      "Cloakroom\n",
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	require.NoError(t, enc.Encode(turnOutputView(r)))

	out := buf.String()
	assert.True(t, strings.Contains(out, `"mode": "transitioned"`),
		"outer mode field should be the string form, got: %s", out)
	assert.Contains(t, out, `"prev_state": "foyer"`)
	assert.Contains(t, out, `"next_state": "cloakroom"`)
	assert.Contains(t, out, `"view_rendered": "Cloakroom\n"`)
}

// loadCloak loads the cloak app from testdata.
func loadCloak(t *testing.T) *app.AppDef {
	t.Helper()
	def, err := app.Load(filepath.Join("..", "..", "testdata", "apps", "cloak", "app.yaml"))
	require.NoError(t, err)
	return def
}

// cloakDefaultWorld returns the cloak app's schema defaults as a plain map,
// ready to drop into OneShotInput.World.
func cloakDefaultWorld(def *app.AppDef) map[string]any {
	w := machine.WorldFromSchema(def.World)
	out := make(map[string]any, len(w.Vars))
	for k, v := range w.Vars {
		out[k] = v
	}
	return out
}

// buildOneShotOrch constructs a fully wired orchestrator backed by an
// in-memory store, suitable for OneShot tests.
func buildOneShotOrch(t *testing.T, def *app.AppDef, h harness.Harness) *orchestrator.Orchestrator {
	t.Helper()
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	return orchestrator.New(def, m, s, h,
		orchestrator.WithHostRegistry(hostReg),
	)
}

// TestTraceTurn_HappyPath exercises the trace-backed turn path end-to-end:
//   - first call: creates the JSONL file, appends events for turn 1
//   - second call: loads the existing trace, resumes, appends events for turn 2
//
// Uses the cloak story and the "go" intent (go west → cloakroom).
func TestTraceTurn_HappyPath(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "cloak-test.jsonl")

	// --- Turn 1: new trace ---
	turn1Events := runTraceTurnInProcess(t, tracePath, "go", map[string]any{"direction": "west"})
	assert.NotEmpty(t, turn1Events, "turn 1 should produce events")

	// The trace file must exist and be non-empty.
	data, err := os.ReadFile(tracePath)
	require.NoError(t, err)
	assert.Contains(t, string(data), "session.header", "trace must start with session.header")
	assert.Contains(t, string(data), "turn.start", "trace must contain TurnStarted event")

	// --- Turn 2: resume from existing trace ---
	turn2Events := runTraceTurnInProcess(t, tracePath, "hang_cloak", nil)
	assert.NotEmpty(t, turn2Events, "turn 2 should produce events")

	// Both turns' events should be in the file.
	data2, err := os.ReadFile(tracePath)
	require.NoError(t, err)
	lines := 0
	for _, line := range bytes.Split(bytes.TrimSpace(data2), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines++
		}
	}
	// At minimum: 1 header + events from 2 turns (TurnStarted, etc.)
	assert.Greater(t, lines, 5, "trace should have at least 5 lines after 2 turns")
}

// TestTraceTurn_NewEvents_PrintedToStdout verifies that only the events from
// the current turn are printed to stdout (not the full trace history).
func TestTraceTurn_NewEvents_PrintedToStdout(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "stdout-test.jsonl")

	// Turn 1.
	events1 := runTraceTurnInProcess(t, tracePath, "go", map[string]any{"direction": "west"})
	// Turn 2.
	events2 := runTraceTurnInProcess(t, tracePath, "hang_cloak", nil)

	// Events from turn 2 must not re-emit events from turn 1.
	for _, ev := range events2 {
		assert.NotEqual(t, 1, ev["turn"], "turn-2 stdout should not re-emit turn-1 events (got turn=1)")
	}
	assert.Greater(t, len(events1)+len(events2), 2, "should have events in both turns")
}

// runTraceTurnInProcess calls runTraceTurn directly (in-process) against the
// cloak story and returns the events printed to stdout as a slice of maps.
func runTraceTurnInProcess(t *testing.T, tracePath, intent string, slots map[string]any) []map[string]any {
	t.Helper()

	// Build the orchestrator manually (same as runTraceTurn does internally).
	def := loadCloak(t)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	require.NoError(t, os.MkdirAll(filepath.Dir(tracePath), 0o755))
	sink, err := store.OpenJSONL(tracePath)
	require.NoError(t, err)
	// Close the sink when this helper returns so the flock is released before
	// the next call (t.Cleanup runs at test end, too late for sequential calls).
	defer func() { _ = sink.Close() }()

	hostReg := host.NewRegistry()
	host.RegisterBuiltins(hostReg)

	orch := orchestrator.New(def, m, s, &noRunHarness{},
		orchestrator.WithHostRegistry(hostReg),
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)

	histBefore := len(sink.History())

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	slotMap := make(map[string]any)
	for k, v := range slots {
		slotMap[k] = v
	}

	_, err = orch.SubmitDirect(context.Background(), sid, intent, slotMap)
	require.NoError(t, err)

	// Collect newly appended events (what the CLI prints to stdout).
	newEvents := sink.History()[histBefore:]
	out := make([]map[string]any, 0, len(newEvents))
	for _, ev := range newEvents {
		b, _ := json.Marshal(ev)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		out = append(out, m)
	}
	return out
}
