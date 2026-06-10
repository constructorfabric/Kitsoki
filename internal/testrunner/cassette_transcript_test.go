package testrunner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// TestEpisodeOracle_TranscriptReplayGolden is the agent-action-transcripts
// golden contract (proposal Tasks 3.1/3.2): driving a host.oracle.task episode
// whose oracle: block carries a recorded transcript: must write a per-call
// <call_id>.jsonl sidecar that is BYTE-IDENTICAL to the recorded events
// (canonicalized), a matching .timings sidecar, and a transcript_ref on the
// oracle.call.complete event whose events count matches — with NO live tool run.
func TestEpisodeOracle_TranscriptReplayGolden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// A small but realistic claude stream-json transcript: a Read tool_use, its
	// tool_result, and the terminal result event with usage.
	p := writeCassetteFile(t, dir, "transcript_oracle.yaml", `
kind: host_cassette
app_id: bugfix
episodes:
  - id: phase_1_oracle
    match:
      handler: host.oracle.task
    response:
      data: {submitted: {found: true}}
    oracle:
      verb: task
      agent: bugfix
      model: claude-sonnet-4-6
      duration_ms: 2100
      response: '{"found":true}'
      transcript:
        format: claude-stream-json
        events:
          - '{"type":"system","subtype":"init","session_id":"s1"}'
          - '{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the test."}]}}'
          - '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"foo_test.go"}}]}}'
          - '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"package foo"}]}}'
          - '{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":120,"output_tokens":40},"total_cost_usd":0.01}'
        timings: [0, 250, 410, 880, 1500]
`)

	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	// Install the same ctx seam the orchestrator installs: an event sink, the
	// oracle call ctx, and a file-backed transcript writer pointing at a
	// transcripts/ dir (sibling of oracle-prompts/, as in host_dispatch.go).
	traceDir := filepath.Join(dir, "run")
	if mkErr := os.MkdirAll(traceDir, 0o755); mkErr != nil {
		t.Fatalf("mkdir traceDir: %v", mkErr)
	}
	transcriptsDir := filepath.Join(traceDir, "transcripts")

	sink := newMemSink()
	ctx := host.WithOracleCallCtx(context.Background(), host.OracleCallCtx{
		SessionID: app.SessionID("golden-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: "phase_1.dispatching",
	})
	ctx = host.WithOraclePromptsDir(ctx, filepath.Join(traceDir, "oracle-prompts"))
	ctx = host.WithTranscriptWriter(ctx, host.NewFileTranscriptWriter(transcriptsDir))

	clk := newFakeClock()
	stateOf := func() string { return "phase_1.dispatching" }
	// nil fallback: a cassette miss (or any live tool) would error — proving the
	// replay writes the sidecar from the cassette alone, never from a live run.
	dispatch := BuildCassetteDispatcherWithSink(cas, "host.oracle.task", stateOf, nil, nil, clk, sink, nil)

	if _, derr := dispatch(ctx, nil); derr != nil {
		t.Fatalf("dispatch: %v", derr)
	}

	// The deterministic call_id pairs the sidecar with the trace event.
	callID := host.DeriveCallID("bugfix", "phase_1_oracle:0")

	// (a) The <call_id>.jsonl sidecar is byte-identical to the recorded events,
	// VERBATIM — authored key order preserved, number literals untouched — one
	// event per line with a trailing newline. (No re-marshaling: this is the
	// determinism invariant that makes a live-captured-then-folded transcript
	// replay byte-for-byte; see EpisodeTranscript.eventLines.)
	wantEvents := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Reading the test."}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"foo_test.go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"package foo"}]}}`,
		`{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":120,"output_tokens":40},"total_cost_usd":0.01}`,
	}
	want := strings.Join(wantEvents, "\n") + "\n"
	gotBytes, rerr := os.ReadFile(filepath.Join(transcriptsDir, callID+".jsonl"))
	if rerr != nil {
		t.Fatalf("read jsonl sidecar: %v", rerr)
	}
	if string(gotBytes) != want {
		t.Errorf("jsonl sidecar not byte-identical:\n got=%q\nwant=%q", string(gotBytes), want)
	}

	// (c) The .timings sidecar matches the recorded offsets, one "<idx> <ms>" per line.
	wantTimings := "0 0\n1 250\n2 410\n3 880\n4 1500\n"
	gotTimings, terr := os.ReadFile(filepath.Join(transcriptsDir, callID+".timings"))
	if terr != nil {
		t.Fatalf("read timings sidecar: %v", terr)
	}
	if string(gotTimings) != wantTimings {
		t.Errorf("timings sidecar mismatch:\n got=%q\nwant=%q", string(gotTimings), wantTimings)
	}

	// (b) transcript_ref on oracle.call.complete carries the event count + path.
	var ref *host.TranscriptRef
	for _, ev := range sink.History() {
		if ev.Kind != store.OracleReturned {
			continue
		}
		var p host.OracleReturnedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("unmarshal OracleReturned payload: %v", err)
		}
		ref = p.TranscriptRef
	}
	if ref == nil {
		t.Fatal("expected transcript_ref on oracle.call.complete, got nil")
	}
	if ref.Events != len(wantEvents) {
		t.Errorf("transcript_ref.events: got %d want %d", ref.Events, len(wantEvents))
	}
	if ref.Format != "claude-stream-json" {
		t.Errorf("transcript_ref.format: got %q", ref.Format)
	}
	if ref.SchemaVersion != host.TranscriptSchemaVersion {
		t.Errorf("transcript_ref.schema_version: got %d want %d", ref.SchemaVersion, host.TranscriptSchemaVersion)
	}
	if ref.Path != "transcripts/"+callID+".jsonl" {
		t.Errorf("transcript_ref.path: got %q", ref.Path)
	}

	// Determinism: a second replay produces the byte-identical sidecar.
	transcriptsDir2 := filepath.Join(traceDir, "transcripts2")
	ctx2 := host.WithOracleCallCtx(context.Background(), host.OracleCallCtx{
		SessionID: app.SessionID("golden-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: "phase_1.dispatching",
	})
	ctx2 = host.WithTranscriptWriter(ctx2, host.NewFileTranscriptWriter(transcriptsDir2))
	cas2, _ := LoadCassette(p)
	dispatch2 := BuildCassetteDispatcherWithSink(cas2, "host.oracle.task", stateOf, nil, nil, clk, newMemSink(), nil)
	if _, derr := dispatch2(ctx2, nil); derr != nil {
		t.Fatalf("dispatch2: %v", derr)
	}
	gotBytes2, _ := os.ReadFile(filepath.Join(transcriptsDir2, callID+".jsonl"))
	if string(gotBytes2) != want {
		t.Errorf("second replay sidecar not byte-identical to first")
	}
}

