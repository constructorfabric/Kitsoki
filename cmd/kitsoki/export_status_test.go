package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
)

// cloakAppPath is the path to the cloak-of-darkness app relative to the
// cmd/kitsoki package. Keep in sync with the pattern used by inspect_test.go.
const cloakAppYAML = "../../testdata/apps/cloak/app.yaml"

// TestExportStatus_FromTrace_TableDriven is a table-driven test for the
// --from-trace export pipeline. It uses the hand-authored JSONL fixture
// under testdata/export_status/ and asserts the fields the proposal says
// must be derivable from a trace without flag overrides.
//
// Runtime budget: <50 ms per case. No real LLM calls.
func TestExportStatus_FromTrace_TableDriven(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("testdata", "export_status", "cloak_run.jsonl")

	// The fixture has 20 events across turns 1-3 for the cloak-of-darkness app.
	// The final state_path in the trace is "foyer" (the player went west, hung
	// the cloak, and returned east).

	cases := []struct {
		name             string
		currentStateFlag string
		sessionIDFlag    string
		startedAtFlag    string
		wantSessionID    string
		wantCurrentState string
		wantTurn         int
		wantTerminal     bool
		wantEventsLen    int
	}{
		{
			name:             "all derived from trace",
			wantSessionID:    "sess-cloak-001",
			wantCurrentState: "foyer",
			wantTurn:         3,
			wantTerminal:     false, // foyer is not terminal
			wantEventsLen:    20,
		},
		{
			name:             "current-state override to ended",
			currentStateFlag: "ended",
			wantSessionID:    "sess-cloak-001",
			wantCurrentState: "ended",
			wantTurn:         3,
			wantTerminal:     true, // ended IS terminal in cloak-of-darkness
			wantEventsLen:    20,
		},
		{
			name:             "session-id override",
			sessionIDFlag:    "custom-sess-42",
			wantSessionID:    "custom-sess-42",
			wantCurrentState: "foyer",
			wantTurn:         3,
			wantTerminal:     false,
			wantEventsLen:    20,
		},
		{
			name:             "started-at override",
			startedAtFlag:    "2024-01-01T00:00:00Z",
			wantSessionID:    "sess-cloak-001",
			wantCurrentState: "foyer",
			wantTurn:         3,
			wantTerminal:     false,
			wantEventsLen:    20,
		},
	}

	for _, tc := range cases {
		tc := tc // capture
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Load app to run the pipeline under test.
			def, err := app.Load(cloakAppYAML)
			require.NoError(t, err, "load cloak app.yaml")

			// Parse trace.
			events := parseTraceFixture(t, fixturePath)
			require.Len(t, events, tc.wantEventsLen, "events count must match fixture line count")

			// Synthesise header.
			header := runstatus.SessionHeaderFromTrace(def, events, runstatus.HeaderOverrides{
				SessionID:    tc.sessionIDFlag,
				CurrentState: tc.currentStateFlag,
				StartedAt:    tc.startedAtFlag,
			})

			assert.Equal(t, tc.wantSessionID, header.SessionID, "SessionID")
			assert.Equal(t, "cloak-of-darkness", header.AppID, "AppID always from AppDef")
			assert.Equal(t, tc.wantCurrentState, header.CurrentState, "CurrentState")
			assert.Equal(t, tc.wantTurn, header.Turn, "Turn")
			assert.Equal(t, tc.wantTerminal, header.Terminal, "Terminal")

			if tc.startedAtFlag != "" {
				want, _ := time.Parse(time.RFC3339, tc.startedAtFlag)
				assert.True(t, want.Equal(header.StartedAt), "StartedAt override should be applied")
			} else {
				// Derived from earliest trace event (2026-05-25T10:00:00.000Z).
				want := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
				assert.True(t, want.Equal(header.StartedAt), "StartedAt derived from earliest event")
			}
		})
	}
}

// TestExportStatus_WriteFile asserts that runExportFromTrace writes a valid
// JSON file to disk with the expected top-level structure.
func TestExportStatus_WriteFile(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "status.snapshot.json")

	err := runExportFromTrace(
		filepath.Join("testdata", "export_status", "cloak_run.jsonl"),
		cloakAppYAML,
		"", // currentState: derived
		"", // sessionID: derived
		"", // startedAt: derived
		outPath,
		false, // withMermaid: false — keep original behaviour for this test
	)
	require.NoError(t, err, "runExportFromTrace must succeed")

	// Read back and decode.
	raw, err := os.ReadFile(outPath)
	require.NoError(t, err, "output file must exist")

	var snap runstatus.Snapshot
	err = json.Unmarshal(raw, &snap)
	require.NoError(t, err, "output must be valid JSON matching Snapshot shape")

	assert.Equal(t, "sess-cloak-001", snap.Session.SessionID)
	assert.Equal(t, "cloak-of-darkness", snap.Session.AppID)
	assert.Equal(t, "foyer", snap.Session.CurrentState)
	assert.Equal(t, 3, snap.Session.Turn)
	assert.False(t, snap.Session.Terminal)

	require.NotNil(t, snap.App, "App must be serialised")
	assert.Equal(t, "cloak-of-darkness", snap.App.App.ID)

	// withMermaid=false: Source is empty, NodeMap is nil.
	assert.Empty(t, snap.Mermaid.Source)
	assert.Nil(t, snap.Mermaid.NodeMap)

	assert.Len(t, snap.Events, 20, "all 20 trace events must be included")
}

