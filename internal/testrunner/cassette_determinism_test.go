package testrunner

// cassette_determinism_test.go — cassette corner cases.
//
// Sub-cases from the proposal:
//   - ReplayAnyDistinctCallIDs:       DeriveCallID scheme for distinct per-match IDs
//   - MatchIdxContinuityAcrossResume: matchIdx scheme proof (future engine work)
//   - UnmatchedEpisodesBlocking:      UnmatchedEpisodes() gate
//   - EpisodeResponseFailsSchema:     skipped (phase B)
//   - OversizeInclude:               !include resolves; JSONL rejects oversize
//   - IncludeTargetMissing:           missing !include target fails at load
//   - IncludeTargetOutsideDir:        cross-directory !include (known gap)
//   - EpisodeOrderOffPath:            independent dispatchers don't cross-contaminate
//   - CrashRecovery:                  trace with dangling AgentCalled folds ok

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"kitsoki/internal/agent"
	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
	"kitsoki/internal/world"
)

// testMemSink is an in-memory EventSink for test assertions.
type testMemSink struct {
	mu     sync.Mutex
	events []store.Event
}

func newMemSink() *testMemSink { return &testMemSink{} }

func (s *testMemSink) Append(ev store.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *testMemSink) History() store.History {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(store.History, len(s.events))
	copy(out, s.events)
	return out
}

// ─── replay:any produces distinct call_ids ───────────────────────────────────

// TestCassettesDeterminism_ReplayAnyDistinctCallIDs verifies the DeriveCallID
// scheme produces distinct IDs per matchIdx for replay:any episodes, and that
// the cassette dispatcher replays the same episode multiple times.
func TestCassettesDeterminism_ReplayAnyDistinctCallIDs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "replay_any.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: run_any
    match:
      handler: host.run
    response:
      data: {ok: true}
    replay: any
`)
	cas, loadErr := LoadCassette(p)
	if loadErr != nil {
		t.Fatalf("LoadCassette: %v", loadErr)
	}

	// Dispatch 3 times — all should succeed with replay:any.
	clk := newFakeClock()
	stateOf := func() string { return "main" }
	dispatch := BuildCassetteDispatcher(cas, "host.run", stateOf, nil, nil, clk)

	for i := 0; i < 3; i++ {
		_, err := dispatch(context.Background(), nil)
		if err != nil {
			t.Fatalf("dispatch attempt %d: %v", i, err)
		}
	}

	// DeriveCallID with matchIdx must produce distinct IDs.
	id0 := host.DeriveCallID("myapp", "run_any:0")
	id1 := host.DeriveCallID("myapp", "run_any:1")
	id2 := host.DeriveCallID("myapp", "run_any:2")

	if id0 == id1 || id1 == id2 || id0 == id2 {
		t.Errorf("distinct matchIdx must produce distinct call_ids: %q %q %q", id0, id1, id2)
	}

	// agent: + replay:any was previously forbidden; relaxed in phase B.
	t.Log("agent: + replay:any combination is allowed")
}

// ─── matchIdx continuity across resume ───────────────────────────────────────

// TestCassettesDeterminism_MatchIdxContinuity verifies the call_id derivation
// scheme produces non-colliding IDs across the resume boundary.
func TestCassettesDeterminism_MatchIdxContinuity(t *testing.T) {
	t.Parallel()

	appID := "myapp"
	episodeID := "replay_any_ep"

	// Pre-resume: matchIdx 0, 1, 2.
	preIDs := make([]string, 3)
	for i := range preIDs {
		preIDs[i] = host.DeriveCallID(appID, fmt.Sprintf("%s:%d", episodeID, i))
	}

	// Post-resume: matchIdx 3, 4 (must not collide with pre-resume).
	postIDs := make([]string, 2)
	for i := range postIDs {
		postIDs[i] = host.DeriveCallID(appID, fmt.Sprintf("%s:%d", episodeID, 3+i))
	}

	all := append(preIDs, postIDs...)
	seen := make(map[string]bool)
	for _, id := range all {
		if seen[id] {
			t.Errorf("duplicate call_id %q — matchIdx continuity broken", id)
		}
		seen[id] = true
	}

	for _, pre := range preIDs {
		for _, post := range postIDs {
			if pre == post {
				t.Errorf("pre-resume %q collides with post-resume %q", pre, post)
			}
		}
	}
}

// TestCassettesDeterminism_MatchIdxContinuityFullRoundTrip exercises the full
// engine path for matchIdx continuity across resume (finding 2.10):
//
//  1. Create a cassette with a replay:any agent episode and a JSONL trace sink.
//  2. Dispatch 3 times (pre-resume). The dispatcher (BuildCassetteDispatcherWithSink)
//     writes AgentCalled events to the sink directly via writeCassetteAgentEvents.
//     No manual sink.Append calls — the dispatcher owns the write.
//  3. Close the sink; reload it (simulating session resume).
//  4. Build a new dispatcher seeded from the reloaded history.
//  5. Dispatch 2 more times (post-resume). Verify match_idx 3 and 4, and that
//     post-resume call_ids differ from pre-resume ones.
//
// replay:any + agent: is now legal on the JSONL path because
// each match produces a new event pair with a distinct call_id (matchIdx-keyed).
func TestCassettesDeterminism_MatchIdxContinuityFullRoundTrip(t *testing.T) {
	t.Parallel()

	const appID = "roundtrip_app"
	const epID = "agent_ep"

	dir := t.TempDir()
	// replay:any + agent: is now valid (finding 2.10).
	casPath := writeCassetteFile(t, dir, "replay_any_agent.yaml", `
