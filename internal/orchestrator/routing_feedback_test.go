package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// newRoutingFeedbackTestOrch builds an orchestrator wired with a real
// SQLite-backed journal writer/reader (matching TestAttachSession's rig) so
// RecordRoutingFeedback's standalone journal write can be read back.
func newRoutingFeedbackTestOrch(t *testing.T) (*orchestrator.Orchestrator, journal.Reader, app.SessionID) {
	t.Helper()
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jw, err := journal.NewSQLiteWriter(s.DB())
	require.NoError(t, err)
	jr, err := journal.NewSQLiteReader(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, nil, orchestrator.WithJournalWriter(jw))

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	return orch, jr, sid
}

// routingFeedbackEntries filters a session's typed journal replay down to
// KindRoutingFeedback entries, decoded into journal.RoutingFeedbackEvent.
func routingFeedbackEntries(t *testing.T, jr journal.Reader, sid app.SessionID) []journal.RoutingFeedbackEvent {
	t.Helper()
	seq, stop := jr.ReplayTyped(sid)
	defer func() { require.NoError(t, stop()) }()

	var out []journal.RoutingFeedbackEvent
	for e := range seq {
		if e.Kind != journal.KindRoutingFeedback {
			continue
		}
		var body journal.RoutingFeedbackEvent
		require.NoError(t, json.Unmarshal(e.Body, &body))
		out = append(out, body)
	}
	return out
}

// TestRecordRoutingFeedback_Up verifies a "up" verdict journals correctly and
// carries the phrase/state/intent/tier the caller supplied.
func TestRecordRoutingFeedback_Up(t *testing.T) {
	orch, jr, sid := newRoutingFeedbackTestOrch(t)
	ctx := context.Background()

	err := orch.RecordRoutingFeedback(ctx, sid, "foyer", "look", "look around", "deterministic", orchestrator.RoutingFeedbackUp)
	require.NoError(t, err)

	entries := routingFeedbackEntries(t, jr, sid)
	require.Len(t, entries, 1)
	require.Equal(t, journal.RoutingFeedbackEvent{
		Phrase:  "look around",
		State:   "foyer",
		Intent:  "look",
		Tier:    "deterministic",
		Verdict: "up",
	}, entries[0])
}

// TestRecordRoutingFeedback_Down verifies the dissatisfaction verdict.
func TestRecordRoutingFeedback_Down(t *testing.T) {
	orch, jr, sid := newRoutingFeedbackTestOrch(t)
	ctx := context.Background()

	err := orch.RecordRoutingFeedback(ctx, sid, "foyer", "go_north", "go south please", "main-llm", orchestrator.RoutingFeedbackDown)
	require.NoError(t, err)

	entries := routingFeedbackEntries(t, jr, sid)
	require.Len(t, entries, 1)
	require.Equal(t, "down", entries[0].Verdict)
	require.Equal(t, "go_north", entries[0].Intent)
}

// TestRecordRoutingFeedback_RejectsUnknownVerdict proves the method validates
// the verdict rather than silently journaling garbage.
func TestRecordRoutingFeedback_RejectsUnknownVerdict(t *testing.T) {
	orch, jr, sid := newRoutingFeedbackTestOrch(t)
	ctx := context.Background()

	err := orch.RecordRoutingFeedback(ctx, sid, "foyer", "look", "look around", "deterministic", orchestrator.RoutingFeedbackVerdict("sideways"))
	require.Error(t, err)

	entries := routingFeedbackEntries(t, jr, sid)
	require.Empty(t, entries, "an invalid verdict must not journal anything")
}

// TestRecordRoutingFeedback_RejectsNoRoutedTurn proves a caller can't attach
// feedback with no (state, intent) to point at.
func TestRecordRoutingFeedback_RejectsNoRoutedTurn(t *testing.T) {
	orch, jr, sid := newRoutingFeedbackTestOrch(t)
	ctx := context.Background()

	err := orch.RecordRoutingFeedback(ctx, sid, "", "", "look around", "", orchestrator.RoutingFeedbackUp)
	require.Error(t, err)

	entries := routingFeedbackEntries(t, jr, sid)
	require.Empty(t, entries)
}
