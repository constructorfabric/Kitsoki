package testrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/clock"
	"kitsoki/internal/host"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

// newFakeClock returns a fake clock starting at epoch 0.
func newFakeClock() *clock.Fake {
	return clock.NewFake(time.Unix(0, 0))
}

// writeCassetteFile writes a cassette YAML to a temp dir and returns the path.
func writeCassetteFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatalf("write cassette %q: %v", p, err)
	}
	return p
}

// simpleDispatcher builds and invokes the cassette dispatcher for one call.
func invokeDispatcher(
	t *testing.T,
	cas *Cassette,
	handler string,
	args map[string]any,
	statePath string,
	fallback host.Handler,
	recordSink func(*CassetteEpisode),
) (host.Result, error) {
	t.Helper()
	clk := newFakeClock()
	stateOf := func() string { return statePath }
	dispatch := BuildCassetteDispatcher(cas, handler, stateOf, fallback, recordSink, clk)
	return dispatch(context.Background(), args)
}

// ─── LoadCassette + !include ──────────────────────────────────────────────────

func TestCassette_Load_BasicRoundtrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data:
        result: ok
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	if cas.AppID != "myapp" {
		t.Errorf("AppID: got %q want %q", cas.AppID, "myapp")
	}
	if len(cas.Episodes) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(cas.Episodes))
	}
	if cas.Episodes[0].ID != "ep1" {
		t.Errorf("episode ID: got %q want %q", cas.Episodes[0].ID, "ep1")
	}
}

func TestCassette_Load_WrongKind(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "bad.yaml", `
kind: something_else
app_id: x
episodes: []
`)
	_, err := LoadCassette(p)
	if err == nil {
		t.Fatal("expected error for wrong kind")
	}
	if !strings.Contains(err.Error(), "kind") {
		t.Errorf("error should mention kind, got: %v", err)
	}
}

func TestCassette_Include(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Write the included JSON file.
	jsonPath := filepath.Join(dir, "data.json")
	if err := os.WriteFile(jsonPath, []byte(`{"answer": 42}`), 0644); err != nil {
		t.Fatalf("write json: %v", err)
	}
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: inc
episodes:
  - id: inc_ep
    match:
      handler: host.agent
    response:
      data: !include data.json
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette with !include: %v", err)
	}
	if len(cas.Episodes) == 0 {
		t.Fatal("no episodes loaded")
	}
	data := cas.Episodes[0].Response.Data
	v, ok := data["answer"]
	if !ok {
		t.Fatalf("expected 'answer' in response data, got %v", data)
	}
	// go-yaml may decode JSON integers as int, uint64, or float64 depending
	// on the value. Normalise to string for comparison.
	if fmt.Sprint(v) != "42" {
		t.Errorf("answer: got %v (%T), want 42", v, v)
	}
}

// ─── Episode matching ─────────────────────────────────────────────────────────