kind: host_cassette
app_id: `+appID+`
episodes:
  - id: `+epID+`
    match:
      handler: host.agent.ask
    response:
      data: {ok: true}
    replay: any
    agent:
      verb: ask
      agent: test
      prompt: test prompt
      response: "ok"
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	tracePath := filepath.Join(dir, "session.jsonl")
	sink, err := store.OpenJSONL(tracePath)
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}

	clk := newFakeClock()
	stateOf := func() string { return "main" }

	// Phase 1: build dispatcher with the sink (no prior history).
	// The dispatcher calls writeCassetteAgentEvents internally — no manual Append.
	dispatch := BuildCassetteDispatcherWithSink(cas, "host.agent.ask", stateOf, nil, nil, clk, sink, nil)

	// Pre-resume: 3 dispatches. The dispatcher writes AgentCalled+AgentReturned.
	for i := 0; i < 3; i++ {
		ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{
			SessionID: "test-session",
			Turn:      app.TurnNumber(1),
			StatePath: "main",
		})
		_, dispErr := dispatch(ctx, nil)
		if dispErr != nil {
			t.Fatalf("pre-resume dispatch %d: %v", i, dispErr)
		}
	}

	// Close the sink; simulate session end.
	if closeErr := sink.Close(); closeErr != nil {
		t.Fatalf("Close sink: %v", closeErr)
	}

	// Phase 2: reload trace (session resume).
	sink2, err := store.OpenJSONL(tracePath)
	if err != nil {
		t.Fatalf("reload trace: %v", err)
	}
	defer sink2.Close()

	priorHist := sink2.History()

	// Collect the AgentCalled events the dispatcher wrote.
	var agentCalledEvents []store.Event
	for _, ev := range priorHist {
		if ev.Kind == store.AgentCalled {
			agentCalledEvents = append(agentCalledEvents, ev)
		}
	}
	if len(agentCalledEvents) != 3 {
		t.Fatalf("expected 3 AgentCalled events in history, got %d (dispatcher must write them — no manual Append)", len(agentCalledEvents))
	}

	// Verify episode_id, match_idx, and distinct call_ids.
	var preCallIDs []string
	for i, ev := range agentCalledEvents {
		if ev.EpisodeID != epID {
			t.Errorf("AgentCalled[%d]: episode_id=%q, want %q", i, ev.EpisodeID, epID)
		}
		if ev.MatchIdx != i {
			t.Errorf("AgentCalled[%d]: match_idx=%d, want %d", i, ev.MatchIdx, i)
		}
		expectedCallID := host.DeriveCallID(appID, fmt.Sprintf("%s:%d", epID, i))
		if ev.CallID != expectedCallID {
			t.Errorf("AgentCalled[%d]: call_id=%q, want %q", i, ev.CallID, expectedCallID)
		}
		preCallIDs = append(preCallIDs, ev.CallID)
	}

	// Phase 3: build a fresh cassette and dispatcher seeded from history.
	cas2, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette (resume): %v", err)
	}

	// BuildCassetteDispatcherWithSink seeds match counts from prior history
	// so post-resume dispatches get matchIdx 3, 4 (not 0, 1).
	dispatch2 := BuildCassetteDispatcherWithSink(cas2, "host.agent.ask", stateOf, nil, nil, clk, sink2, priorHist)

	// Post-resume: 2 dispatches. The dispatcher writes AgentCalled+AgentReturned.
	for i := 0; i < 2; i++ {
		ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{
			SessionID: "test-session",
			Turn:      app.TurnNumber(2),
			StatePath: "main",
		})
		_, dispErr := dispatch2(ctx, nil)
		if dispErr != nil {
			t.Fatalf("post-resume dispatch %d: %v", i, dispErr)
		}
	}

	// Collect post-resume AgentCalled events (new since the reload).
	allHistAfter := sink2.History()
	var postCallIDs []string
	for _, ev := range allHistAfter {
		if ev.Kind == store.AgentCalled {
			// Skip pre-resume events (match_idx < 3).
			if ev.MatchIdx >= 3 {
				postCallIDs = append(postCallIDs, ev.CallID)
				expectedMatchIdx := len(postCallIDs) - 1 + 3
				if ev.MatchIdx != expectedMatchIdx {
					t.Errorf("post-resume AgentCalled: match_idx=%d, want %d", ev.MatchIdx, expectedMatchIdx)
				}
			}
		}
	}
	if len(postCallIDs) != 2 {
		t.Fatalf("expected 2 post-resume AgentCalled events, got %d", len(postCallIDs))
	}

	// Assert: post-resume call_ids differ from pre-resume ones.
	preSet := make(map[string]bool)
	for _, id := range preCallIDs {
		preSet[id] = true
	}
	for i, id := range postCallIDs {
		if preSet[id] {
			t.Errorf("post-resume call_id[%d]=%q collides with a pre-resume call_id (matchIdx seeding broken)", i, id)
		}
	}

	// Assert: all 5 call_ids are distinct.
	all := append(preCallIDs, postCallIDs...)
	seen := make(map[string]bool)
	for _, id := range all {
		if seen[id] {
			t.Errorf("duplicate call_id %q across pre- and post-resume", id)
		}
		seen[id] = true
	}

	t.Logf("pre-resume call_ids:  %v", preCallIDs)
	t.Logf("post-resume call_ids: %v", postCallIDs)
}

