package server_test

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
)

// openLiveSink opens a fresh JSONLSink in a temp dir and wraps it as a
// LiveSession over def. It is the in-process equivalent of writeTrace.
func openLiveSink(t *testing.T, def *app.AppDef, sid, initialState string) (*store.JSONLSink, *server.LiveSession) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	sink, err := store.OpenJSONL(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sink.Close() })
	return sink, server.NewLiveSession(sink, def, sid, initialState)
}

// TestLiveSession_SnapshotReflectsAppends proves the live in-process source
// (Phase 1.2): events appended through the LiveSession show up in the Snapshot
// it serves, with Kind mapped to Msg and state_path preserved — the same shape
// ParseTrace yields for the read-only file path.
func TestLiveSession_SnapshotReflectsAppends(t *testing.T) {
	t.Parallel()
	def := testDef()
	sink, live := openLiveSink(t, def, "sess-1", "main")

	// Before any event: header backfills CurrentState from the initial state.
	snap, err := live.Snapshot()
	require.NoError(t, err)
	assert.Empty(t, snap.Events)
	assert.Equal(t, "main", snap.Session.CurrentState, "initial state backfilled before first event")

	require.NoError(t, sink.Append(store.Event{
		Turn: 1, Kind: store.StateEntered, StatePath: "lobby",
		Payload: json.RawMessage(`{"state":"lobby"}`),
	}))
	require.NoError(t, sink.Append(store.Event{
		Turn: 1, Kind: store.TransitionApplied, StatePath: "lobby",
		Payload: json.RawMessage(`{"intent":"go_north"}`),
	}))

	snap, err = live.Snapshot()
	require.NoError(t, err)
	require.Len(t, snap.Events, 2)
	assert.Equal(t, string(store.StateEntered), snap.Events[0].Msg)
	assert.Equal(t, "lobby", snap.Events[0].StatePath)
	assert.Equal(t, string(store.TransitionApplied), snap.Events[1].Msg)
	// state_path drives CurrentState once events carry it.
	assert.Equal(t, "lobby", snap.Session.CurrentState)
	assert.Equal(t, 1, snap.Session.Turn)
	assert.Equal(t, "test-app", snap.Session.AppID)

	// Events() (the cheap SSE-poll path) agrees with Snapshot().Events.
	evs, err := live.Events()
	require.NoError(t, err)
	require.Len(t, evs, 2)
	assert.Equal(t, snap.Events[1].Msg, evs[1].Msg)
}

// TestLiveSession_ServesOverRPC proves a Server backed by a live source answers
// the same JSON-RPC contract the SPA expects, so the existing frontend observes
// an in-process session unchanged.
func TestLiveSession_ServesOverRPC(t *testing.T) {
	t.Parallel()
	def := testDef()
	sink, live := openLiveSink(t, def, "sess-2", "main")
	require.NoError(t, sink.Append(store.Event{
		Turn: 1, Kind: store.StateEntered, StatePath: "lobby",
		Payload: json.RawMessage(`{"state":"lobby"}`),
	}))

	ts := httptest.NewServer(server.NewWithSource(live).Handler())
	defer ts.Close()

	var header runstatus.SessionHeader
	rpcCall(t, ts, "runstatus.session.get", nil, &header)
	assert.Equal(t, "sess-2", header.SessionID)
	assert.Equal(t, "lobby", header.CurrentState)

	var trace traceResult
	rpcCall(t, ts, "runstatus.session.trace", nil, &trace)
	require.Len(t, trace.Events, 1)
	assert.Equal(t, string(store.StateEntered), trace.Events[0].Msg)
	assert.Equal(t, 1, trace.LastTurn)

	var appDef app.AppDef
	rpcCall(t, ts, "runstatus.session.app", nil, &appDef)
	assert.Equal(t, "test-app", appDef.App.ID)
}

// traceResult mirrors the runstatus.session.trace response shape (the server's
// internal type is unexported).
type traceResult struct {
	Events   []runstatus.TraceEvent `json:"events"`
	LastTurn int                    `json:"last_turn"`
}

// TestLiveSession_ConcurrentAppendAndRead exercises the lock that lets the
// orchestrator append while the HTTP server reads. The underlying JSONLSink is
// not safe for concurrent Append + History, so this test must stay green under
// `go test -race`.
func TestLiveSession_ConcurrentAppendAndRead(t *testing.T) {
	t.Parallel()
	def := testDef()
	_, live := openLiveSink(t, def, "sess-3", "main")

	const n = 200
	var wg sync.WaitGroup
	wg.Add(2)

	// Writer: the orchestrator's append path.
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_ = live.Append(store.Event{
				Turn: app.TurnNumber(i + 1), Kind: store.TransitionApplied,
				StatePath: "lobby", Payload: json.RawMessage(`{}`),
			})
		}
	}()

	// Reader: the server's snapshot/poll path.
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			snap, err := live.Snapshot()
			require.NoError(t, err)
			// Events count only grows; never observe a torn slice.
			_ = snap.Events
			if _, err := live.Events(); err != nil {
				require.NoError(t, err)
			}
		}
	}()

	wg.Wait()

	final, err := live.Snapshot()
	require.NoError(t, err)
	assert.Len(t, final.Events, n)
}
