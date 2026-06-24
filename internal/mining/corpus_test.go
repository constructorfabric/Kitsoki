package mining

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

func TestDirJSONLSource_DiscoverFiltersByMTime(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.jsonl")
	newPath := filepath.Join(dir, "new.jsonl")
	mustWrite(t, oldPath, []byte(`{"schema_version":"session-corpus.v1"}`))
	mustWrite(t, newPath, []byte(`{"schema_version":"session-corpus.v1"}`))
	oldTime := time.Unix(100, 0)
	newTime := time.Unix(200, 0)
	requireNoError(t, os.Chtimes(oldPath, oldTime, oldTime))
	requireNoError(t, os.Chtimes(newPath, newTime, newTime))

	src := DirJSONLSource{Backend: SourceImportedJSONL}
	refs, err := src.Discover(context.Background(), SourceScope{Dirs: []string{dir}, SinceMTime: 150})
	requireNoError(t, err)

	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1: %+v", len(refs), refs)
	}
	if refs[0].ID != "new" || refs[0].Source != SourceImportedJSONL {
		t.Fatalf("unexpected ref: %+v", refs[0])
	}
}

func TestDirJSONLSource_LoadKitsokiTraceCanonical(t *testing.T) {
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "run.jsonl")
	lines := [][]byte{
		[]byte(`{"kind":"session.header","schema_version":1,"written_at":"2026-06-22T00:00:00Z"}`),
		eventLine(t, store.Event{Turn: 1, Seq: 0, Kind: store.TurnStarted, StatePath: app.StatePath("review"), Payload: raw(t, map[string]any{
			"input": "accept it", "routed_by": "semantic", "intent": "accept",
		})}),
		eventLine(t, store.Event{Turn: 1, Seq: 1, Kind: store.TransitionApplied, StatePath: app.StatePath("review"), Payload: raw(t, map[string]any{
			"from": "review", "to": "done", "intent": "accept",
		})}),
		eventLine(t, store.Event{Turn: 1, Seq: 2, Kind: store.EffectApplied, StatePath: app.StatePath("done"), Payload: raw(t, map[string]any{
			"flows_green": true,
		})}),
	}
	requireNoError(t, os.WriteFile(tracePath, appendLines(lines), 0o644))

	src := DirJSONLSource{Backend: SourceKitsokiTrace}
	sess, err := src.Load(context.Background(), SessionRef{Source: SourceKitsokiTrace, ID: "run", Path: tracePath})
	requireNoError(t, err)

	if sess.SchemaVersion != "session-corpus.v1" || sess.Source.Backend != SourceKitsokiTrace {
		t.Fatalf("unexpected canonical session header: %+v", sess)
	}
	if sess.Kitsoki == nil {
		t.Fatal("missing kitsoki block")
	}
	if got := sess.Kitsoki.Rooms; len(got) != 2 || got[0] != "done" || got[1] != "review" {
		t.Fatalf("rooms = %+v, want [done review]", got)
	}
	if got := sess.Kitsoki.Intents; len(got) != 1 || got[0] != "accept" {
		t.Fatalf("intents = %+v, want [accept]", got)
	}
	if len(sess.Kitsoki.WorldChanges) != 1 {
		t.Fatalf("world changes = %d, want 1", len(sess.Kitsoki.WorldChanges))
	}
	if len(sess.Kitsoki.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(sess.Kitsoki.Events))
	}
}

func TestTranscriptJSONLSource_LoadClaudeStyleToolCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	body := "" +
		`{"type":"user","message":{"content":"fix the tests"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"I will run them."},{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}` + "\n"
	requireNoError(t, os.WriteFile(path, []byte(body), 0o644))

	src := TranscriptJSONLSource{Backend: SourceClaudeCode}
	sess, err := src.Load(context.Background(), SessionRef{Source: SourceClaudeCode, ID: "session", Path: path})
	requireNoError(t, err)

	if sess.Source.Backend != SourceClaudeCode {
		t.Fatalf("backend = %q, want %q", sess.Source.Backend, SourceClaudeCode)
	}
	if len(sess.Turns) != 2 {
		t.Fatalf("turns = %d, want 2: %+v", len(sess.Turns), sess.Turns)
	}
	if sess.Turns[0].Text != "fix the tests" {
		t.Fatalf("first turn text = %q", sess.Turns[0].Text)
	}
	if len(sess.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(sess.ToolCalls))
	}
	if sess.ToolCalls[0].Tool != "Bash" || sess.ToolCalls[0].SourceRef.Line != 2 {
		t.Fatalf("unexpected tool call: %+v", sess.ToolCalls[0])
	}
}

func eventLine(t *testing.T, ev store.Event) []byte {
	t.Helper()
	b, err := json.Marshal(ev)
	requireNoError(t, err)
	return b
}

func raw(t *testing.T, v map[string]any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	requireNoError(t, err)
	return b
}

func appendLines(lines [][]byte) []byte {
	var out []byte
	for _, line := range lines {
		out = append(out, line...)
		out = append(out, '\n')
	}
	return out
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	requireNoError(t, os.WriteFile(path, data, 0o644))
}

func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
