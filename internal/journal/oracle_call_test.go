package journal_test

// TestOracleCallRoundTrip verifies that KindOracleCall entries can be inserted
// into the journal and loaded back via LoadOracleCalls keyed by call_id.
//
// Runtime budget: <10 ms (in-memory SQLite, no real LLM calls).
import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
)

func TestOracleCallRoundTrip(t *testing.T) {
	t.Parallel()

	db := openTestDB(t)
	w, err := journal.NewSQLiteWriter(db)
	require.NoError(t, err, "create journal writer")

	sid := app.SessionID("test-session-oracle-001")
	turn := app.TurnNumber(2)
	callID := "call-abc-123"

	// Build a minimal OracleCallBody-shaped payload.
	body := map[string]any{
		"call_id":       callID,
		"verb":          "decide",
		"agent":         "my-agent",
		"model":         "claude-3-5-sonnet",
		"duration_ms":   int64(1234),
		"system_prompt": "You are a helpful assistant.",
		"prompt":        "What is the capital of France?",
		"input": map[string]any{
			"schema_path": "schemas/decision.json",
		},
		"response": map[string]any{
			"json":     map[string]any{"decision": "paris"},
			"decision": "paris",
		},
	}
	bodyJSON, err := json.Marshal(body)
	require.NoError(t, err, "marshal body")

	entry := journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     5,
		Kind:    journal.KindOracleCall,
		Body:    bodyJSON,
	}
	require.NoError(t, w.Append(entry), "append oracle call entry")

	// Also write a second entry for a different call_id to verify keying.
	callID2 := "call-def-456"
	body2 := map[string]any{
		"call_id": callID2,
		"verb":    "ask",
		"model":   "claude-3-haiku",
		"prompt":  "Hello!",
	}
	bodyJSON2, _ := json.Marshal(body2)
	entry2 := journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     6,
		Kind:    journal.KindOracleCall,
		Body:    bodyJSON2,
	}
	require.NoError(t, w.Append(entry2), "append second oracle call entry")

	// Load oracle calls and verify both entries are keyed by call_id.
	calls, err := journal.LoadOracleCalls(db, sid)
	require.NoError(t, err, "LoadOracleCalls must succeed")
	require.Len(t, calls, 2, "expect 2 oracle call entries")

	// Verify first entry.
	raw1, ok1 := calls[callID]
	require.True(t, ok1, "call_id %q must be present", callID)
	var loaded1 map[string]any
	require.NoError(t, json.Unmarshal(raw1, &loaded1))
	assert.Equal(t, "decide", loaded1["verb"])
	assert.Equal(t, "my-agent", loaded1["agent"])
	assert.Equal(t, "You are a helpful assistant.", loaded1["system_prompt"])

	// Verify second entry.
	raw2, ok2 := calls[callID2]
	require.True(t, ok2, "call_id %q must be present", callID2)
	var loaded2 map[string]any
	require.NoError(t, json.Unmarshal(raw2, &loaded2))
	assert.Equal(t, "ask", loaded2["verb"])

	// Entries from a different session must NOT be returned.
	otherSID := app.SessionID("other-session")
	otherCalls, err := journal.LoadOracleCalls(db, otherSID)
	require.NoError(t, err)
	assert.Empty(t, otherCalls, "entries from a different session must not bleed through")
}

// TestKindOracleCall_IsTypedKind verifies that KindOracleCall is classified as
// a typed (semantic) kind, not a patch or checkpoint kind.
func TestKindOracleCall_IsTypedKind(t *testing.T) {
	t.Parallel()

	assert.True(t, journal.IsTypedKind(journal.KindOracleCall),
		"KindOracleCall must be a typed kind")
	assert.False(t, journal.IsPatchKind(journal.KindOracleCall),
		"KindOracleCall must not be a patch kind")
	assert.False(t, journal.IsCheckpointKind(journal.KindOracleCall),
		"KindOracleCall must not be a checkpoint kind")
}
