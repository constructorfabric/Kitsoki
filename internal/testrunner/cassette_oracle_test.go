package testrunner

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
)

// ─── Phase 1: EpisodeOracle round-trip ───────────────────────────────────────

// TestEpisodeOracle_RoundTrip verifies that a cassette with an oracle: block
// survives LoadCassette → YAML unmarshal with all fields intact.
func TestEpisodeOracle_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "oracle.yaml", `
kind: host_cassette
app_id: testapp
episodes:
  - id: phase_1_oracle
    match:
      handler: host.oracle.task
      phase: phase_1
    response:
      data:
        submitted: {found: true}
    oracle:
      verb: task
      agent: bugfix-reproducer
      model: claude-opus-4-7
      duration_ms: 18432
      prompt_tokens: 1200
      response_tokens: 300
      cost_usd: 0.05
      system_prompt: "You are a helpful assistant."
      prompt: "Reproduce the bug."
      response: "I found the bug."
      call_id: aabbccdd11223344
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	if len(cas.Episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(cas.Episodes))
	}

	ep := cas.Episodes[0]
	if ep.Oracle == nil {
		t.Fatal("episode.Oracle is nil, expected non-nil")
	}

	o := ep.Oracle
	if o.Verb != "task" {
		t.Errorf("Verb: got %q want %q", o.Verb, "task")
	}
	if o.Agent != "bugfix-reproducer" {
		t.Errorf("Agent: got %q want %q", o.Agent, "bugfix-reproducer")
	}
	if o.Model != "claude-opus-4-7" {
		t.Errorf("Model: got %q want %q", o.Model, "claude-opus-4-7")
	}
	if o.DurationMs != 18432 {
		t.Errorf("DurationMs: got %d want 18432", o.DurationMs)
	}
	if o.PromptTokens != 1200 {
		t.Errorf("PromptTokens: got %d want 1200", o.PromptTokens)
	}
	if o.ResponseTokens != 300 {
		t.Errorf("ResponseTokens: got %d want 300", o.ResponseTokens)
	}
	if o.SystemPrompt != "You are a helpful assistant." {
		t.Errorf("SystemPrompt: got %q", o.SystemPrompt)
	}
	if o.Prompt != "Reproduce the bug." {
		t.Errorf("Prompt: got %q", o.Prompt)
	}
	if o.Response != "I found the bug." {
		t.Errorf("Response: got %q", o.Response)
	}
}

// TestEpisodeOracle_IncludeTxt verifies that !include works on .txt sidecar
// files for oracle string fields (system_prompt, prompt, response).
func TestEpisodeOracle_IncludeTxt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Write sidecar text files.
	writeOracleFile(t, dir, "system.txt", "You are a test assistant.")
	writeOracleFile(t, dir, "prompt.txt", "Fix the bug please.")
	writeOracleFile(t, dir, "response.txt", "Bug is fixed.")

	p := writeCassetteFile(t, dir, "oracle_inc.yaml", `
kind: host_cassette
app_id: incapp
episodes:
  - id: inc_oracle
    match:
      handler: host.oracle.ask
    response:
      data: {ok: true}
    oracle:
      verb: ask
      agent: fixer
      system_prompt: !include system.txt
      prompt: !include prompt.txt
      response: !include response.txt
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette with !include txt: %v", err)
	}
	if len(cas.Episodes) == 0 {
		t.Fatal("no episodes")
	}
	o := cas.Episodes[0].Oracle
	if o == nil {
		t.Fatal("oracle block is nil")
	}
	if o.SystemPrompt != "You are a test assistant." {
		t.Errorf("SystemPrompt via !include: got %q", o.SystemPrompt)
	}
	if o.Prompt != "Fix the bug please." {
		t.Errorf("Prompt via !include: got %q", o.Prompt)
	}
	if o.Response != "Bug is fixed." {
		t.Errorf("Response via !include: got %q", o.Response)
	}
}

