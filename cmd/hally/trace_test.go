package main

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cannedTrace is a synthetic JSONL trace file (two turns).
const cannedTrace = `{"time":"2026-04-21T13:02:45.123Z","level":"DEBUG","msg":"turn.start","session_id":"abc123","turn":1,"state_path":"foyer","input":"go south","mode":"normal"}
{"time":"2026-04-21T13:02:45.200Z","level":"DEBUG","msg":"harness.request","session_id":"abc123","turn":1,"state_path":"foyer","prompt_bytes":1240}
{"time":"2026-04-21T13:02:45.380Z","level":"DEBUG","msg":"harness.oracle_hit","session_id":"abc123","turn":1,"state_path":"foyer","intent":"go","slots":{"direction":"south"}}
{"time":"2026-04-21T13:02:45.385Z","level":"DEBUG","msg":"turn.routed","session_id":"abc123","turn":1,"state_path":"foyer","dur":"185ms","outcome":"hit","intent":"go"}
{"time":"2026-04-21T13:02:45.386Z","level":"DEBUG","msg":"turn.stepped","session_id":"abc123","turn":1,"state_path":"foyer","intent":"go"}
{"time":"2026-04-21T13:02:45.387Z","level":"DEBUG","msg":"machine.guard.eval","session_id":"abc123","turn":1,"state_path":"foyer","expr":"slots.direction == \"south\"","result":true}
{"time":"2026-04-21T13:02:45.388Z","level":"DEBUG","msg":"machine.guard.winner","session_id":"abc123","turn":1,"state_path":"foyer","expr":"slots.direction == \"south\"","target":"bar.dark"}
{"time":"2026-04-21T13:02:45.389Z","level":"DEBUG","msg":"machine.transition","session_id":"abc123","turn":1,"state_path":"foyer","from":"foyer","to":"bar.dark","intent":"go"}
{"time":"2026-04-21T13:02:45.390Z","level":"DEBUG","msg":"machine.effect.applied","session_id":"abc123","turn":1,"state_path":"foyer","type":"set","key":"disturbance","before":0,"after":1}
{"time":"2026-04-21T13:02:45.400Z","level":"DEBUG","msg":"turn.persisted","session_id":"abc123","turn":1,"state_path":"foyer","count":5,"outcome":"transitioned"}
{"time":"2026-04-21T13:02:45.410Z","level":"DEBUG","msg":"turn.done","session_id":"abc123","turn":1,"state_path":"foyer","mode":"transitioned","view_bytes":237,"new_state":"bar.dark"}
{"time":"2026-04-21T13:02:50.000Z","level":"DEBUG","msg":"turn.start","session_id":"abc123","turn":2,"state_path":"bar.dark","input":"read message","mode":"normal"}
{"time":"2026-04-21T13:02:50.500Z","level":"DEBUG","msg":"turn.done","session_id":"abc123","turn":2,"state_path":"bar.dark","mode":"completed","view_bytes":45,"new_state":"ended"}
`

// TestPrettyPrintStructure feeds the canned JSONL and checks the output structure.
func TestPrettyPrintStructure(t *testing.T) {
	r := strings.NewReader(cannedTrace)
	var buf bytes.Buffer

	err := prettyPrint(r, &buf)
	require.NoError(t, err)

	out := buf.String()
	t.Logf("pretty output:\n%s", out)

	// Must have at least two "TURN START" sections (one per turn).
	// The pretty output formats turn.start as "START" after stripping the "turn." prefix.
	turnStartCount := strings.Count(out, "START")
	assert.GreaterOrEqual(t, turnStartCount, 2, "expected at least 2 TURN START sections")

	// Should contain HARNESS oracle_hit.
	assert.Contains(t, out, "oracle_hit", "expected oracle_hit in output")

	// Should contain MACHINE guard.winner (pretty-printer strips "machine." prefix).
	assert.Contains(t, out, "guard.winner", "expected guard.winner in output")

	// Should contain MACHINE transition (pretty-printer strips "machine." prefix).
	assert.Contains(t, out, "transition", "expected transition in MACHINE output")

	// Should contain STORE events if present — or at least MACHINE and HARNESS lines.
	assert.Contains(t, out, "HARNESS", "expected HARNESS label")
	assert.Contains(t, out, "MACHINE", "expected MACHINE label")

	// Sessions should be separated by a blank line.
	assert.Contains(t, out, "\n\n", "expected blank line between turns")
}

