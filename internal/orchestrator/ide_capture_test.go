package orchestrator_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// newIDECaptureApp is a minimal one-room app wired with a JSONL eventSink (the
// trace RecordIDEContext writes to), for exercising RecordIDEContext.
func newIDECaptureApp(t *testing.T) (*orchestrator.Orchestrator, *store.JSONLSink, app.SessionID) {
	t.Helper()
	const appYAML = `
app:
  id: ide-capture-test
  version: 0.1.0
world: {}
intents:
  look:
    examples: ["look"]
root: chat
states:
  chat:
    view: "chat"
    on:
      look:
        - target: .
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	sink, err := store.OpenJSONL(filepath.Join(t.TempDir(), "trace.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	orch := orchestrator.New(def, m, s, nil,
		orchestrator.WithEventSink(sink),
		orchestrator.WithEventSinkAuthority(true),
	)
	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)
	return orch, sink, sid
}

func idePayloads(t *testing.T, sink *store.JSONLSink) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range sink.History() {
		if ev.Kind != store.IDEContextCaptured {
			continue
		}
		var p map[string]any
		require.NoError(t, json.Unmarshal(ev.Payload, &p))
		out = append(out, p)
	}
	return out
}

// TestRecordIDEContext_Injected: a captured selection that rode the turn is
// recorded with its file, lines, range, and injected=true.
func TestRecordIDEContext_Injected(t *testing.T) {
	t.Parallel()
	orch, sink, sid := newIDECaptureApp(t)

	orch.RecordIDEContext(context.Background(), sid, orchestrator.IDECaptureRecord{
		Connected: true,
		Source:    "selection",
		File:      "/repo/internal/foo.go",
		Lines:     12,
		Range:     "10:0-22:1",
		Injected:  true,
	})

	recs := idePayloads(t, sink)
	require.Len(t, recs, 1)
	r := recs[0]
	require.Equal(t, true, r["connected"])
	require.Equal(t, "selection", r["source"])
	require.Equal(t, "/repo/internal/foo.go", r["file"])
	require.EqualValues(t, 12, r["lines"])
	require.Equal(t, "10:0-22:1", r["range"])
	require.Equal(t, true, r["injected"])
}

// TestRecordIDEContext_NothingCaptured: a connected turn that found no usable
// context is still recorded (source "none" + a reason), so a "connected but the
// model didn't see my doc" report is diagnosable from the trace alone.
func TestRecordIDEContext_NothingCaptured(t *testing.T) {
	t.Parallel()
	orch, sink, sid := newIDECaptureApp(t)

	orch.RecordIDEContext(context.Background(), sid, orchestrator.IDECaptureRecord{
		Connected: true,
		Source:    "none",
		Reason:    "ambiguous_focus",
	})

	recs := idePayloads(t, sink)
	require.Len(t, recs, 1)
	r := recs[0]
	require.Equal(t, "none", r["source"])
	require.Equal(t, false, r["injected"])
	require.Equal(t, "ambiguous_focus", r["reason"])
	require.NotContains(t, r, "file", "no file when nothing was captured")
}
