// Package testrunner — cassette Agent transport tests (B-4).
//
// Coverage:
//   - Happy path: agent episode → AskResponse.Submission correct, Meta populated.
//   - Non-agent episode: response.data marshalled as Submission.
//   - Episode miss: returns *agent.AskError{Kind: "transport_error"}.
//   - Episode error: agentBlock.Error → *agent.AskError{Kind: "plugin_crash"}.
//   - InfraError: resp.InfraError → *agent.AskError{Kind: "transport_error"}.
//   - replay:any + agent: is legal and produces N Ask calls with distinct matchIdx.
//   - matchIdx advances per Ask call; Meta["episode_id"] + Meta["match_idx"] set.
//   - Call_id in Meta is deterministic and consistent with DeriveCallID scheme.
//   - replay:any + agent: no longer rejected at load time.
//   - Close is a no-op.
package testrunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"kitsoki/internal/agent"
	"kitsoki/internal/host"
)

// ─── Happy path ───────────────────────────────────────────────────────────────

// TestCassetteAgent_HappyPath verifies that NewCassetteAgent.Ask returns the
// agent episode response as Submission with all expected Meta fields.
func TestCassetteAgent_HappyPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const appID = "testapp"
	const epID = "agent_ep"
	casPath := writeCassetteFile(t, dir, "agent.yaml", `
kind: host_cassette
app_id: `+appID+`
episodes:
  - id: `+epID+`
    match:
      handler: agent.fixer
    response:
      data: {ok: true}
    agent:
      verb: ask
      agent: fixer-agent
      model: claude-haiku
      duration_ms: 1234
      prompt: test prompt
      response: "{\"result\": \"fixed\"}"
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	clk := newFakeClock()
	o := NewCassetteAgent(cas, "agent.fixer", func() string { return "main" }, clk)
	defer o.Close()

	req := agent.AskRequest{Verb: "ask"}
	resp, err := o.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// Verify Submission is the agent response.
	if resp.Submission == nil {
		t.Fatal("Submission is nil")
	}
	var sub map[string]any
	if err := json.Unmarshal(resp.Submission, &sub); err != nil {
		t.Fatalf("unmarshal Submission: %v", err)
	}
	if sub["result"] != "fixed" {
		t.Errorf("Submission.result: got %v, want fixed", sub["result"])
	}

	// Verify Meta fields.
	if resp.Meta == nil {
		t.Fatal("Meta is nil")
	}
	if resp.Meta["transport"] != "cassette" {
		t.Errorf("Meta.transport: got %v, want cassette", resp.Meta["transport"])
	}
	if resp.Meta["episode_id"] != epID {
		t.Errorf("Meta.episode_id: got %v, want %q", resp.Meta["episode_id"], epID)
	}
	if resp.Meta["match_idx"] != 0 {
		t.Errorf("Meta.match_idx: got %v, want 0", resp.Meta["match_idx"])
	}
	if resp.Meta["model"] != "claude-haiku" {
		t.Errorf("Meta.model: got %v, want claude-haiku", resp.Meta["model"])
	}
	if resp.Meta["agent"] != "fixer-agent" {
		t.Errorf("Meta.agent: got %v, want fixer-agent", resp.Meta["agent"])
	}
	if fmt.Sprintf("%v", resp.Meta["duration_ms"]) != "1234" {
		t.Errorf("Meta.duration_ms: got %v, want 1234", resp.Meta["duration_ms"])
	}

	// Verify call_id is the deterministic DeriveCallID value.
	expectedCallID := host.DeriveCallID(appID, fmt.Sprintf("%s:%d", epID, 0))
	if resp.Meta["call_id"] != expectedCallID {
		t.Errorf("Meta.call_id: got %v, want %q", resp.Meta["call_id"], expectedCallID)
	}
}

// TestCassetteAgent_NonAgentEpisode verifies that a cassette episode without
// an agent: block uses response.data as the Submission.
func TestCassetteAgent_NonAgentEpisode(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	casPath := writeCassetteFile(t, dir, "plain.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: plain_ep
    match:
      handler: agent.myagent
    response:
      data:
        choice: "a"
        score: 0.9
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	o := NewCassetteAgent(cas, "agent.myagent", nil, nil)
	defer o.Close()

	resp, err := o.Ask(context.Background(), agent.AskRequest{})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if resp.Submission == nil {
		t.Fatal("Submission nil for non-agent episode")
	}
	var sub map[string]any
	if err := json.Unmarshal(resp.Submission, &sub); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if sub["choice"] != "a" {
		t.Errorf("choice: got %v, want a", sub["choice"])
	}
}

// TestCassetteAgent_EpisodeMiss verifies that a miss returns *agent.AskError{Kind: "transport_error"}.
func TestCassetteAgent_EpisodeMiss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	casPath := writeCassetteFile(t, dir, "empty.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: other_ep
    match:
      handler: agent.other
    response:
      data: {ok: true}
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	o := NewCassetteAgent(cas, "agent.myagent", nil, nil)
	defer o.Close()

	_, err = o.Ask(context.Background(), agent.AskRequest{})
	if err == nil {
		t.Fatal("expected error on episode miss, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *agent.AskError, got %T: %v", err, err)
	}
	if ae.Kind != "transport_error" {
		t.Errorf("Kind: got %q, want transport_error", ae.Kind)
	}
}

// TestCassetteAgent_EpisodeWithError verifies that an agent: block with an
// error field returns *agent.AskError{Kind: "plugin_crash"}.
func TestCassetteAgent_EpisodeWithError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	casPath := writeCassetteFile(t, dir, "err.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: err_ep
    match:
      handler: agent.fixer
    response:
      data: {}
    agent:
      verb: ask
      error: "upstream timed out"
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	o := NewCassetteAgent(cas, "agent.fixer", nil, nil)
	defer o.Close()

	_, err = o.Ask(context.Background(), agent.AskRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *agent.AskError, got %T: %v", err, err)
	}
	if ae.Kind != "plugin_crash" {
		t.Errorf("Kind: got %q, want plugin_crash", ae.Kind)
	}
}

// TestCassetteAgent_InfraError verifies that an InfraError episode propagates
// as *agent.AskError{Kind: "transport_error"}.
func TestCassetteAgent_InfraError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	casPath := writeCassetteFile(t, dir, "infra.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: infra_ep
    match:
      handler: agent.fixer
    response:
      infra_error: "connection refused"
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	o := NewCassetteAgent(cas, "agent.fixer", nil, nil)
	defer o.Close()

	_, err = o.Ask(context.Background(), agent.AskRequest{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ae *agent.AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *agent.AskError, got %T: %v", err, err)
	}
	if ae.Kind != "transport_error" {
		t.Errorf("Kind: got %q, want transport_error", ae.Kind)
	}
}

// ─── replay:any + agent: (constraint relaxation) ───────────────────────────

// TestCassetteAgent_ReplayAnyDistinctMatchIdx verifies that replay:any episodes
// produce distinct matchIdx values on successive Ask calls, and that each call
// returns a distinct call_id derived from the matchIdx.
func TestCassetteAgent_ReplayAnyDistinctMatchIdx(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	const appID = "roundtrip_app"
	const epID = "replay_any_ep"

	casPath := writeCassetteFile(t, dir, "replay_any.yaml", `
kind: host_cassette
app_id: `+appID+`
episodes:
  - id: `+epID+`
    match:
      handler: agent.fixer
    replay: any
    agent:
      verb: ask
      response: "ok"
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette (replay:any + agent): %v", err)
	}

	o := NewCassetteAgent(cas, "agent.fixer", nil, nil)
	defer o.Close()

	const n = 4
	callIDs := make([]string, n)
	matchIdxes := make([]int, n)

	for i := 0; i < n; i++ {
		resp, askErr := o.Ask(context.Background(), agent.AskRequest{})
		if askErr != nil {
			t.Fatalf("Ask[%d]: %v", i, askErr)
		}
		if resp.Meta["episode_id"] != epID {
			t.Errorf("Ask[%d]: episode_id=%v, want %q", i, resp.Meta["episode_id"], epID)
		}
		matchIdxes[i] = MatchIdxFromMeta(resp.Meta)
		callIDs[i] = fmt.Sprintf("%v", resp.Meta["call_id"])
	}

	// matchIdx should be 0,1,2,3.
	for i, idx := range matchIdxes {
		if idx != i {
			t.Errorf("matchIdxes[%d] = %d, want %d", i, idx, i)
		}
	}

	// All call_ids must be distinct.
	seen := make(map[string]bool)
	for i, id := range callIDs {
		if seen[id] {
			t.Errorf("duplicate call_id %q at index %d", id, i)
		}
		seen[id] = true
	}

	// Each call_id should match DeriveCallID(appID, epID+":"+i).
	for i, id := range callIDs {
		expected := host.DeriveCallID(appID, fmt.Sprintf("%s:%d", epID, i))
		if id != expected {
			t.Errorf("callIDs[%d]: got %q, want %q", i, id, expected)
		}
	}
}

// TestCassetteAgent_Section63_RelaxationLoads verifies that a cassette with
// replay:any + agent: loads cleanly after the load-time constraint was relaxed.
// This was previously forbidden; the constraint is now gone.
func TestCassetteAgent_Section63_RelaxationLoads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	casPath := writeCassetteFile(t, dir, "relaxed.yaml", `
kind: host_cassette
app_id: myapp
episodes:
  - id: relaxed_ep
    match:
      handler: agent.fixer
    replay: any
    agent:
      verb: ask
      response: "ok"
`)
	_, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("replay:any + agent: must be legal; got: %v", err)
	}
}

// TestCassetteAgent_Close_Noop verifies that Close() is a no-op.
func TestCassetteAgent_Close_Noop(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	casPath := writeCassetteFile(t, dir, "close.yaml", `
kind: host_cassette
app_id: myapp
episodes: []
`)
	cas, err := LoadCassette(casPath)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	o := NewCassetteAgent(cas, "agent.fixer", nil, nil)
	if err := o.Close(); err != nil {
		t.Errorf("Close: unexpected error: %v", err)
	}
	// Second close is also safe.
	if err := o.Close(); err != nil {
		t.Errorf("second Close: unexpected error: %v", err)
	}
}