// TestPrettyPrintWarnRecords renders WARN and ERROR slog records with a
// distinct visual treatment so they stand out from the DEBUG firehose.
// The bridge from slog.Default() into the trace logger means harness Warns
// (e.g. retry-after-parse-failure) now flow through here.
func TestPrettyPrintWarnRecords(t *testing.T) {
	input := `{"time":"2026-04-23T07:00:00Z","level":"WARN","msg":"harness/claude-cli: retrying after parse failure","attempt":1}
{"time":"2026-04-23T07:00:01Z","level":"ERROR","msg":"something exploded","cause":"boom"}
`
	var buf bytes.Buffer
	err := prettyPrint(strings.NewReader(input), &buf)
	require.NoError(t, err)
	out := buf.String()
	assert.Contains(t, out, "WARN", "WARN label should be present")
	assert.Contains(t, out, "retrying after parse failure")
	assert.Contains(t, out, "ERROR", "ERROR label should be present")
	assert.Contains(t, out, "something exploded")
}

// TestPrettyPrintEmptyInput produces no output (no error) for empty input.
func TestPrettyPrintEmptyInput(t *testing.T) {
	r := strings.NewReader("")
	var buf bytes.Buffer
	err := prettyPrint(r, &buf)
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

// TestPrettyPrintInvalidJSON falls back to raw line on bad JSON.
func TestPrettyPrintInvalidJSON(t *testing.T) {
	r := strings.NewReader("not json at all\n")
	var buf bytes.Buffer
	err := prettyPrint(r, &buf)
	require.NoError(t, err)
	// Raw line should be echoed.
	assert.Contains(t, buf.String(), "not json at all")
}

// TestBuildTraceLoggerNoConfig returns default logger when no path is configured.
func TestBuildTraceLoggerNoConfig(t *testing.T) {
	cfg := TraceConfig{}
	logger, cleanup, err := BuildTraceLogger(cfg)
	require.NoError(t, err)
	require.NotNil(t, logger)
	require.NotNil(t, cleanup)
	cleanup()
}

// TestBuildTraceLoggerBridgesBothSinks verifies that a package-level
// slog.Warn reaches both the JSONL and the pretty sinks after the trace
// logger is installed as slog.Default(), matching what runCmd does.
func TestBuildTraceLoggerBridgesBothSinks(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := dir + "/trace.jsonl"
	prettyPath := dir + "/trace.log"

	logger, cleanup, err := BuildTraceLogger(TraceConfig{
		JSONLPath:  jsonlPath,
		PrettyPath: prettyPath,
		Level:      slog.LevelInfo,
	})
	require.NoError(t, err)

	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	slog.Warn("harness/claude-cli: retrying after parse failure",
		"attempt", 1, "err", "fake parse error")

	cleanup()

	jsonl, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)
	require.Contains(t, string(jsonl), "retrying after parse failure")
	require.Contains(t, string(jsonl), `"level":"WARN"`)

	pretty, err := os.ReadFile(prettyPath)
	require.NoError(t, err)
	require.Contains(t, string(pretty), "retrying after parse failure",
		"expected the WARN message to also reach the pretty sink")
}

// TestBuildTraceLoggerBridgesSlogDefault verifies that after SetDefault-ing
// the trace logger, a package-level slog.Warn reaches the JSONL sink. This
// is what lets fence/retry warnings from deep in the harness stack land in
// --trace rather than disappearing into the TUI-swallowed stderr.
func TestBuildTraceLoggerBridgesSlogDefault(t *testing.T) {
	dir := t.TempDir()
	jsonlPath := dir + "/trace.jsonl"

	logger, cleanup, err := BuildTraceLogger(TraceConfig{
		JSONLPath: jsonlPath,
		Level:     slog.LevelInfo,
	})
	require.NoError(t, err)
	t.Cleanup(cleanup)

	// Swap slog.Default() to the trace logger, emulating what runCmd does.
	prev := slog.Default()
	slog.SetDefault(logger)
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Fire a package-level warn. If the bridge works, this line lands in
	// the JSONL regardless of caller passing the logger around.
	slog.Warn("harness/test: probe warning", "kind", "probe")

	// Flush by running cleanup once; but t.Cleanup defers it. Force sync by
	// reading after a microsleep isn't reliable — rely on the bufio.Writer's
	// auto-flush on close, so we explicitly run cleanup here then re-skip it.
	cleanup()
	// Prevent double-close in the t.Cleanup above by swapping it to a noop.
	// (Test helpers in this package don't expose a re-runnable cleanup, so
	// just accept a second call is a no-op on closed *os.File.)

	data, err := os.ReadFile(jsonlPath)
	require.NoError(t, err)
	require.Contains(t, string(data), "harness/test: probe warning",
		"expected slog.Warn to reach the JSONL sink via SetDefault bridge; got:\n%s",
		string(data))
	require.Contains(t, string(data), `"level":"WARN"`,
		"expected WARN level in JSONL; got:\n%s", string(data))
}
