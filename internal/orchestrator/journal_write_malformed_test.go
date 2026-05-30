package orchestrator

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/journal"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// TestJournalEntriesForEvents_MalformedTransitionPayloadLogged proves that a
// malformed TransitionApplied payload is surfaced via the package logger rather
// than silently zeroing from/to/intent in the state.transition journal entry.
//
// Before the fix, journalEntriesForEvents did `_ = json.Unmarshal(...)` and the
// error was discarded: the resulting entry carried empty from/to/intent and
// nothing was logged. This test captures slog.Default() output and asserts the
// unmarshal error is logged. It FAILS on the unfixed code (no log line emitted).
func TestJournalEntriesForEvents_MalformedTransitionPayloadLogged(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Payload is a JSON array, not the expected object — Unmarshal into the
	// struct fails.
	events := []store.Event{
		{
			Kind:    store.TransitionApplied,
			Payload: json.RawMessage(`["not","an","object"]`),
		},
	}

	entries := journalEntriesForEvents(
		"sess-1",
		7,
		time.Now(),
		events,
		world.New(),
		world.New(),
		"",
		"idle",
		"",
	)

	// The entry is still produced (with zeroed fields) — behaviour preserved.
	var sawTransition bool
	for _, e := range entries {
		if e.Kind == journal.KindStateTransition {
			sawTransition = true
		}
	}
	if !sawTransition {
		t.Fatalf("expected a state.transition entry to still be produced")
	}

	got := buf.String()
	if !strings.Contains(got, "unmarshal TransitionApplied payload") {
		t.Fatalf("expected malformed TransitionApplied payload to be logged, got: %q", got)
	}
	if !strings.Contains(got, "session_id=sess-1") {
		t.Fatalf("expected log line to identify the session, got: %q", got)
	}
}
