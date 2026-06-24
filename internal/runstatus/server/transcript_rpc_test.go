package server_test

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/runstatus/server"
)

// TestServer_SessionTranscript_ReadsSidecar drives the runstatus.session.transcript
// RPC end-to-end: a LiveSession's TranscriptsDir() is populated with a small
// <call_id>.jsonl + .timings pair, and the RPC returns the verbatim events keyed
// back to JSON plus the per-index ms offsets. This is the data plane the
// "Agent actions" drawer consumes.
func TestServer_SessionTranscript_ReadsSidecar(t *testing.T) {
	t.Parallel()
	def := testDef()
	sink, live := openLiveSink(t, def, "s-1", "main")
	_ = sink

	// Write a tiny sidecar into the session's transcripts dir.
	dir := live.TranscriptsDir()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	const callID = "4e96533378e89461"
	jsonl := `{"type":"system","subtype":"init","session_id":"abc","model":"claude-x"}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_03","name":"Edit","input":{"file_path":"foo.go"}}]}}
{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":1200,"output_tokens":640},"total_cost_usd":0.04}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, callID+".jsonl"), []byte(jsonl), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, callID+".timings"), []byte("0 0\n1 120\n2 3400\n"), 0o644))

	ts := httptest.NewServer(server.NewWithSource(live).Handler())
	defer ts.Close()

	var out struct {
		Format        string            `json:"format"`
		Events        []map[string]any  `json:"events"`
		Timings       []int64           `json:"timings"`
		SchemaVersion int               `json:"schema_version"`
	}
	rpcCall(t, ts, "runstatus.session.transcript",
		map[string]any{"session_id": "s-1", "call_id": callID}, &out)

	require.Len(t, out.Events, 3)
	assert.Equal(t, "system", out.Events[0]["type"])
	assert.Equal(t, "assistant", out.Events[1]["type"])
	assert.Equal(t, "result", out.Events[2]["type"])
	require.Len(t, out.Timings, 3)
	assert.Equal(t, int64(0), out.Timings[0])
	assert.Equal(t, int64(120), out.Timings[1])
	assert.Equal(t, int64(3400), out.Timings[2])
	assert.Equal(t, 1, out.SchemaVersion)
}

// TestServer_SessionTranscript_MissingSidecar proves an absent sidecar (a call
// with no transcript_ref) is NOT a 500 — it returns an empty, well-formed
// payload so the SPA shows no affordance rather than an error.
func TestServer_SessionTranscript_MissingSidecar(t *testing.T) {
	t.Parallel()
	_, live := openLiveSink(t, testDef(), "s-1", "main")

	ts := httptest.NewServer(server.NewWithSource(live).Handler())
	defer ts.Close()

	var out struct {
		Events  []map[string]any `json:"events"`
		Timings []int64          `json:"timings"`
	}
	rpcCall(t, ts, "runstatus.session.transcript",
		map[string]any{"session_id": "s-1", "call_id": "does-not-exist"}, &out)
	assert.Empty(t, out.Events)
	assert.Empty(t, out.Timings)
}
