package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestFileTranscriptWriterFinalize is the file-writer contract with teeth: two
// verbatim events + one synthetic, finalized, must produce a byte-exact .jsonl
// (one event per line, no re-marshalling), a parallel .timings carrying the
// offsets, and a TranscriptRef whose Events count and relative Path are correct.
func TestFileTranscriptWriterFinalize(t *testing.T) {
	t.Parallel()

	traceDir := t.TempDir()
	transcriptsDir := filepath.Join(traceDir, "transcripts")
	w := NewFileTranscriptWriter(transcriptsDir)

	const callID = "2d8e4fbb0a78646d"
	const format = "claude-stream-json"

	// Deliberately use compact-but-unusual key order / spacing so a byte-exact
	// assertion proves we do NOT re-marshal (which would normalize it).
	e1 := json.RawMessage(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`)
	e2 := json.RawMessage(`{"type":"tool_use","id":"toolu_01","name":"Read","input":{"file_path":"foo.go"}}`)
	synth := json.RawMessage(`{"_kitsoki":"nudge","outer_iter":1,"text":"retry please"}`)

	w.Append(callID, format, e1, 0)
	w.Append(callID, format, e2, 120)
	w.AppendSynthetic(callID, format, synth, 130)

	ref := w.Finalize(callID, format)
	if ref == nil {
		t.Fatal("Finalize returned nil after appending events")
	}
	if ref.Events != 3 {
		t.Errorf("ref.Events = %d, want 3", ref.Events)
	}
	if ref.SchemaVersion != TranscriptSchemaVersion {
		t.Errorf("ref.SchemaVersion = %d, want %d", ref.SchemaVersion, TranscriptSchemaVersion)
	}
	if ref.Format != format {
		t.Errorf("ref.Format = %q, want %q", ref.Format, format)
	}
	wantPath := "transcripts/" + callID + ".jsonl"
	if ref.Path != wantPath {
		t.Errorf("ref.Path = %q, want %q", ref.Path, wantPath)
	}

	// .jsonl must be byte-exact: each verbatim event on its own line, in order.
	gotJSONL, err := os.ReadFile(filepath.Join(transcriptsDir, callID+".jsonl"))
	if err != nil {
		t.Fatalf("read .jsonl: %v", err)
	}
	wantJSONL := string(e1) + "\n" + string(e2) + "\n" + string(synth) + "\n"
	if string(gotJSONL) != wantJSONL {
		t.Errorf(".jsonl not byte-exact:\n got %q\nwant %q", gotJSONL, wantJSONL)
	}

	// .timings carries event-index -> ms offset, out of the verbatim stream.
	gotTimings, err := os.ReadFile(filepath.Join(transcriptsDir, callID+".timings"))
	if err != nil {
		t.Fatalf("read .timings: %v", err)
	}
	wantTimings := "0 0\n1 120\n2 130\n"
	if string(gotTimings) != wantTimings {
		t.Errorf(".timings:\n got %q\nwant %q", gotTimings, wantTimings)
	}
}

// TestFileTranscriptWriterNoEvents verifies a call_id that never accumulated an
// event finalizes to nil (no affordance) and writes no sidecar files.
func TestFileTranscriptWriterNoEvents(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "transcripts")
	w := NewFileTranscriptWriter(dir)

	if ref := w.Finalize("never-appended", "claude-stream-json"); ref != nil {
		t.Errorf("Finalize with no events = %+v, want nil", ref)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("transcripts dir should not be created when nothing was finalized (err=%v)", err)
	}
}

// TestTranscriptWriterCtxSeam verifies the nil-safe context seam mirrors
// StreamSink: a nil writer leaves ctx unchanged and reads back nil; a real
// writer round-trips.
func TestTranscriptWriterCtxSeam(t *testing.T) {
	t.Parallel()

	// Nil writer is a no-op: ctx returned unchanged, reads back nil.
	if ctx := WithTranscriptWriter(context.Background(), nil); TranscriptWriterFrom(ctx) != nil {
		t.Error("WithTranscriptWriter(ctx, nil) should not install a writer")
	}
	if got := TranscriptWriterFrom(context.Background()); got != nil {
		t.Errorf("TranscriptWriterFrom on bare ctx = %v, want nil", got)
	}

	w := NewFileTranscriptWriter(t.TempDir())
	ctx2 := WithTranscriptWriter(context.Background(), w)
	if got := TranscriptWriterFrom(ctx2); got == nil {
		t.Error("TranscriptWriterFrom after WithTranscriptWriter returned nil")
	}
}

// TestClaudeTeeToSidecar verifies the in-host claude path (Task 2.1): when a
// TranscriptWriter and a call_id are installed in ctx, runClaudeStreamJSON tees
// each verbatim stream-json event to the writer keyed by that call_id, and the
// teed bytes are byte-identical to what RawEvents holds. Drives the stub runner
// (no real claude) so it is fast and cost-free.
func TestClaudeTeeToSidecar(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := NewFileTranscriptWriter(dir)

	const stub = `{"type":"system","subtype":"init","session_id":"sid-1"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":10,"output_tokens":2}}`

	ctx := WithClaudeRunner(context.Background(), func(_ context.Context, _ []string, _, _ string) (ClaudeRun, error) {
		return ClaudeRun{Stdout: stub}, nil
	})
	ctx = WithTranscriptWriter(ctx, w)
	ctx = WithCallID(ctx, "callabc")

	cr, _, err := runClaudeStreamJSON(ctx, "stub://claude", nil, "prompt", "")
	if err != nil {
		t.Fatalf("runClaudeStreamJSON: %v", err)
	}
	if len(cr.RawEvents) != 3 {
		t.Fatalf("RawEvents: got %d want 3", len(cr.RawEvents))
	}

	ref := w.Finalize("callabc", claudeTranscriptFormat)
	if ref == nil {
		t.Fatal("Finalize returned nil; expected a transcript ref")
	}
	if ref.Events != 3 {
		t.Errorf("ref.Events: got %d want 3", ref.Events)
	}
	// The sidecar bytes must be byte-identical to RawEvents (verbatim, one/line).
	got, rerr := os.ReadFile(filepath.Join(dir, "callabc.jsonl"))
	if rerr != nil {
		t.Fatalf("read sidecar: %v", rerr)
	}
	var want string
	for _, ev := range cr.RawEvents {
		want += string(ev) + "\n"
	}
	if string(got) != want {
		t.Errorf("sidecar not byte-identical to RawEvents:\n got=%q\nwant=%q", string(got), want)
	}
}

// TestClaudeTeeSkippedWithoutCallID verifies the tee is a no-op when no call_id
// is in ctx (a non-agent claude invocation), so no sidecar is written.
func TestClaudeTeeSkippedWithoutCallID(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	w := NewFileTranscriptWriter(dir)
	ctx := WithClaudeRunner(context.Background(), func(_ context.Context, _ []string, _, _ string) (ClaudeRun, error) {
		return ClaudeRun{Stdout: `{"type":"result","subtype":"success","result":"x"}`}, nil
	})
	ctx = WithTranscriptWriter(ctx, w)
	// No WithCallID.
	if _, _, err := runClaudeStreamJSON(ctx, "stub://claude", nil, "p", ""); err != nil {
		t.Fatalf("runClaudeStreamJSON: %v", err)
	}
	if ref := w.Finalize("", claudeTranscriptFormat); ref != nil {
		t.Errorf("expected no transcript when call_id absent, got %+v", ref)
	}
}