// TestEpisodeOracle_DecideTranscriptReplayGolden is the decide-arc golden
// (proposal "The decide submit → validate → nudge cycle"): a host.oracle.decide
// episode whose recorded transcript carries the synthetic _kitsoki boundary rows
// (validator_reject, nudge, validator_accept) interleaved with claude's verbatim
// events must replay byte-identical, with the _kitsoki rows in order — so the
// drawer renders the full submit → reject → host-nudge → re-submit → accept arc.
func TestEpisodeOracle_DecideTranscriptReplayGolden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	p := writeCassetteFile(t, dir, "decide_transcript.yaml", `
kind: host_cassette
app_id: bugfix
episodes:
  - id: phase_decide
    match:
      handler: host.oracle.decide
    response:
      data: {submitted: {decision: "refund", amount: 49.0}}
    oracle:
      verb: decide
      agent: bugfix
      model: claude-sonnet-4-6
      duration_ms: 1500
      response: '{"decision":"refund","amount":49.0}'
      transcript:
        format: claude-stream-json
        events:
          - '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0a","name":"mcp__validator__submit","input":{"decision":"refund","amount":"lots"}}]}}'
          - '{"_kitsoki":"validator_reject","source":"schema","reason":"amount: expected number, got string \"lots\""}'
          - '{"_kitsoki":"nudge","outer_iter":1,"text":"The last submission attempt was rejected: amount: expected number"}'
          - '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0b","name":"mcp__validator__submit","input":{"decision":"refund","amount":49.0}}]}}'
          - '{"_kitsoki":"validator_accept","outer_iter":1}'
          - '{"type":"result","subtype":"success","result":"refund","usage":{"input_tokens":1400,"output_tokens":320},"total_cost_usd":0.03}'
        timings: [0, 60, 80, 520, 720, 1000]
`)

	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}

	traceDir := filepath.Join(dir, "run")
	if mkErr := os.MkdirAll(traceDir, 0o755); mkErr != nil {
		t.Fatalf("mkdir traceDir: %v", mkErr)
	}
	transcriptsDir := filepath.Join(traceDir, "transcripts")

	sink := newMemSink()
	ctx := host.WithOracleCallCtx(context.Background(), host.OracleCallCtx{
		SessionID: app.SessionID("decide-sess"),
		Turn:      app.TurnNumber(1),
		StatePath: "phase.dispatching",
	})
	ctx = host.WithTranscriptWriter(ctx, host.NewFileTranscriptWriter(transcriptsDir))

	clk := newFakeClock()
	stateOf := func() string { return "phase.dispatching" }
	dispatch := BuildCassetteDispatcherWithSink(cas, "host.oracle.decide", stateOf, nil, nil, clk, sink, nil)
	if _, derr := dispatch(ctx, nil); derr != nil {
		t.Fatalf("dispatch: %v", derr)
	}

	callID := host.DeriveCallID("bugfix", "phase_decide:0")

	// The sidecar replays byte-identical and VERBATIM (authored key order and the
	// 49.0 literal preserved — no re-marshaling), with the synthetic _kitsoki rows
	// interleaved in recorded order.
	wantEvents := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0a","name":"mcp__validator__submit","input":{"decision":"refund","amount":"lots"}}]}}`,
		`{"_kitsoki":"validator_reject","source":"schema","reason":"amount: expected number, got string \"lots\""}`,
		`{"_kitsoki":"nudge","outer_iter":1,"text":"The last submission attempt was rejected: amount: expected number"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_0b","name":"mcp__validator__submit","input":{"decision":"refund","amount":49.0}}]}}`,
		`{"_kitsoki":"validator_accept","outer_iter":1}`,
		`{"type":"result","subtype":"success","result":"refund","usage":{"input_tokens":1400,"output_tokens":320},"total_cost_usd":0.03}`,
	}
	want := strings.Join(wantEvents, "\n") + "\n"
	gotBytes, rerr := os.ReadFile(filepath.Join(transcriptsDir, callID+".jsonl"))
	if rerr != nil {
		t.Fatalf("read jsonl sidecar: %v", rerr)
	}
	if string(gotBytes) != want {
		t.Errorf("decide sidecar not byte-identical:\n got=%q\nwant=%q", string(gotBytes), want)
	}

	// The _kitsoki boundary rows are present and in order (reject → nudge → accept).
	got := string(gotBytes)
	rejIdx := strings.Index(got, `"_kitsoki":"validator_reject"`)
	nudgeIdx := strings.Index(got, `"_kitsoki":"nudge"`)
	acceptIdx := strings.Index(got, `"_kitsoki":"validator_accept"`)
	if rejIdx < 0 || nudgeIdx < 0 || acceptIdx < 0 {
		t.Fatalf("missing _kitsoki rows: reject=%d nudge=%d accept=%d", rejIdx, nudgeIdx, acceptIdx)
	}
	if !(rejIdx < nudgeIdx && nudgeIdx < acceptIdx) {
		t.Errorf("_kitsoki rows out of order: reject=%d nudge=%d accept=%d", rejIdx, nudgeIdx, acceptIdx)
	}

	// transcript_ref counts every line (verbatim + synthetic).
	var ref *host.TranscriptRef
	for _, ev := range sink.History() {
		if ev.Kind != store.OracleReturned {
			continue
		}
		var pl host.OracleReturnedPayload
		if err := json.Unmarshal(ev.Payload, &pl); err != nil {
			t.Fatalf("unmarshal OracleReturned payload: %v", err)
		}
		ref = pl.TranscriptRef
	}
	if ref == nil {
		t.Fatal("expected transcript_ref on oracle.call.complete, got nil")
	}
	if ref.Events != len(wantEvents) {
		t.Errorf("transcript_ref.events: got %d want %d", ref.Events, len(wantEvents))
	}
}