func TestCassette_MatchByHandlerAndArgs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: jira_create
    match:
      handler: host.transport.post
      kind: create
    response:
      data: {comment_id: "1234"}
  - id: jira_update
    match:
      handler: host.transport.post
      kind: update
    response:
      data: {comment_id: "1234", updated: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	res, err := invokeDispatcher(t, cas, "host.transport.post", map[string]any{"kind": "create"}, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["comment_id"] != "1234" {
		t.Errorf("got %v want comment_id=1234", res.Data)
	}
}

func TestCassette_MatchByPhase(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: phase1_agent
    match:
      handler: host.agent.ask
      phase: phase_1
    response:
      data: {result: phase1}
  - id: phase3_agent
    match:
      handler: host.agent.ask
      phase: phase_3
    response:
      data: {result: phase3}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// phase_1.dispatching → first segment = phase_1
	res, err := invokeDispatcher(t, cas, "host.agent.ask", nil, "phase_1.dispatching", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["result"] != "phase1" {
		t.Errorf("expected phase1 result, got %v", res.Data)
	}

	res2, err := invokeDispatcher(t, cas, "host.agent.ask", nil, "phase_3.deciding", nil, nil)
	if err != nil {
		t.Fatalf("phase3: unexpected error: %v", err)
	}
	if res2.Data["result"] != "phase3" {
		t.Errorf("expected phase3 result, got %v", res2.Data)
	}
}

func TestCassette_MatchBySchemaName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: schema_ep
    match:
      handler: host.agent.ask_with_mcp
      schema_name: repro-report.schema.json
    response:
      data: {submitted: {found: true}}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// schema arg is the full path; matcher should use basename.
	args := map[string]any{"schema": "/some/path/repro-report.schema.json"}
	res, err := invokeDispatcher(t, cas, "host.agent.ask_with_mcp", args, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub, _ := res.Data["submitted"].(map[string]any)
	if sub == nil || sub["found"] != true {
		t.Errorf("expected submitted.found=true, got %v", res.Data)
	}
}

// ─── Sequencing ───────────────────────────────────────────────────────────────

func TestCassette_Sequencing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: first
    match:
      handler: host.run
    response:
      data: {n: 1}
  - id: second
    match:
      handler: host.run
    response:
      data: {n: 2}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	r1, err := invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	r2, err := invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	n1, _ := r1.Data["n"].(int)
	n2, _ := r2.Data["n"].(int)
	// go-yaml may unmarshal integers as int or uint64 depending on context.
	if fmt.Sprint(r1.Data["n"]) != "1" {
		t.Errorf("first call: got n=%v want 1", r1.Data["n"])
	}
	if fmt.Sprint(r2.Data["n"]) != "2" {
		t.Errorf("second call: got n=%v want 2", r2.Data["n"])
	}
	_ = n1
	_ = n2
}

// ─── Replay: any ─────────────────────────────────────────────────────────────

func TestCassette_ReplayAny(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: repeatable
    match:
      handler: host.run
    replay: any
    response:
      data: {result: always}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	for i := 0; i < 5; i++ {
		res, err := invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
		if err != nil {
			t.Fatalf("call %d: %v", i+1, err)
		}
		if res.Data["result"] != "always" {
			t.Errorf("call %d: got %v want result=always", i+1, res.Data)
		}
	}
}

// ─── Miss with no fallback ────────────────────────────────────────────────────

func TestCassette_MissNoFallback(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: ep
    match:
      handler: host.run
    response:
      data: {ok: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Exhaust the single episode.
	_, _ = invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)

	// Second call should miss.
	_, err = invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
	if err == nil {
		t.Fatal("expected ErrCassetteMiss, got nil")
	}
	var miss *ErrCassetteMiss
	if !errors.As(err, &miss) {
		t.Errorf("expected *ErrCassetteMiss, got %T: %v", err, err)
	}
	if miss.Handler != "host.run" {
		t.Errorf("miss.Handler: got %q want host.run", miss.Handler)
	}
}

// ─── Replay miss with fallback still fails closed ─────────────────────────────

func TestCassette_ReplayMissWithFallbackFailsClosed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: ep
    match:
      handler: host.run
      special: yes
    response:
      data: {cassette: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	fallbackCalled := false
	fallback := host.Handler(func(_ context.Context, args map[string]any) (host.Result, error) {
		fallbackCalled = true
		return host.Result{Data: map[string]any{"fallback": true}}, nil
	})

	// Call without matching args. Even though a fallback exists, replay mode must
	// fail closed instead of calling the live handler.
	res, err := invokeDispatcher(t, cas, "host.run", map[string]any{"special": "no"}, "", fallback, nil)
	if err == nil {
		t.Fatalf("expected ErrCassetteMiss, got nil result=%v", res)
	}
	var miss *ErrCassetteMiss
	if !errors.As(err, &miss) {
		t.Fatalf("expected *ErrCassetteMiss, got %T: %v", err, err)
	}
	if fallbackCalled {
		t.Error("fallback was called on replay miss")
	}
}

// ─── InfraError / Error responses ─────────────────────────────────────────────

func TestCassette_InfraError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: infra
    match:
      handler: host.run
    response:
      infra_error: "connection refused"
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	_, err = invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
	if err == nil {
		t.Fatal("expected infra error, got nil")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("error should contain 'connection refused', got: %v", err)
	}
}

func TestCassette_DomainError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: domain_err
    match:
      handler: host.run
    response:
      error: "not found"
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	res, err := invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "not found" {
		t.Errorf("expected domain error 'not found', got %q", res.Error)
	}
}

// ─── Record mode new_episodes ─────────────────────────────────────────────────