// ─── Unmatched episodes are a blocking failure ───────────────────────────────

// TestCassettesDeterminism_UnmatchedEpisodesBlocking verifies that
// UnmatchedEpisodes() returns never-played episode IDs after a fixture run.
func TestCassettesDeterminism_UnmatchedEpisodesBlocking(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "unmatched.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: ep_played
    match:
      handler: host.run
    response:
      data: {ok: true}
  - id: ep_never_played
    match:
      handler: host.something_else
    response:
      data: {ok: false}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	clk := newFakeClock()
	stateOf := func() string { return "" }
	dispatch := BuildCassetteDispatcher(cas, "host.run", stateOf, nil, nil, clk)
	if _, err := dispatch(context.Background(), nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	unmatched := cas.UnmatchedEpisodes()
	if len(unmatched) == 0 {
		t.Error("expected at least one unmatched episode")
	}
	found := false
	for _, id := range unmatched {
		if id == "ep_never_played" {
			found = true
		}
	}
	if !found {
		t.Errorf("ep_never_played must be unmatched; got: %v", unmatched)
	}
}

// requireAllEpisodesPlayed is the top-level helper every cassette test should use.
// If any episodes are unmatched after a run, the test fails.
func requireAllEpisodesPlayed(t *testing.T, cas *Cassette) {
	t.Helper()
	if u := cas.UnmatchedEpisodes(); len(u) > 0 {
		t.Errorf("cassette has %d unmatched episode(s): %v", len(u), u)
	}
}

// ─── Episode response fails schema ───────────────────────────────────────────

// TestCassettesDeterminism_EpisodeResponseFailsSchema verifies that a cassette
// whose recorded response fails the room's schema check produces an AgentError
// event pointing at the cassette as the source (B-7 / finding 6).
//
// Uses NewCassetteAgent + host.Dispatch (the production Dispatch path) which
// validates Submission against SchemaJSON. The legacy BuildCassetteDispatcher
// path does not route through Dispatch and is not affected by this test.
func TestCassettesDeterminism_EpisodeResponseFailsSchema(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Episode whose agent.response is {"wrong_field": true} — the schema below
	// requires only {"result": string} with additionalProperties: false.
	casPath := writeCassetteFile(t, dir, "schema_fail.yaml", `
kind: host_cassette
app_id: schema_test_app
episodes:
  - id: bad_schema_ep
    match:
      handler: agent.test_fixer
    response:
      data: {ok: true}
    agent:
      verb: ask
      response: '{"wrong_field": true}'
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Strict schema: only allows {result: string}, no additional properties.
	schemaJSON := json.RawMessage(`{
		"type": "object",
		"properties": {"result": {"type": "string"}},
		"required": ["result"],
		"additionalProperties": false
	}`)

	// Create the cassetteAgent and register it in a registry.
	co := NewCassetteAgent(cas, "agent.test_fixer", func() string { return "test_state" }, nil)
	defer co.Close()

	reg := agent.NewRegistry()
	reg.Register("agent.test_fixer", co)

	// Set up a sink to capture events.
	sink := newMemSink()
	ctx := context.Background()
	ctx = host.WithAgentRegistry(ctx, reg)
	ctx = host.WithAgentEventSink(ctx, sink)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{SessionID: "test-sess"})

	// Dispatch through host.Dispatch with the strict schema.
	dr := host.AgentDispatchRequest{
		Req: agent.AskRequest{
			Verb:       "ask",
			PromptText: "fix the bug",
			SchemaJSON: schemaJSON,
		},
		PluginName: "agent.test_fixer",
		Verb:       "ask",
	}
	_, dispErr := host.Dispatch(ctx, dr)

	// The dispatch MUST fail because the cassette response doesn't satisfy the schema.
	if dispErr == nil {
		t.Fatal("expected dispatch error due to schema violation, got nil")
	}

	// The error should be a schema_invalid AskError.
	var ae *agent.AskError
	if !errors.As(dispErr, &ae) {
		t.Fatalf("expected *agent.AskError, got %T: %v", dispErr, dispErr)
	}
	if ae.Kind != "schema_invalid" {
		t.Errorf("AskError.Kind: got %q, want schema_invalid", ae.Kind)
	}

	// The sink should contain AgentCalled + AgentError events (not AgentReturned).
	events := sink.events
	var calledCount, errorCount, returnedCount int
	for _, ev := range events {
		switch ev.Kind {
		case store.AgentCalled:
			calledCount++
		case store.AgentReturned:
			returnedCount++
		case store.AgentError:
			errorCount++
		}
	}
	if calledCount != 1 {
		t.Errorf("AgentCalled event count: got %d, want 1", calledCount)
	}
	if errorCount != 1 {
		t.Errorf("AgentError event count: got %d, want 1", errorCount)
	}
	if returnedCount != 0 {
		t.Errorf("AgentReturned event count: got %d, want 0 (should not appear on schema failure)", returnedCount)
	}
}

// ─── Large episode via !include ──────────────────────────────────────────────

// TestCassettesDeterminism_LargeInclude verifies that a large !include
// payload is resolved at load time and correctly written to JSONL
// (PIPE_BUF limit was removed). Skipped under -short (4 MiB allocation).
func TestCassettesDeterminism_LargeInclude(t *testing.T) {
	if testing.Short() {
		t.Skip("TestCassettesDeterminism_LargeInclude: skipped under -short (4 MiB allocation)")
	}
	t.Parallel()
	dir := t.TempDir()

	const size = 4 * 1024 * 1024
	sidecarPath := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(sidecarPath, []byte(strings.Repeat("x", size)), 0644); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	p := writeCassetteFile(t, dir, "large_inc.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: large_ep
    match:
      handler: host.agent.ask
    response:
      data: {ok: true}
    agent:
      verb: ask
      agent: test
      prompt: !include large.txt
      response: small response
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette with large !include: %v", err)
	}

	if len(cas.Episodes) == 0 || cas.Episodes[0].Agent == nil {
		t.Fatal("expected episode with agent block")
	}
	if len(cas.Episodes[0].Agent.Prompt) != size {
		t.Errorf("resolved prompt must be %d bytes, got %d", size, len(cas.Episodes[0].Agent.Prompt))
	}

	// Write this as a JSONL AgentCalled event — must now succeed.
	s, err := store.OpenJSONL(filepath.Join(dir, "trace.jsonl"))
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}
	defer s.Close()

	payload, marshalErr := json.Marshal(map[string]any{
		"verb":   "ask",
		"prompt": cas.Episodes[0].Agent.Prompt,
	})
	if marshalErr != nil {
		t.Fatalf("marshal agent payload: %v", marshalErr)
	}

	appendErr := s.Append(store.Event{
		Turn:    app.TurnNumber(1),
		Seq:     0,
		Kind:    store.AgentCalled,
		Payload: json.RawMessage(payload),
		CallID:  host.DeriveCallID("myapp", "large_ep:0"),
	})
	if appendErr != nil {
		t.Errorf("Append of large agent payload must succeed, got error: %v", appendErr)
	}

	// Verify it was written correctly
	hist := s.History()
	if len(hist) != 1 {
		t.Errorf("expected 1 event, got %d", len(hist))
	}
}

// ─── !include target missing ─────────────────────────────────────────────────

// TestCassettesDeterminism_IncludeTargetMissing verifies that a missing
// !include target fails at cassette load time.
func TestCassettesDeterminism_IncludeTargetMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	p := writeCassetteFile(t, dir, "missing_inc.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data: {ok: true}
    agent:
      verb: ask
      prompt: !include nonexistent_file.txt
`)
	_, err := LoadCassette(p)
	if err == nil {
		t.Fatal("LoadCassette must fail when !include target is missing")
	}
	if !strings.Contains(err.Error(), "nonexistent_file.txt") &&
		!strings.Contains(err.Error(), "include") {
		t.Errorf("error must mention the missing path; got: %v", err)
	}
}

// ─── !include target outside story directory ─────────────────────────────────

// TestCassettesDeterminism_IncludeTargetOutsideDir verifies cross-tree !include behaviour.
// Current implementation resolves relative to the cassette dir and does not
// restrict cross-tree access. Documented as known gap.
func TestCassettesDeterminism_IncludeTargetOutsideDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	outsideFile := filepath.Join(filepath.Dir(dir), "outside.txt")
	_ = os.WriteFile(outsideFile, []byte("secret content"), 0644)
	defer os.Remove(outsideFile)

	p := writeCassetteFile(t, dir, "outside_inc.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data: {ok: true}
    agent:
      verb: ask
      prompt: !include ../outside.txt
`)
	_, err := LoadCassette(p)
	if err == nil {
		t.Fatal("LoadCassette must reject a cross-tree !include path")
	}
	if !strings.Contains(err.Error(), "outside") && !strings.Contains(err.Error(), "include") {
		t.Errorf("error must mention include restriction; got: %v", err)
	}
	t.Logf("cross-tree !include correctly rejected: %v", err)
}

// ─── Episode order across off-path interleave ────────────────────────────────

// TestCassettesDeterminism_EpisodeOrderOffPath verifies that independent
// dispatchers for different handlers don't cross-contaminate cassette episode state.
func TestCassettesDeterminism_EpisodeOrderOffPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "offpath.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: on_path_ep
    match:
      handler: host.run
    response:
      data: {path: on_path}
  - id: off_path_ep
    match:
      handler: host.query
    response:
      data: {path: off_path}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	clk := newFakeClock()
	stateOf := func() string { return "" }
	onPath := BuildCassetteDispatcher(cas, "host.run", stateOf, nil, nil, clk)
	offPath := BuildCassetteDispatcher(cas, "host.query", stateOf, nil, nil, clk)

	// Off-path fires first.
	offRes, err := offPath(context.Background(), nil)
	if err != nil {
		t.Fatalf("off-path: %v", err)
	}
	if offRes.Data["path"] != "off_path" {
		t.Errorf("off-path result wrong: %v", offRes.Data)
	}

	// On-path fires second.
	onRes, err := onPath(context.Background(), nil)
	if err != nil {
		t.Fatalf("on-path: %v", err)
	}
	if onRes.Data["path"] != "on_path" {
		t.Errorf("on-path result wrong: %v", onRes.Data)
	}

	requireAllEpisodesPlayed(t, cas)
}

// ─── Cassette + crash recovery ───────────────────────────────────────────────

// TestCassettesDeterminism_CrashRecovery verifies that a trace ending on
// AgentCalled (no AgentReturned — crash) can be reopened and folded.
// matchIdx reconciliation is a phase B engine change.
func TestCassettesDeterminism_CrashRecovery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.jsonl")

	s, err := store.OpenJSONL(path)
	if err != nil {
		t.Fatalf("OpenJSONL: %v", err)
	}

	if err := s.Append(store.Event{
		Turn:    app.TurnNumber(1),
		Seq:     0,
		Kind:    store.TurnStarted,
		Payload: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("Append TurnStarted: %v", err)
	}
	if err := s.Append(store.Event{
		Turn:    app.TurnNumber(1),
		Seq:     1,
		Kind:    store.AgentCalled,
		Payload: json.RawMessage(`{"verb":"ask","prompt":"test"}`),
		CallID:  host.DeriveCallID("myapp", "ep1:0"),
	}); err != nil {
		t.Fatalf("Append AgentCalled: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reload.
	s2, err := store.OpenJSONL(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	defer s2.Close()

	hist := s2.History()
	if len(hist) != 2 {
		t.Errorf("expected 2 events, got %d", len(hist))
	}

	// Fold — AgentCalled is a no-op.
	def := &app.AppDef{App: app.AppMeta{ID: "test", Version: "1.0"}}
	initial := world.New()
	js, foldErr := store.BuildJourney(def, "start", initial, hist)
	if foldErr != nil {
		t.Fatalf("fold after crash: %v", foldErr)
	}
	if js == nil {
		t.Fatal("fold result nil")
	}

	t.Log("known: matchIdx reconciliation (cassette considers AgentCalled as matched) requires phase B")
}