// TestEpisodeOracle_NoTranscriptNoSidecar verifies an episode without a
// transcript: block writes no sidecar and no transcript_ref (backward compat).
func TestEpisodeOracle_NoTranscriptNoSidecar(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := writeCassetteFile(t, dir, "no_transcript.yaml", `
kind: host_cassette
app_id: bugfix
episodes:
  - id: ep_plain
    match:
      handler: host.oracle.ask
    response:
      data: {ok: true}
    oracle:
      verb: ask
      agent: bugfix
      response: "ok"
`)
	cas, err := LoadCassette(p)
	if err != nil {
		t.Fatalf("LoadCassette: %v", err)
	}
	transcriptsDir := filepath.Join(dir, "transcripts")
	sink := newMemSink()
	ctx := host.WithOracleCallCtx(context.Background(), host.OracleCallCtx{
		SessionID: app.SessionID("s"), Turn: 1, StatePath: "p.d",
	})
	ctx = host.WithTranscriptWriter(ctx, host.NewFileTranscriptWriter(transcriptsDir))
	clk := newFakeClock()
	dispatch := BuildCassetteDispatcherWithSink(cas, "host.oracle.ask", func() string { return "p.d" }, nil, nil, clk, sink, nil)
	if _, derr := dispatch(ctx, nil); derr != nil {
		t.Fatalf("dispatch: %v", derr)
	}
	if _, statErr := os.Stat(transcriptsDir); statErr == nil {
		t.Errorf("transcripts dir should not be created when no transcript: block")
	}
	for _, ev := range sink.History() {
		if ev.Kind != store.OracleReturned {
			continue
		}
		var pl host.OracleReturnedPayload
		_ = json.Unmarshal(ev.Payload, &pl)
		if pl.TranscriptRef != nil {
			t.Errorf("expected nil transcript_ref, got %+v", pl.TranscriptRef)
		}
	}
}