// TestEpisodeOracle_IncludeJson verifies that !include works on .json sidecar
// files for the oracle.input field (json.RawMessage).
func TestEpisodeOracle_IncludeJson(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "input.json"), []byte(`{"task_id":"abc"}`), 0644); err != nil {
		t.Fatalf("write json: %v", err)
	}

	p := writeCassetteFile(t, dir, "oracle_json.yaml", `
kind: host_cassette
app_id: jsonapp
episodes:
  - id: json_oracle
    match:
      handler: host.oracle.extract
    response:
      data: {done: true}
    oracle:
      verb: extract
      input: !include input.json
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	o := cas.Episodes[0].Oracle
	if o == nil {
		t.Fatal("oracle nil")
	}
	// Input is typed as any; after !include of a .json file it should be a map.
	m, ok := o.Input.(map[string]any)
	if !ok {
		t.Fatalf("oracle.input should be map[string]any, got %T: %v", o.Input, o.Input)
	}
	if m["task_id"] != "abc" {
		t.Errorf("input.task_id: got %v", m["task_id"])
	}
}

// TestEpisodeOracle_ReplayAnyForbidden verifies that replay:any + oracle: is
// rejected at load time per §6.3.
func TestEpisodeOracle_ReplayAnyForbidden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "bad_oracle.yaml", `
kind: host_cassette
app_id: testapp
episodes:
  - id: bad
    match:
      handler: host.oracle.ask
    replay: any
    response:
      data: {ok: true}
    oracle:
      verb: ask
`)
	_, err := LoadCassette(p)
	if err == nil {
		t.Fatal("expected error for replay:any + oracle:, got nil")
	}
	if !strings.Contains(err.Error(), "replay:any") {
		t.Errorf("error should mention replay:any, got: %v", err)
	}
}

// TestEpisodeOracle_DerivedCallID verifies the deterministic call_id scheme §7.
func TestEpisodeOracle_DerivedCallID(t *testing.T) {
	t.Parallel()

	id1 := derivedCallID("bugfix", "phase_1_repro_oracle")
	id2 := derivedCallID("bugfix", "phase_1_repro_oracle")
	if id1 != id2 {
		t.Errorf("derivedCallID: not stable: %q vs %q", id1, id2)
	}
	if len(id1) != 16 {
		t.Errorf("derivedCallID: expected 16 hex chars, got %d: %q", len(id1), id1)
	}

	// Different app or episode → different call_id.
	id3 := derivedCallID("other_app", "phase_1_repro_oracle")
	if id1 == id3 {
		t.Errorf("derivedCallID: same id for different app: %q", id1)
	}
	id4 := derivedCallID("bugfix", "phase_2_oracle")
	if id1 == id4 {
		t.Errorf("derivedCallID: same id for different episode: %q", id1)
	}
}

// TestEpisodeOracle_ExistingCassetteUnchanged verifies that an existing cassette
// without oracle: blocks loads unchanged (backwards compatibility).
func TestEpisodeOracle_ExistingCassetteUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "legacy.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data:
        result: ok
  - id: ep2
    match:
      handler: host.transport.post
      kind: create
    response:
      data:
        id: "42"
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette (legacy): %v", err)
	}
	if len(cas.Episodes) != 2 {
		t.Fatalf("expected 2 episodes, got %d", len(cas.Episodes))
	}
	for _, ep := range cas.Episodes {
		if ep.Oracle != nil {
			t.Errorf("episode %q: Oracle should be nil for legacy cassette, got %+v", ep.ID, ep.Oracle)
		}
	}
}

// ─── Phase 2: Replay writes KindOracleCall to journal ────────────────────────

// TestEpisodeOracle_ReplayWritesJournal replays a cassette episode with an
// oracle: block and asserts that a KindOracleCall entry lands in the in-memory
// SQLite journal with the expected derived call_id (§7).
func TestEpisodeOracle_ReplayWritesJournal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "replay_oracle.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: phase_1_oracle
    match:
      handler: host.oracle.task
    response:
      data: {submitted: {found: true}}
    oracle:
      verb: task
      agent: bugfix-reproducer
      model: claude-sonnet
      duration_ms: 5000
      system_prompt: "System prompt here."
      prompt: "Prompt here."
      response: "Response here."
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Open an in-memory SQLite store (same as the rig uses) and create a journal writer.
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	jw, err := journal.NewSQLiteWriter(st.DB())
	if err != nil {
		t.Fatalf("create journal writer: %v", err)
	}

	// Inject a session ID + turn into the context so the journal entry gets
	// the right session metadata.
	sid := app.SessionID("test-session-replay-1")
	ctx := host.WithOracleCallCtx(context.Background(), host.OracleCallCtx{
		SessionID: sid,
		Turn:      app.TurnNumber(1),
		StatePath: "phase_1.dispatching",
	})

	// Build dispatcher with journal writer.
	clk := newFakeClock()
	stateOf := func() string { return "phase_1.dispatching" }
	dispatch := BuildCassetteDispatcherWithJournal(cas, "host.oracle.task", stateOf, nil, nil, clk, jw, nil)

	res, err := dispatch(ctx, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res.Data["submitted"] == nil {
		t.Errorf("expected submitted in result data, got %v", res.Data)
	}

	// Assert a KindOracleCall entry landed in the SQLite journal keyed by call_id.
	expectedCallID := derivedCallID("myapp", "phase_1_oracle")
	rows, err := loadOracleCallRows(st.DB(), string(sid))
	if err != nil {
		t.Fatalf("load oracle call rows: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no KindOracleCall entry found in journal")
	}

	// Parse body and check call_id matches the derived value.
	var body host.OracleCallBody
	if err := json.Unmarshal([]byte(rows[0]), &body); err != nil {
		t.Fatalf("unmarshal oracle call body: %v", err)
	}
	if body.CallID != expectedCallID {
		t.Errorf("call_id: got %q want %q", body.CallID, expectedCallID)
	}
	if body.Verb != "task" {
		t.Errorf("verb: got %q want task", body.Verb)
	}
	if body.Agent != "bugfix-reproducer" {
		t.Errorf("agent: got %q want bugfix-reproducer", body.Agent)
	}
	if body.DurationMS != 5000 {
		t.Errorf("duration_ms: got %d want 5000", body.DurationMS)
	}
}

// TestEpisodeOracle_NoOracleNoJournalEntry verifies that episodes without
// an oracle: block do not write any KindOracleCall entry.
func TestEpisodeOracle_NoOracleNoJournalEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "plain.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: plain_ep
    match:
      handler: host.run
    response:
      data: {ok: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	st, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	jw, err := journal.NewSQLiteWriter(st.DB())
	if err != nil {
		t.Fatalf("create journal writer: %v", err)
	}

	clk := newFakeClock()
	stateOf := func() string { return "" }
	dispatch := BuildCassetteDispatcherWithJournal(cas, "host.run", stateOf, nil, nil, clk, jw, nil)

	_, err = dispatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	sid := app.SessionID("empty-session")
	rows, err := loadOracleCallRows(st.DB(), string(sid))
	if err != nil {
		t.Fatalf("load oracle call rows: %v", err)
	}
	if len(rows) > 0 {
		t.Errorf("expected no KindOracleCall entries for non-oracle episode, got %d", len(rows))
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────────

// writeOracleFile writes content to dir/name. Helper for text sidecar files.
func writeOracleFile(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write %q: %v", p, err)
	}
}

// loadOracleCallRows reads all KindOracleCall body strings for a session from
// the SQLite journal. Returns the raw body JSON strings.
func loadOracleCallRows(db *sql.DB, sessionID string) ([]string, error) {
	rows, err := db.Query(
		`SELECT body_json FROM journal WHERE kind = 'oracle.call' AND session_id = ? ORDER BY ROWID`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("loadOracleCallRows: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// invokeDispatcherWithJournal is like invokeDispatcher but threads in a
// journal writer and lookup function (for oracle replay/record tests).
func invokeDispatcherWithJournal(
	t *testing.T,
	cas *Cassette,
	handler string,
	args map[string]any,
	statePath string,
	fallback host.Handler,
	recordSink func(*CassetteEpisode),
	jw journal.Writer,
	journalLookup OracleJournalLookup,
) (host.Result, error) {
	t.Helper()
	clk := newFakeClock()
	stateOf := func() string { return statePath }
	dispatch := BuildCassetteDispatcherWithJournal(cas, handler, stateOf, fallback, recordSink, clk, jw, journalLookup)
	ctx := host.WithOracleCallCtx(context.Background(), host.OracleCallCtx{
		SessionID: app.SessionID(fmt.Sprintf("sid-%d", time.Now().UnixNano())),
		Turn:      1,
		StatePath: app.StatePath(statePath),
	})
	return dispatch(ctx, args)
}
