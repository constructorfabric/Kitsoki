package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
)

// turnResultWire decodes a write-RPC response (the server's turnResult is
// unexported).
type turnResultWire struct {
	Mode           string   `json:"mode"`
	State          string   `json:"state"`
	View           string   `json:"view"`
	AllowedIntents []string `json:"allowed_intents"`
	ErrorCode      string   `json:"error_code"`
	ErrorMessage   string   `json:"error_message"`
	PendingIntent  string   `json:"pending_intent"`
}

// buildLiveCloak wires a live orchestrator over the cloak fixture (no harness —
// SubmitDirect bypasses routing, so no LLM is touched) behind an httptest
// server with the write Driver attached. It is the in-process equivalent of a
// `kitsoki web` session.
func buildLiveCloak(t *testing.T) *httptest.Server {
	t.Helper()
	def, err := app.Load("../../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jw, err := journal.NewSQLiteWriter(s.DB())
	require.NoError(t, err)
	jr, err := journal.NewSQLiteReader(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, nil,
		orchestrator.WithJournalWriter(jw),
		orchestrator.WithJournalReader(jr),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	sink, err := store.OpenJSONL(filepath.Join(t.TempDir(), "run.jsonl"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })

	live := server.NewLiveSession(sink, def, string(sid), string(orch.InitialState()))
	orch.SetEventSink(live)
	require.NoError(t, orch.RunInitialOnEnter(ctx, sid))

	driver := server.OrchestratorDriver{Orch: orch, SID: sid}
	ts := httptest.NewServer(server.NewWithSource(live, server.WithDriver(driver)).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestWrite_SubmitTransitions drives the happy path: a valid intent applied via
// session.submit fires a transition and the wire result carries the new state,
// rendered view, and next-menu intents.
func TestWrite_SubmitTransitions(t *testing.T) {
	t.Parallel()
	ts := buildLiveCloak(t)

	var res turnResultWire
	rpcCall(t, ts, "runstatus.session.submit",
		map[string]any{"intent": "go", "slots": map[string]any{"direction": "south"}}, &res)

	assert.Equal(t, "transitioned", res.Mode)
	assert.True(t, strings.HasPrefix(res.State, "bar"), "foyer --go south--> bar, got %q", res.State)
	assert.NotEmpty(t, res.View, "transition should carry a rendered view")
	assert.NotEmpty(t, res.AllowedIntents, "next state should expose a menu")

	// The live read header must reflect the transition: turn.end is stamped
	// with the turn's STARTING state (foyer), so the header derivation has to
	// prefer the last state_entered (bar.dark), not the last state_path.
	var header struct {
		CurrentState string `json:"current_state"`
	}
	rpcCall(t, ts, "runstatus.session.get", nil, &header)
	assert.True(t, strings.HasPrefix(header.CurrentState, "bar"),
		"session.get should report the entered state after a transition, got %q", header.CurrentState)
}

// TestWrite_SubmitRejected proves a rejection rides back as a structured
// result (mode=rejected + reason), NOT as a transport error: hang_cloak is not
// allowed in the foyer.
func TestWrite_SubmitRejected(t *testing.T) {
	t.Parallel()
	ts := buildLiveCloak(t)

	var res turnResultWire
	rpcCall(t, ts, "runstatus.session.submit",
		map[string]any{"intent": "hang_cloak"}, &res)

	assert.Equal(t, "rejected", res.Mode)
	assert.NotEmpty(t, res.ErrorMessage, "rejection should explain itself")
}

// TestWrite_MissingIntent rejects a submit with no intent as a transport error
// (a malformed request, not an interpreted outcome).
func TestWrite_MissingIntent(t *testing.T) {
	t.Parallel()
	ts := buildLiveCloak(t)
	code, msg := rpcCallExpectError(t, ts, "runstatus.session.submit", map[string]any{})
	assert.NotZero(t, code)
	assert.Contains(t, msg, "intent")
}

// TestWrite_ReadOnlySurfaceRejectsWrites proves the gating: a Server with no
// Driver (the `status serve` shape) refuses write RPCs with codeReadOnly while
// still answering reads.
func TestWrite_ReadOnlySurfaceRejectsWrites(t *testing.T) {
	t.Parallel()
	def := testDef()
	_, live := openLiveSink(t, def, "ro-1", "main")
	ts := httptest.NewServer(server.NewWithSource(live).Handler()) // no WithDriver
	defer ts.Close()

	code, msg := rpcCallExpectError(t, ts, "runstatus.session.submit",
		map[string]any{"intent": "go"})
	assert.Equal(t, -32001, code, "read-only surface should return codeReadOnly")
	assert.Contains(t, msg, "read-only")

	// Reads still work.
	var header struct {
		SessionID string `json:"session_id"`
	}
	rpcCall(t, ts, "runstatus.session.get", nil, &header)
	assert.Equal(t, "ro-1", header.SessionID)
}

// rpcCallExpectError posts an RPC expecting a JSON-RPC error, returning its
// code and message.
func rpcCallExpectError(t *testing.T, ts *httptest.Server, method string, params map[string]any) (int, string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	resp, err := http.Post(ts.URL+"/rpc", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	var frame struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&frame))
	require.NotNil(t, frame.Error, "expected an rpc error for %s", method)
	return frame.Error.Code, frame.Error.Message
}
