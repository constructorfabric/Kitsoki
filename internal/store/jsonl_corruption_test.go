package store_test

// jsonl_corruption_test.go — Layer 5: forward-compat / corruption catalogue.
//
// Table-driven tests for every reject path enumerated in the proposal.
// Each entry names a sub-case, the raw JSONL bytes to hand-craft, and the
// expected error substring (empty string means "must succeed").

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// corruptionCase describes one hand-crafted input for the corruption test table.
type corruptionCase struct {
	name        string
	content     string // raw bytes written to the JSONL file
	mustSucceed bool   // if true, OpenJSONL must NOT error
	wantErr     string // substring that must appear in the error (if mustSucceed==false)
}

// validHeader is the minimal well-formed header used across cases.
const validHeader = `{"kind":"session.header","schema_version":1,"written_at":"2024-01-01T00:00:00Z"}` + "\n"

// validEvent is a well-formed event line used across cases.
const validEvent = `{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}` + "\n"

var corruptionCases = []corruptionCase{
	// ── Unknown EventKind ───────────────────────────────────────────────────
	{
		name: "unknown_event_kind_succeeds",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"future.event.v99","payload":{"x":1}}` + "\n",
		mustSucceed: true,
		wantErr:     "",
	},

	// ── Newer schema_version ────────────────────────────────────────────────
	{
		name:    "newer_schema_version",
		content: `{"kind":"session.header","schema_version":99,"written_at":"2024-01-01T00:00:00Z"}` + "\n",
		wantErr: "schema_version",
	},

	// ── Missing header ──────────────────────────────────────────────────────
	{
		name:    "missing_header",
		content: validEvent,
		wantErr: "session.header",
	},

	// ── Duplicate header ────────────────────────────────────────────────────
	{
		name: "duplicate_header",
		content: validHeader +
			validHeader,
		wantErr: "duplicate",
	},

	// ── Header not on line 1 ────────────────────────────────────────────────
	{
		name: "header_not_on_line_1",
		content: validEvent + // line 1 is an event, not a header
			validHeader,
		wantErr: "session.header",
	},

	// ── Truncated last line (corruption, not crash) ──────────────────────────
	// The prior file is complete; the last line is torn (no trailing \n).
	// splitLines now requires every file to end with \n; a missing trailing
	// newline is "trace corrupted: missing trailing newline at EOF".
	{
		name: "truncated_last_line_incomplete_json",
		content: validHeader +
			validEvent +
			`{"turn":2,"seq":0,"ts":"2024-01-01T00:00:02Z","kind":"turn.end","paylo`, // no closing brace or \n
		wantErr: "missing trailing newline",
	},

	// ── NUL byte in a line ──────────────────────────────────────────────────
	// NUL bytes in a JSONL line are now explicitly rejected at read time.
	{
		name: "nul_byte_in_event_line",
		content: validHeader +
			"{\"turn\":1,\"seq\":0,\"ts\":\"2024-01-01T00:00:01Z\",\"kind\":\"turn.start\",\"payload\":\x00{}}\n",
		wantErr: "NUL byte",
	},

	// ── BOM at start of file ────────────────────────────────────────────────
	{
		name:    "bom_at_start",
		content: "\xEF\xBB\xBF" + validHeader + validEvent,
		wantErr: "JSON", // BOM causes the header JSON to fail to parse
	},

	// ── CRLF line endings ───────────────────────────────────────────────────
	// JSONL uses LF-only. CRLF endings are now a hard error — the \r is not
	// stripped. splitLines detects \r immediately before \n and rejects it.
	{
		name:    "crlf_line_endings",
		content: strings.ReplaceAll(validHeader+validEvent, "\n", "\r\n"),
		wantErr: "CRLF",
	},

	// ── Missing trailing newline at EOF ─────────────────────────────────────
	// A file ending without \n is now rejected: "trace corrupted: missing
	// trailing newline at EOF".
	{
		name: "missing_trailing_newline",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}`, // no trailing \n
		wantErr: "missing trailing newline",
	},

	// ── Duplicate (turn, seq) ────────────────────────────────────────────────
	{
		name: "duplicate_turn_seq",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}` + "\n" +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:02Z","kind":"turn.end","payload":{}}` + "\n",
		wantErr: "duplicate (turn,seq)",
	},

	// ── Out-of-order (turn, seq) ─────────────────────────────────────────────
	{
		name: "out_of_order_turn_seq",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}` + "\n" +
			`{"turn":1,"seq":2,"ts":"2024-01-01T00:00:02Z","kind":"turn.end","payload":{}}` + "\n" +
			`{"turn":1,"seq":1,"ts":"2024-01-01T00:00:03Z","kind":"machine.transition","payload":{}}` + "\n",
		wantErr: "gap in seq",
	},

	// ── Gap in seq within a turn ─────────────────────────────────────────────
	{
		name: "gap_in_seq",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}` + "\n" +
			`{"turn":1,"seq":2,"ts":"2024-01-01T00:00:02Z","kind":"turn.end","payload":{}}` + "\n",
		wantErr: "gap in seq",
	},

	// ── True out-of-order within a turn (cross-turn late arrival) ────────────────
	// Finding 2.9: turn 1 seq 1 arrives after turn 2 has already started.
	// The monotonicity check for "turn 1 seq 1" only looks at seqByTurn[1]
	// which was last at seq 0; arriving as seq 1 would be a valid gap-free
	// continuation — BUT it arrives after turn 2 started, which means turn 1
	// has effectively been "closed" by the arrival of a higher turn.
	// The loadAndValidate reader tracks maxTurn; a turn number <= maxTurn that
	// appears out-of-order relative to a later turn is an error.
	{
		name: "out_of_order_cross_turn",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}` + "\n" +
			`{"turn":2,"seq":0,"ts":"2024-01-01T00:00:02Z","kind":"turn.start","payload":{}}` + "\n" +
			`{"turn":1,"seq":1,"ts":"2024-01-01T00:00:03Z","kind":"turn.end","payload":{}}` + "\n",
		wantErr: "out-of-order",
	},

	// ── Truncated mid-file line (not the last line) ──────────────────────────────
	// Finding 2.9: a torn line in the middle of the file (not at EOF) — the
	// adjacent line completes with \n so splitLines passes, but the JSON is
	// invalid. The reader must error on the malformed JSON, not silently skip it.
	{
		name: "truncated_mid_file_line",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}` + "\n" +
			`{"turn":1,"seq":1,"ts":"2024-01-01T00:00:02Z","kind":"turn.e` + "\n" +
			`{"turn":1,"seq":2,"ts":"2024-01-01T00:00:03Z","kind":"turn.end","payload":{}}` + "\n",
		wantErr: "JSON",
	},

	// ── Oversize line (now accepted) ────────────────────────────────────────────
	// PIPE_BUF limit was removed to allow arbitrary-sized events.
	{
		name: "oversize_line_accepted",
		content: validHeader +
			`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{"data":"` +
			strings.Repeat("x", 5000) + `"}}` + "\n",
		mustSucceed: true,
	},
}