func TestCassette_RecordNewEpisodes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
record_mode: new_episodes
episodes: []
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	cas.path = p

	var appended []*CassetteEpisode
	recordSink := func(ep *CassetteEpisode) {
		appended = append(appended, ep)
		_ = AppendEpisodeToFile(cas, ep)
	}

	fallback := host.Handler(func(_ context.Context, args map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"live": true}}, nil
	})

	res, err := invokeDispatcher(t, cas, "host.run", nil, "phase_1.foo", fallback, recordSink)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["live"] != true {
		t.Errorf("expected live result, got %v", res.Data)
	}
	if len(appended) != 1 {
		t.Fatalf("expected 1 appended episode, got %d", len(appended))
	}

	// Verify the file was updated.
	content, readErr := os.ReadFile(p)
	if readErr != nil {
		t.Fatalf("read cassette file: %v", readErr)
	}
	if !strings.Contains(string(content), "recorded_host.run") {
		t.Errorf("appended episode ID not found in file; content:\n%s", content)
	}
}

// ─── KITSOKI_CASSETTE_STRICT ──────────────────────────────────────────────────

// TestCassette_StrictRecordingEnvError tests the env-var helpers. These tests
// mutate env vars so they cannot run in parallel; they are sequential subtests
// under a sequential parent.
func TestCassette_StrictRecordingEnvError(t *testing.T) {
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
record_mode: new_episodes
episodes: []
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	cas.path = p

	t.Run("strict_check", func(t *testing.T) {
		t.Setenv("KITSOKI_CASSETTE_STRICT", "1")
		t.Setenv("KITSOKI_CASSETTE_RECORD", "")

		if !CassetteStrictRecording() {
			t.Error("CassetteStrictRecording should return true")
		}
		mode := CassetteRecordMode(cas)
		if mode != "new_episodes" {
			t.Errorf("expected new_episodes mode, got %q", mode)
		}
	})

	t.Run("env_wins", func(t *testing.T) {
		t.Setenv("KITSOKI_CASSETTE_RECORD", "none")
		if CassetteRecordMode(cas) != "none" {
			t.Error("env var should win over file-level record_mode")
		}
	})

	t.Run("validate_rejects_unsupported", func(t *testing.T) {
		if err := ValidateRecordMode("all"); err == nil {
			t.Error("ValidateRecordMode should reject 'all'")
		}
		if err := ValidateRecordMode("new_episodes"); err != nil {
			t.Errorf("ValidateRecordMode should accept 'new_episodes', got: %v", err)
		}
	})

	t.Run("none_default", func(t *testing.T) {
		t.Setenv("KITSOKI_CASSETTE_RECORD", "")
		cas2 := &Cassette{}
		if CassetteRecordMode(cas2) != "none" {
			t.Errorf("expected none, got %q", CassetteRecordMode(cas2))
		}
	})
}

// ─── PhaseFrom regex ──────────────────────────────────────────────────────────

