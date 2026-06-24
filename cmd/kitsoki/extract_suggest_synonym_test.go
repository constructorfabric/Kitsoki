package main

// Tests for `kitsoki extract suggest-synonym`.
//
// All tests are deterministic (no LLM, no real sessions). They build an
// in-memory SQLite journal directly, write host.invoked + host.returned
// entries for host.agent.extract calls, then exercise runExtractSuggestSynonym
// via execRoot or the inner helper.
//
// Patterns follow session_continue_journal_test.go and the journal sqlite_test.go
// test helpers, but the DB lives entirely in-memory (:memory:) for speed.

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
)

// appendExtractPair writes a matching host.invoked + host.returned pair for a
// host.agent.extract call into the journal.
func appendExtractPair(
	t *testing.T,
	w journal.Writer,
	sid app.SessionID,
	turn app.TurnNumber,
	invokedSeq, returnedSeq int,
	input string,
	resolvers []any,
	submitted any,
	resolvedBy string,
) {
	t.Helper()

	// HostInvoked body.
	callArgs := map[string]any{
		"input":     input,
		"resolvers": resolvers,
	}
	invokedBody, err := json.Marshal(map[string]any{
		"namespace": "host.agent.extract",
		"args":      callArgs,
	})
	if err != nil {
		t.Fatalf("marshal invoked body: %v", err)
	}
	if err := w.Append(journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     invokedSeq,
		Kind:    journal.KindHostInvoked,
		Body:    json.RawMessage(invokedBody),
	}); err != nil {
		t.Fatalf("Append invoked: %v", err)
	}

	// HostReturned body.
	returnedBody, err := json.Marshal(map[string]any{
		"namespace": "host.agent.extract",
		"data": map[string]any{
			"submitted":         submitted,
			"resolved_by":       resolvedBy,
			"claude_session_id": "",
		},
	})
	if err != nil {
		t.Fatalf("marshal returned body: %v", err)
	}
	if err := w.Append(journal.Entry{
		Ts:      time.Now(),
		Session: sid,
		Turn:    turn,
		Seq:     returnedSeq,
		Kind:    journal.KindHostReturned,
		Body:    json.RawMessage(returnedBody),
	}); err != nil {
		t.Fatalf("Append returned: %v", err)
	}
}

