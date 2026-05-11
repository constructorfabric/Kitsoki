package orchestrator_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
	"kitsoki/internal/trace"
)

// ─── capturingHandler ────────────────────────────────────────────────────────

// recordSink holds the shared slice of captured records.
type recordSink struct {
	records []slog.Record
}

// capturingHandler is a slog.Handler that buffers all records in a shared sink.
type capturingHandler struct {
	sink     *recordSink
	level    slog.Level
	preAttrs []slog.Attr
}

func newCapturingHandler(level slog.Level) *capturingHandler {
	return &capturingHandler{sink: &recordSink{}, level: level}
}

func (h *capturingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	clone := r.Clone()
	if len(h.preAttrs) > 0 {
		clone.AddAttrs(h.preAttrs...)
	}
	h.sink.records = append(h.sink.records, clone)
	return nil
}

func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, len(h.preAttrs)+len(attrs))
	copy(merged, h.preAttrs)
	copy(merged[len(h.preAttrs):], attrs)
	return &capturingHandler{sink: h.sink, level: h.level, preAttrs: merged}
}

func (h *capturingHandler) WithGroup(_ string) slog.Handler { return h }

func (h *capturingHandler) msgs() []string {
	out := make([]string, len(h.sink.records))
	for i, r := range h.sink.records {
		out[i] = r.Message
	}
	return out
}

func (h *capturingHandler) hasMsg(msg string) bool {
	for _, r := range h.sink.records {
		if r.Message == msg {
			return true
		}
	}
	return false
}

func (h *capturingHandler) allRecords() []slog.Record {
	return h.sink.records
}

// ─── TestOrchestratorTraceEvents ─────────────────────────────────────────────

// TestOrchestratorTraceEvents runs a Cloak turn and asserts that the expected
// trace event names are emitted in the right order.
func TestOrchestratorTraceEvents(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	// Wire up the capturing handler.
	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)

	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Use replay harness so no LLM is needed.
	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)
	h.WithLogger(logger)

	orch := orchestrator.New(def, m, s, h, orchestrator.WithLogger(logger))
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Turn 1: foyer → go west → cloakroom
	out, err := orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)

	msgs := handler.msgs()
	t.Logf("captured %d events: %v", len(msgs), msgs)

	// Required orchestrator events.
	assert.True(t, handler.hasMsg(trace.EvTurnStart), "expected turn.start")
	assert.True(t, handler.hasMsg(trace.EvTurnRouted), "expected turn.routed")
	assert.True(t, handler.hasMsg(trace.EvTurnStepped), "expected turn.stepped")
	assert.True(t, handler.hasMsg(trace.EvTurnPersisted), "expected turn.persisted")
	assert.True(t, handler.hasMsg(trace.EvTurnDone), "expected turn.done")

	// Harness event: recording hit.
	assert.True(t, handler.hasMsg(trace.EvHarnessRecordingHit), "expected harness.recording_hit")

	// Machine events.
	assert.True(t, handler.hasMsg(trace.EvMachineTransition), "expected machine.transition")
	assert.True(t, handler.hasMsg(trace.EvMachineGuardWinner), "expected machine.guard.winner")

	// Ordering: turn.start must precede turn.done.
	startIdx, doneIdx := -1, -1
	for i, msg := range msgs {
		if msg == trace.EvTurnStart && startIdx < 0 {
			startIdx = i
		}
		if msg == trace.EvTurnDone {
			doneIdx = i
		}
	}
	require.GreaterOrEqual(t, startIdx, 0, "turn.start not found")
	require.GreaterOrEqual(t, doneIdx, 0, "turn.done not found")
	assert.Greater(t, doneIdx, startIdx, "turn.start must come before turn.done")
}