// TestExportStatus_WithMermaid asserts that --with-mermaid=true populates
// Mermaid.Source (non-empty) and Mermaid.NodeMap (at least one entry).
func TestExportStatus_WithMermaid(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "status-mermaid.snapshot.json")

	err := runExportFromTrace(
		filepath.Join("testdata", "export_status", "cloak_run.jsonl"),
		cloakAppYAML,
		"", // currentState: derived
		"", // sessionID: derived
		"", // startedAt: derived
		outPath,
		true, // withMermaid: true
	)
	require.NoError(t, err, "runExportFromTrace with --with-mermaid must succeed")

	raw, err := os.ReadFile(outPath)
	require.NoError(t, err, "output file must exist")

	var snap runstatus.Snapshot
	require.NoError(t, json.Unmarshal(raw, &snap), "output must be valid JSON matching Snapshot shape")

	assert.NotEmpty(t, snap.Mermaid.Source, "Mermaid.Source must be non-empty when --with-mermaid=true")
	require.NotNil(t, snap.Mermaid.NodeMap, "Mermaid.NodeMap must not be nil when --with-mermaid=true")
	assert.Greater(t, len(snap.Mermaid.NodeMap), 0, "Mermaid.NodeMap must have at least one entry")
}

// TestAggregateTaskDetails verifies that aggregateTaskDetails correctly
// correlates task.tool and task.end slog events to their oracle.task.complete
// event using task_trace_id / parent_trace_id.
//
// Runtime budget: <1 ms (pure in-memory slice manipulation, no I/O).
func TestAggregateTaskDetails(t *testing.T) {
	t.Parallel()

	traceID := "trace-abc-001"

	events := []runstatus.TraceEvent{
		{
			Msg: "task.start",
			Attrs: map[string]any{
				"task_trace_id": traceID,
				"agent":         "fixer",
			},
		},
		{
			Msg: "task.tool",
			Attrs: map[string]any{
				"tool":            "Read",
				"preview":         "workerpool/dispatcher.go",
				"parent_trace_id": traceID,
				"seq":             float64(1),
			},
		},
		{
			Msg: "task.tool",
			Attrs: map[string]any{
				"tool":            "Bash",
				"preview":         "go test ./...",
				"parent_trace_id": traceID,
				"seq":             float64(2),
			},
		},
		{
			Msg: "task.end",
			Attrs: map[string]any{
				"task_trace_id": traceID,
				"outcome":       "success",
				"files_changed": []any{"workerpool/dispatcher.go", "workerpool/dispatcher_test.go"},
			},
		},
		{
			Msg: "oracle.task.complete",
			Attrs: map[string]any{
				"call_id":       "call-xyz",
				"model":         "claude-3-sonnet",
				"duration_ms":   float64(5000),
				"task_trace_id": traceID,
			},
		},
		{
			// A different oracle verb — must not gain tool_calls.
			Msg: "oracle.decide.complete",
			Attrs: map[string]any{
				"call_id": "call-decide-999",
				"model":   "claude-3-sonnet",
			},
		},
	}

	runstatus.AggregateTaskDetails(events)

	// oracle.task.complete must have tool_calls and files_changed.
	taskComplete := events[4]
	require.Equal(t, "oracle.task.complete", taskComplete.Msg)

	toolCalls, ok := taskComplete.Attrs["tool_calls"].([]map[string]any)
	require.True(t, ok, "tool_calls must be a []map[string]any")
	require.Len(t, toolCalls, 2, "expect 2 tool calls")
	assert.Equal(t, "Read", toolCalls[0]["tool"])
	assert.Equal(t, "Bash", toolCalls[1]["tool"])

	filesChanged, ok := taskComplete.Attrs["files_changed"].([]map[string]any)
	require.True(t, ok, "files_changed must be a []map[string]any")
	require.Len(t, filesChanged, 2, "expect 2 files changed")
	assert.Equal(t, "workerpool/dispatcher.go", filesChanged[0]["path"])
	assert.Equal(t, "modified", filesChanged[0]["status"])

	// oracle.decide.complete must NOT gain tool_calls.
	decideComplete := events[5]
	require.Equal(t, "oracle.decide.complete", decideComplete.Msg)
	assert.Nil(t, decideComplete.Attrs["tool_calls"], "oracle.decide.complete must not gain tool_calls")
}

// TestTerminalDetection asserts terminal-state detection (exercised through
// SessionHeaderFromTrace's CurrentState override → Terminal) against the cloak
// app's known state graph:
//   - "ended" is terminal
//   - "foyer", "bar", "cloakroom", "bar.dark", "bar.lit" are NOT terminal
//   - empty / unknown paths are not terminal and do not panic
func TestTerminalDetection(t *testing.T) {
	t.Parallel()

	def, err := app.Load(cloakAppYAML)
	require.NoError(t, err)

	cases := []struct {
		path string
		want bool
	}{
		{"ended", true},
		{"foyer", false},
		{"bar", false},
		{"cloakroom", false},
		{"bar.dark", false},
		{"bar.lit", false},
		{"", false},
		{"nonexistent", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			t.Parallel()
			hdr := runstatus.SessionHeaderFromTrace(def, nil, runstatus.HeaderOverrides{CurrentState: tc.path})
			assert.Equal(t, tc.want, hdr.Terminal, "Terminal for state %q", tc.path)
		})
	}
}

// parseTraceFixture opens a JSONL trace fixture and parses it into TraceEvents,
// failing the test on any read/scan error.
func parseTraceFixture(t *testing.T, path string) []runstatus.TraceEvent {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	events, err := runstatus.ParseTrace(f, nil)
	require.NoError(t, err)
	return events
}