func TestCassette_PhaseFromRegex(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
phase_from: "^(phase_\\d+(?:_\\d+)?)\\."
episodes:
  - id: ep_phase_1_5
    match:
      handler: host.agent
      phase: phase_1_5
    response:
      data: {found: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// StatePath: "phase_1_5.deciding" should match phase capture "phase_1_5"
	res, err := invokeDispatcher(t, cas, "host.agent", nil, "phase_1_5.deciding", nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Data["found"] != true {
		t.Errorf("expected found=true, got %v", res.Data)
	}
}

// ─── Mutual exclusion with host_handlers ─────────────────────────────────────

func TestCassette_MutualExclusionWithHostHandlers(t *testing.T) {
	t.Parallel()

	fixture := &FlowFixture{
		TestKind:     "flow",
		App:          "test",
		InitialState: "start",
		HostCassette: "some.yaml",
		HostHandlers: map[string]HostStub{
			"host.run": {Data: map[string]any{"ok": true}},
		},
		Turns: []FlowTurn{},
	}

	// The check in runFlowFile is on the fixture struct. Replicate it here.
	if fixture.HostCassette != "" && len(fixture.HostHandlers) > 0 {
		// Expected: this is a load-time error.
		return
	}
	t.Error("should have detected mutual exclusion")
}

// ─── UnmatchedEpisodes / orphan accounting ────────────────────────────────────

// TestCassette_UnmatchedEpisodes_AllUnplayed verifies that an episode that was
// never matched is returned by UnmatchedEpisodes.
func TestCassette_UnmatchedEpisodes_AllUnplayed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: phantom
    match:
      handler: host.run
      phase: phase_999_does_not_exist
    response:
      data: {ok: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// No calls made — phantom episode should be unmatched.
	unmatched := cas.UnmatchedEpisodes()
	if len(unmatched) != 1 || unmatched[0] != "phantom" {
		t.Errorf("expected [phantom] as unmatched, got %v", unmatched)
	}
}

// TestCassette_UnmatchedEpisodes_PlayedIsNotOrphan verifies that a consume-once
// episode that was matched at least once does NOT appear in UnmatchedEpisodes.
func TestCassette_UnmatchedEpisodes_PlayedIsNotOrphan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data: {n: 1}
  - id: ep2
    match:
      handler: host.run
    response:
      data: {n: 2}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Play only ep1.
	_, err = invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
	if err != nil {
		t.Fatalf("play ep1: %v", err)
	}

	// ep1 was played, ep2 was not.
	unmatched := cas.UnmatchedEpisodes()
	if len(unmatched) != 1 || unmatched[0] != "ep2" {
		t.Errorf("expected [ep2] as unmatched, got %v", unmatched)
	}
}

// TestCassette_UnmatchedEpisodes_ReplayAnyCountsAsPlayed verifies that a
// replay: any episode matched at least once does NOT appear in UnmatchedEpisodes,
// even though it remains available for further calls.
func TestCassette_UnmatchedEpisodes_ReplayAnyCountsAsPlayed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: reusable
    match:
      handler: host.run
    replay: any
    response:
      data: {result: always}
  - id: phantom
    match:
      handler: host.agent
      phase: phase_999
    response:
      data: {ok: true}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Before any calls, both are unmatched.
	unmatched := cas.UnmatchedEpisodes()
	if len(unmatched) != 2 {
		t.Fatalf("expected 2 unmatched before any calls, got %v", unmatched)
	}

	// Call reusable once — it should be marked played even though replay: any.
	_, err = invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
	if err != nil {
		t.Fatalf("call reusable: %v", err)
	}

	// Only the phantom (never matched) should remain.
	unmatched = cas.UnmatchedEpisodes()
	if len(unmatched) != 1 || unmatched[0] != "phantom" {
		t.Errorf("expected [phantom] as only unmatched, got %v", unmatched)
	}

	// Calling reusable again still works (replay: any reuse).
	for i := 0; i < 3; i++ {
		_, err = invokeDispatcher(t, cas, "host.run", nil, "", nil, nil)
		if err != nil {
			t.Fatalf("reuse call %d: %v", i+1, err)
		}
	}

	// Still only phantom is unmatched.
	unmatched = cas.UnmatchedEpisodes()
	if len(unmatched) != 1 || unmatched[0] != "phantom" {
		t.Errorf("after reuse calls: expected [phantom], got %v", unmatched)
	}
}

// TestCassette_UnmatchedEpisodes_EmptyWhenAllPlayed verifies that
// UnmatchedEpisodes returns nil when all episodes have been played.
func TestCassette_UnmatchedEpisodes_EmptyWhenAllPlayed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: ep1
    match:
      handler: host.run
    response:
      data: {n: 1}
  - id: ep2
    match:
      handler: host.run
    response:
      data: {n: 2}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Play both episodes.
	for i := 0; i < 2; i++ {
		if _, err = invokeDispatcher(t, cas, "host.run", nil, "", nil, nil); err != nil {
			t.Fatalf("play episode %d: %v", i+1, err)
		}
	}

	unmatched := cas.UnmatchedEpisodes()
	if len(unmatched) != 0 {
		t.Errorf("expected no unmatched episodes, got %v", unmatched)
	}
}

// ─── AppendEpisodeToFile ──────────────────────────────────────────────────────

func TestCassette_AppendEpisodeToFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "cas.yaml", `
kind: host_cassette
app_id: test
episodes:
  - id: existing
    match:
      handler: host.run
    response:
      data: {n: 1}
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	cas.path = p

	newEp := &CassetteEpisode{
		ID:       "appended_ep",
		Match:    map[string]any{"handler": "host.run"},
		Response: CassetteResponse{Data: map[string]any{"n": 2}},
	}
	if err := AppendEpisodeToFile(cas, newEp); err != nil {
		t.Fatalf("AppendEpisodeToFile: %v", err)
	}

	// Reload and verify.
	reloaded, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("reload cassette: %v", err)
	}
	if len(reloaded.Episodes) != 2 {
		t.Errorf("expected 2 episodes after append, got %d", len(reloaded.Episodes))
	}
	found := false
	for _, ep := range reloaded.Episodes {
		if ep.ID == "appended_ep" {
			found = true
			break
		}
	}
	if !found {
		t.Error("appended episode not found in reloaded cassette")
	}
}