// TestLayer5_CorruptionCatalogue runs the table-driven corruption cases.
// Each case is independent and parallel.
func TestLayer5_CorruptionCatalogue(t *testing.T) {
	t.Parallel()
	for _, tc := range corruptionCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := filepath.Join(dir, "corrupt.jsonl")
			require.NoError(t, os.WriteFile(path, []byte(tc.content), 0o644))

			_, err := store.OpenJSONL(path)
			if tc.mustSucceed {
				require.NoError(t, err, "case %q: expected success", tc.name)
				return
			}
			if tc.wantErr == "" {
				// Undetermined / known gap — just verify no panic. Either outcome ok.
				return
			}
			require.Error(t, err, "case %q: expected error", tc.name)
			require.Contains(t, err.Error(), tc.wantErr,
				"case %q: error must contain %q; got: %v", tc.name, tc.wantErr, err)
		})
	}
}

// ─── Focused assertions for the proposal-required cases ────────────────────

// TestLayer5_UnknownEventKind_BuildJourneyIgnores verifies that BuildJourney
// silently ignores unknown event kinds (forward-compat shim).
func TestLayer5_UnknownEventKind_BuildJourneyIgnores(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "unknown.jsonl")
	content := validHeader +
		`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{}}` + "\n" +
		`{"turn":1,"seq":1,"ts":"2024-01-01T00:00:02Z","kind":"future.kind.v42","payload":{"data":"x"}}` + "\n" +
		`{"turn":1,"seq":2,"ts":"2024-01-01T00:00:03Z","kind":"machine.transition","payload":{"from":"a","to":"b"}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	s, err := store.OpenJSONL(path)
	require.NoError(t, err, "file with unknown event kind must open successfully")
	defer s.Close()

	hist := s.History()
	require.Len(t, hist, 3, "all three lines must be in history")

	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	js, foldErr := store.BuildJourney(def, "a", world.New(), hist)
	require.NoError(t, foldErr, "BuildJourney must ignore unknown event kind")
	require.Equal(t, app.StatePath("b"), js.State, "TransitionApplied must still fire")
}

// TestLayer5_NewerSchemaVersion_NamesVersions verifies the newer schema_version
// error names both the on-disk version and the highest supported.
func TestLayer5_NewerSchemaVersion_NamesVersions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "future.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(
		`{"kind":"session.header","schema_version":42,"written_at":"2024-01-01T00:00:00Z"}`+"\n",
	), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "42", "error must name on-disk version")
	require.Contains(t, err.Error(), "schema_version", "error must mention schema_version")
}

// TestLayer5_MissingHeader verifies the "trace missing SessionHeader on line 1" message.
func TestLayer5_MissingHeader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "no_header.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(validEvent), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.header")
}

// TestLayer5_DuplicateHeader verifies that a second SessionHeader line errors.
func TestLayer5_DuplicateHeader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "dup_header.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(validHeader+validHeader), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate")
}

// TestLayer5_HeaderNotOnLine1 verifies that a header on line 2+ errors.
func TestLayer5_HeaderNotOnLine1(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "hdr_line2.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(validEvent+validHeader), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "session.header")
}

// TestLayer5_BOMAtStart verifies BOM is rejected.
func TestLayer5_BOMAtStart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bom.jsonl")
	require.NoError(t, os.WriteFile(path, []byte("\xEF\xBB\xBF"+validHeader+validEvent), 0o644))

	_, err := store.OpenJSONL(path)
	require.Error(t, err, "BOM at start of file must be rejected")
}

// ─── Oversize line (read side) ────────────────────────────────────────────────

// TestLayer5_OversizeLine_ReadSide verifies that a line exceeding 4096 bytes
// is now accepted (PIPE_BUF limit was removed).
func TestLayer5_OversizeLine_ReadSide(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bigline.jsonl")

	// Craft a line with a large payload (> 4096 bytes).
	bigVal := strings.Repeat("x", 5000)
	bigLine := fmt.Sprintf(`{"turn":1,"seq":0,"ts":"2024-01-01T00:00:01Z","kind":"turn.start","payload":{"data":%q}}`,
		bigVal) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(validHeader+bigLine), 0o644))

	// Large lines must now be accepted.
	sink, err := store.OpenJSONL(path)
	require.NoError(t, err, "large line must be accepted")
	defer sink.Close()

	hist := sink.History()
	require.Len(t, hist, 1)
}