// TestSuggestSynonym_LLMTier_ProposesEntry verifies that an LLM-resolved call
// produces a YAML snippet and diff hint in the output.
func TestSuggestSynonym_LLMTier_ProposesEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS journal (
    session_id TEXT NOT NULL, turn INTEGER NOT NULL, seq INTEGER NOT NULL,
    ts INTEGER NOT NULL, kind TEXT NOT NULL, doc TEXT, doc_version INTEGER,
    body_json TEXT NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;
CREATE INDEX IF NOT EXISTS journal_doc_idx ON journal (session_id, doc, doc_version);
`); err != nil {
		t.Fatalf("DDL: %v", err)
	}

	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}

	sid := app.SessionID("sess-llm-1")
	synonymsFile := "/app/synonyms.yaml"
	appendExtractPair(t, w, sid, 3, 0, 1,
		"go north",
		[]any{map[string]any{"synonyms": synonymsFile}},
		map[string]any{"direction": "north"},
		"llm",
	)
	_ = db.Close()

	out, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		string(sid), "3",
	)
	if execErr != nil {
		t.Fatalf("suggest-synonym: %v\n%s", execErr, out)
	}

	// Output must contain the input phrase as a quoted YAML key.
	if !strings.Contains(out, `"go north"`) {
		t.Errorf("output missing quoted input phrase:\n%s", out)
	}
	// Output must contain the payload.
	if !strings.Contains(out, `"direction"`) {
		t.Errorf("output missing payload direction field:\n%s", out)
	}
	// Diff hint must name the synonyms file.
	if !strings.Contains(out, synonymsFile) {
		t.Errorf("output missing synonyms file path %q:\n%s", synonymsFile, out)
	}
	// Output must contain the diff marker.
	if !strings.Contains(out, "+") {
		t.Errorf("output missing diff + marker:\n%s", out)
	}
}

// TestSuggestSynonym_DeterministicTier_NoSuggestion verifies that a call
// resolved by the synonyms tier (not llm) prints a "not llm" notice and does
// not propose a YAML entry.
func TestSuggestSynonym_DeterministicTier_NoSuggestion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db := openTestDBForExtract(t, dbPath)
	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}

	sid := app.SessionID("sess-syn-1")
	appendExtractPair(t, w, sid, 1, 0, 1,
		"wade",
		nil,
		map[string]any{"action": "wade"},
		"synonyms",
	)
	_ = db.Close()

	out, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		string(sid), "1",
	)
	if execErr != nil {
		t.Fatalf("suggest-synonym: %v\n%s", execErr, out)
	}
	if !strings.Contains(out, "not llm") {
		t.Errorf("expected 'not llm' notice for synonyms-resolved call; got:\n%s", out)
	}
	if strings.Contains(out, "YAML snippet") {
		t.Errorf("should not print YAML snippet for deterministic-resolved call; got:\n%s", out)
	}
}

// TestSuggestSynonym_NoExtractCalls prints a notice when no extract calls exist.
func TestSuggestSynonym_NoExtractCalls(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db := openTestDBForExtract(t, dbPath)
	_ = db.Close()

	out, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		"nonexistent-session", "1",
	)
	if execErr != nil {
		t.Fatalf("suggest-synonym: %v\n%s", execErr, out)
	}
	if !strings.Contains(out, "No host.agent.extract calls found") {
		t.Errorf("expected no-calls notice; got:\n%s", out)
	}
}

// TestSuggestSynonym_CallID_TurnSeq verifies the "turn:seq" call-id format.
func TestSuggestSynonym_CallID_TurnSeq(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db := openTestDBForExtract(t, dbPath)
	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}

	sid := app.SessionID("sess-ts-1")
	appendExtractPair(t, w, sid, 5, 2, 3,
		"head north",
		nil,
		map[string]any{"direction": "north"},
		"llm",
	)
	_ = db.Close()

	out, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		string(sid), "5:2",
	)
	if execErr != nil {
		t.Fatalf("suggest-synonym: %v\n%s", execErr, out)
	}
	if !strings.Contains(out, `"head north"`) {
		t.Errorf("expected quoted input phrase in output; got:\n%s", out)
	}
}

// TestSuggestSynonym_CallID_Index verifies the 1-based index call-id format.
// Turns 10 and 20 are used so that "2" as a turn number finds nothing and falls
// through to the 1-based index path (returning the second call, on turn 20).
func TestSuggestSynonym_CallID_Index(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db := openTestDBForExtract(t, dbPath)
	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}

	sid := app.SessionID("sess-idx-1")
	// Two calls on turns 10 and 20 so that "2" doesn't match any turn number
	// and falls through to 1-based index lookup.
	appendExtractPair(t, w, sid, 10, 0, 1, "go north", nil, map[string]any{"direction": "north"}, "llm")
	appendExtractPair(t, w, sid, 20, 0, 1, "wade", nil, map[string]any{"action": "wade"}, "llm")
	_ = db.Close()

	// Index 2 → second call (turn 20, input "wade").
	out, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		string(sid), "2",
	)
	if execErr != nil {
		t.Fatalf("suggest-synonym: %v\n%s", execErr, out)
	}
	if !strings.Contains(out, `"wade"`) {
		t.Errorf("expected second-call input 'wade'; got:\n%s", out)
	}
}

// TestSuggestSynonym_CallID_MultipleSameTurn_Error verifies that specifying a
// plain turn number with multiple calls on that turn returns an error.
func TestSuggestSynonym_CallID_MultipleSameTurn_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db := openTestDBForExtract(t, dbPath)
	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}

	sid := app.SessionID("sess-multi-1")
	// Two extract calls on the same turn (seq 0→1, seq 2→3).
	appendExtractPair(t, w, sid, 3, 0, 1, "go north", nil, map[string]any{"direction": "north"}, "llm")
	appendExtractPair(t, w, sid, 3, 2, 3, "go south", nil, map[string]any{"direction": "south"}, "llm")
	_ = db.Close()

	out, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		string(sid), "3",
	)
	// Should return an error (multiple calls on same turn).
	if execErr == nil {
		t.Errorf("expected error for ambiguous turn 3; got nil\noutput:\n%s", out)
	}
	if !strings.Contains(out+execErr.Error(), "multiple") {
		t.Errorf("expected 'multiple' in error; got:\n%s\n%v", out, execErr)
	}
}

// TestSuggestSynonym_NoSynonymsFileInResolvers verifies the no-synonyms-file path.
func TestSuggestSynonym_NoSynonymsFileInResolvers(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db := openTestDBForExtract(t, dbPath)
	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}

	sid := app.SessionID("sess-nosyn-1")
	// Resolver chain has only an llm entry — no synonyms file.
	appendExtractPair(t, w, sid, 1, 0, 1,
		"go north",
		[]any{map[string]any{"llm": map[string]any{"prompt": "/prompts/p.md"}}},
		map[string]any{"direction": "north"},
		"llm",
	)
	_ = db.Close()

	out, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		string(sid), "1",
	)
	if execErr != nil {
		t.Fatalf("suggest-synonym: %v\n%s", execErr, out)
	}
	// Should still show the YAML snippet (no synonyms file → "your synonyms YAML file").
	if !strings.Contains(out, "YAML snippet") {
		t.Errorf("expected YAML snippet even without synonyms file; got:\n%s", out)
	}
	if !strings.Contains(out, "No synonyms file found") {
		t.Errorf("expected no-synonyms-file notice; got:\n%s", out)
	}
}

// TestSuggestSynonym_UnknownCallID_Error verifies an error for an unrecognised
// call-id format.
func TestSuggestSynonym_UnknownCallID_Error(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "j.db")

	db := openTestDBForExtract(t, dbPath)
	w, err := journal.NewSQLiteWriter(db)
	if err != nil {
		t.Fatalf("NewSQLiteWriter: %v", err)
	}
	sid := app.SessionID("sess-badid-1")
	appendExtractPair(t, w, sid, 1, 0, 1, "go north", nil, map[string]any{"direction": "north"}, "llm")
	_ = db.Close()

	_, execErr := execRoot(t,
		"extract", "suggest-synonym",
		"--db", dbPath,
		string(sid), "not-a-number",
	)
	if execErr == nil {
		t.Error("expected error for unrecognised call-id format")
	}
}

// TestExtractCmd_Help verifies that `kitsoki extract --help` includes the
// sub-command name and is reachable from the root.
func TestExtractCmd_Help(t *testing.T) {
	t.Parallel()
	out, err := execRoot(t, "extract", "--help")
	if err != nil {
		t.Fatalf("extract --help: %v\n%s", err, out)
	}
	if !strings.Contains(out, "suggest-synonym") {
		t.Errorf("extract --help missing suggest-synonym subcommand:\n%s", out)
	}
}

// TestFindCallByID covers the three call-id formats directly.
func TestFindCallByID_Formats(t *testing.T) {
	t.Parallel()
	calls := []journalExtractCall{
		{Turn: 1, Seq: 0, Input: "go north", ResolvedBy: "llm"},
		{Turn: 3, Seq: 2, Input: "wade", ResolvedBy: "synonyms"},
		{Turn: 5, Seq: 0, Input: "go south", ResolvedBy: "llm"},
	}

	cases := []struct {
		id    string
		input string
	}{
		{"1:0", "go north"}, // turn:seq
		{"3:2", "wade"},     // turn:seq with non-zero seq
		{"5", "go south"},   // plain turn number (only one call on turn 5)
		{"1", "go north"},   // plain turn number
		{"2", "wade"},       // 1-based index (second call)
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.id, func(t *testing.T) {
			t.Parallel()
			got, err := findCallByID(calls, tc.id)
			if err != nil {
				t.Fatalf("findCallByID(%q): %v", tc.id, err)
			}
			if got.Input != tc.input {
				t.Errorf("input: want %q, got %q", tc.input, got.Input)
			}
		})
	}
}

// TestFindCallByID_Errors covers error paths.
func TestFindCallByID_Errors(t *testing.T) {
	t.Parallel()
	calls := []journalExtractCall{
		{Turn: 1, Seq: 0, Input: "go north"},
		{Turn: 1, Seq: 2, Input: "wade"},
	}

	// Multiple calls on same turn → error.
	_, err := findCallByID(calls, "1")
	if err == nil {
		t.Error("expected error for ambiguous turn 1")
	}

	// Non-existent turn:seq.
	_, err = findCallByID(calls, "9:0")
	if err == nil {
		t.Error("expected error for non-existent 9:0")
	}

	// Out-of-range index.
	_, err = findCallByID(calls, "9")
	if err == nil {
		t.Error("expected error for out-of-range index 9")
	}
}

// TestFindSynonymsFileFromResolvers covers resolver chain parsing.
func TestFindSynonymsFileFromResolvers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		resolvers []any
		want      string
	}{
		{
			name:      "nil",
			resolvers: nil,
			want:      "",
		},
		{
			name: "llm_only",
			resolvers: []any{
				map[string]any{"llm": map[string]any{"prompt": "/p.md"}},
			},
			want: "",
		},
		{
			name: "synonyms_first",
			resolvers: []any{
				map[string]any{"synonyms": "/app/synonyms.yaml"},
				map[string]any{"llm": map[string]any{"prompt": "/p.md"}},
			},
			want: "/app/synonyms.yaml",
		},
		{
			name: "llm_then_synonyms",
			resolvers: []any{
				map[string]any{"llm": map[string]any{"prompt": "/p.md"}},
				map[string]any{"synonyms": "/app/s.yaml"},
			},
			want: "/app/s.yaml",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := findSynonymsFileFromResolvers(tc.resolvers)
			if got != tc.want {
				t.Errorf("findSynonymsFileFromResolvers: want %q, got %q", tc.want, got)
			}
		})
	}
}

// openTestDBForExtract is a helper that opens a file-backed SQLite DB and
// applies the journal DDL. It registers cleanup on t.
func openTestDBForExtract(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open %q: %v", path, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS journal (
    session_id TEXT NOT NULL, turn INTEGER NOT NULL, seq INTEGER NOT NULL,
    ts INTEGER NOT NULL, kind TEXT NOT NULL, doc TEXT, doc_version INTEGER,
    body_json TEXT NOT NULL,
    PRIMARY KEY (session_id, turn, seq)
) STRICT;
CREATE INDEX IF NOT EXISTS journal_doc_idx ON journal (session_id, doc, doc_version);
`); err != nil {
		t.Fatalf("DDL: %v", err)
	}
	return db
}
