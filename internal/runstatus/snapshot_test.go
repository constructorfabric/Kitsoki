package runstatus_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
)

// TestSnapshot_JSONRoundTrip marshals a fully populated Snapshot to JSON and
// unmarshals it back, asserting every field survives the round-trip.
func TestSnapshot_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)

	original := runstatus.Snapshot{
		Session: runstatus.SessionHeader{
			SessionID:    "sess-abc",
			AppID:        "cloak-of-darkness",
			CurrentState: "foyer",
			Turn:         3,
			StartedAt:    now,
			Terminal:     false,
		},
		App: &app.AppDef{
			App: app.AppMeta{
				ID:      "cloak-of-darkness",
				Version: "0.1.0",
			},
		},
		Mermaid: runstatus.MermaidSnapshot{
			Source: "flowchart LR\n  foyer --> bar",
			NodeMap: map[string]runstatus.NodeRef{
				"foyer": {Kind: "state", Ref: "foyer"},
				"bar":   {Kind: "state", Ref: "bar"},
			},
		},
		Events: []runstatus.TraceEvent{
			{
				Time:      now,
				Level:     "DEBUG",
				Msg:       "turn.start",
				SessionID: "sess-abc",
				Turn:      1,
				StatePath: "foyer",
				Attrs:     map[string]any{"input": "go west"},
			},
		},
	}

	b, err := json.MarshalIndent(original, "", "  ")
	require.NoError(t, err, "marshal must not fail")

	var got runstatus.Snapshot
	err = json.Unmarshal(b, &got)
	require.NoError(t, err, "unmarshal must not fail")

	assert.Equal(t, original.Session.SessionID, got.Session.SessionID)
	assert.Equal(t, original.Session.AppID, got.Session.AppID)
	assert.Equal(t, original.Session.CurrentState, got.Session.CurrentState)
	assert.Equal(t, original.Session.Turn, got.Session.Turn)
	assert.True(t, original.Session.StartedAt.Equal(got.Session.StartedAt))
	assert.Equal(t, original.Session.Terminal, got.Session.Terminal)

	require.NotNil(t, got.App)
	assert.Equal(t, original.App.App.ID, got.App.App.ID)
	assert.Equal(t, original.App.App.Version, got.App.App.Version)

	assert.Equal(t, original.Mermaid.Source, got.Mermaid.Source)
	assert.Equal(t, original.Mermaid.NodeMap["foyer"], got.Mermaid.NodeMap["foyer"])
	assert.Equal(t, original.Mermaid.NodeMap["bar"], got.Mermaid.NodeMap["bar"])

	require.Len(t, got.Events, 1)
	assert.True(t, original.Events[0].Time.Equal(got.Events[0].Time))
	assert.Equal(t, original.Events[0].Level, got.Events[0].Level)
	assert.Equal(t, original.Events[0].Msg, got.Events[0].Msg)
	assert.Equal(t, original.Events[0].SessionID, got.Events[0].SessionID)
	assert.Equal(t, original.Events[0].Turn, got.Events[0].Turn)
	assert.Equal(t, original.Events[0].StatePath, got.Events[0].StatePath)
	assert.Equal(t, "go west", got.Events[0].Attrs["input"])
}

// TestTraceEvent_UnmarshalJSON_UnknownKeysInAttrs asserts that fields not in
// the known-keys set land in Attrs and are not silently dropped.
func TestTraceEvent_UnmarshalJSON_UnknownKeysInAttrs(t *testing.T) {
	t.Parallel()

	raw := `{
		"time": "2026-05-25T10:00:00Z",
		"level": "DEBUG",
		"msg": "machine.transition",
		"session_id": "sess-xyz",
		"turn": 2,
		"state_path": "bar.dark",
		"intent": "go",
		"handler": "host.cloak.exit",
		"duration_ms": 42
	}`

	var ev runstatus.TraceEvent
	err := json.Unmarshal([]byte(raw), &ev)
	require.NoError(t, err)

	assert.Equal(t, "DEBUG", ev.Level)
	assert.Equal(t, "machine.transition", ev.Msg)
	assert.Equal(t, "sess-xyz", ev.SessionID)
	assert.Equal(t, 2, ev.Turn)
	assert.Equal(t, "bar.dark", ev.StatePath)

	// Unknown keys must land in Attrs.
	require.NotNil(t, ev.Attrs, "Attrs must be populated for unknown keys")
	assert.Equal(t, "go", ev.Attrs["intent"])
	assert.Equal(t, "host.cloak.exit", ev.Attrs["handler"])
	// JSON numbers without a type hint decode as float64.
	assert.EqualValues(t, 42, ev.Attrs["duration_ms"])
}

// TestTraceEvent_UnmarshalJSON_KnownKeysNotInAttrs asserts that well-known
// keys are NOT duplicated in Attrs (they are promoted to named fields only).
func TestTraceEvent_UnmarshalJSON_KnownKeysNotInAttrs(t *testing.T) {
	t.Parallel()

	raw := `{"time":"2026-05-25T10:00:00Z","level":"INFO","msg":"turn.end","session_id":"s1","turn":5,"state_path":"ended"}`

	var ev runstatus.TraceEvent
	require.NoError(t, json.Unmarshal([]byte(raw), &ev))

	// Known keys must NOT appear in Attrs.
	// "attrs" is also a known key (round-trip safety); its contents are merged in directly.
	for _, k := range []string{"time", "level", "msg", "session_id", "turn", "state_path", "attrs"} {
		_, found := ev.Attrs[k]
		assert.False(t, found, "known key %q must not be duplicated in Attrs", k)
	}
}

// TestTraceEvent_EmptyAttrsWhenNoUnknownKeys asserts that Attrs is nil (not an
// empty map) when the event record has only well-known keys, to avoid
// producing `"attrs":{}` in the serialised output.
func TestTraceEvent_EmptyAttrsWhenNoUnknownKeys(t *testing.T) {
	t.Parallel()

	raw := `{"time":"2026-05-25T10:00:00Z","level":"DEBUG","msg":"turn.start","session_id":"s1","turn":1,"state_path":"foyer"}`

	var ev runstatus.TraceEvent
	require.NoError(t, json.Unmarshal([]byte(raw), &ev))

	assert.Nil(t, ev.Attrs, "Attrs should be nil when no unknown keys are present")
}