// TestOrchestratorTraceEffects verifies machine.effect.applied is emitted
// when the hang_cloak transition fires (it sets wearing_cloak=false).
func TestOrchestratorTraceEffects(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)

	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)
	h.WithLogger(logger)

	orch := orchestrator.New(def, m, s, h, orchestrator.WithLogger(logger))
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// go west → cloakroom
	_, err = orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)

	// hang_cloak → emits machine.effect.applied for wearing_cloak=false
	_, err = orch.Turn(ctx, sid, "hang the cloak")
	require.NoError(t, err)

	assert.True(t, handler.hasMsg(trace.EvMachineEffectApplied),
		"expected machine.effect.applied from hang_cloak transition")
}

// TestOrchestratorTraceWinningPath verifies all acceptance criteria events across
// a full Cloak winning-path run.
func TestOrchestratorTraceWinningPath(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)

	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)
	h.WithLogger(logger)

	orch := orchestrator.New(def, m, s, h, orchestrator.WithLogger(logger))
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	inputs := []string{"go west", "hang the cloak", "go east", "go south", "read the message"}
	for _, inp := range inputs {
		_, err := orch.Turn(ctx, sid, inp)
		require.NoError(t, err, "turn %q failed", inp)
	}

	// Acceptance criteria:
	// - At least one harness.recording_hit per turn.
	recordingHits := countMsg(handler.allRecords(), trace.EvHarnessRecordingHit)
	assert.GreaterOrEqual(t, recordingHits, 5, "expected at least one recording_hit per turn")

	// - At least one machine.guard.winner event per transition.
	guardWinners := countMsg(handler.allRecords(), trace.EvMachineGuardWinner)
	assert.GreaterOrEqual(t, guardWinners, 1, "expected at least one machine.guard.winner")

	// - At least one machine.effect.applied.
	effectApplied := countMsg(handler.allRecords(), trace.EvMachineEffectApplied)
	assert.GreaterOrEqual(t, effectApplied, 1, "expected at least one machine.effect.applied")

	// - turn.done with the correct final state.
	var lastDoneState string
	for _, r := range handler.allRecords() {
		if r.Message == trace.EvTurnDone {
			r.Attrs(func(a slog.Attr) bool {
				if a.Key == "new_state" {
					lastDoneState = a.Value.String()
				}
				return true
			})
		}
	}
	assert.Equal(t, "ended", lastDoneState, "final turn.done should report state=ended")
}

// countMsg counts how many records have the given message.
func countMsg(records []slog.Record, msg string) int {
	n := 0
	for _, r := range records {
		if r.Message == msg {
			n++
		}
	}
	return n
}

// TestTurnDoneCarriesRenderedView asserts that turn.done events embed the
// rendered view string under view_rendered. This is the asymmetric-win
// observability surface from ai-collaboration-proposal.md §1: any --trace
// consumer (AI collaborator, future analytics, flow-test assertions) gets
// the rendered narrative for free, by-byte reproducible.
func TestTurnDoneCarriesRenderedView(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	handler := newCapturingHandler(slog.LevelDebug)
	logger := slog.New(handler)

	m, err := machine.New(def, machine.WithMachineLogger(logger))
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	h, err := harness.NewReplay("../../testdata/apps/cloak/recording.yaml")
	require.NoError(t, err)
	h.WithLogger(logger)

	orch := orchestrator.New(def, m, s, h, orchestrator.WithLogger(logger))
	ctx := context.Background()

	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	out, err := orch.Turn(ctx, sid, "go west")
	require.NoError(t, err)
	require.Equal(t, orchestrator.ModeTransitioned, out.Mode)
	require.NotEmpty(t, out.View, "turn outcome should carry rendered view for cloak go-west")

	var (
		gotView   string
		foundDone bool
	)
	for _, r := range handler.allRecords() {
		if r.Message != trace.EvTurnDone {
			continue
		}
		foundDone = true
		r.Attrs(func(a slog.Attr) bool {
			if a.Key == "view_rendered" {
				gotView = a.Value.String()
			}
			return true
		})
	}
	require.True(t, foundDone, "expected turn.done event")
	assert.Equal(t, out.View, gotView,
		"view_rendered on turn.done should match the outcome's view byte-for-byte")
}
