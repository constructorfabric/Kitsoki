package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/orchestrator"
)

// cancelDriver blocks inside Turn until its execution context is cancelled, then
// returns ctx.Err() — exactly what the real orchestrator does when an operator
// cancellation propagates down to (and kills) the agent subprocess. It records
// the observed ctx error so the test can prove the cancel reached the turn.
type cancelDriver struct {
	captureDriver
	started chan struct{} // closed when Turn is entered (turn is in-flight)
	done    chan struct{} // closed when Turn returns

	mu     sync.Mutex
	ctxErr error
}

func (d *cancelDriver) Turn(ctx context.Context, _ string) (*orchestrator.TurnOutcome, error) {
	close(d.started)
	<-ctx.Done() // only the explicit session.cancel can fire this (ctx is detached from the request)
	d.mu.Lock()
	d.ctxErr = ctx.Err()
	d.mu.Unlock()
	close(d.done)
	return nil, ctx.Err()
}

// TestTurnStream_CancelStopsTurnAndEmitsCancelledFrame is the regression guard
// for the "no way to cancel an active agent" report: runstatus.session.cancel
// must cancel the turn's DETACHED execution context (so the agent actually
// stops, not just the frontend) AND the stream must terminate with a distinct
// "cancelled" frame rather than a red "error" frame.
func TestTurnStream_CancelStopsTurnAndEmitsCancelledFrame(t *testing.T) {
	drv := &cancelDriver{
		started: make(chan struct{}),
		done:    make(chan struct{}),
	}
	srv := NewWithSource(stubSource{def: &app.AppDef{}}, WithDriver(drv))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Open the streamed turn. The handler flushes headers before the turn runs,
	// so Do() returns early with an open body we read frame-by-frame.
	body, err := json.Marshal(map[string]any{"session_id": "s1", "method": "turn", "input": "hi"})
	require.NoError(t, err)
	resp, err := http.Post(ts.URL+"/rpc/turn-stream", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()

	frames := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				frames <- strings.TrimPrefix(line, "data: ")
			}
		}
		close(frames)
	}()

	<-drv.started // turn is in-flight on the server

	// Fire the cancel RPC for this session. It must report a turn was cancelled.
	cancelBody, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1,
		"method": "runstatus.session.cancel",
		"params": map[string]any{"session_id": "s1"},
	})
	require.NoError(t, err)
	cresp, err := http.Post(ts.URL+"/rpc", "application/json", strings.NewReader(string(cancelBody)))
	require.NoError(t, err)
	defer cresp.Body.Close()
	var rpcReply struct {
		Result struct {
			Cancelled bool `json:"cancelled"`
		} `json:"result"`
	}
	require.NoError(t, json.NewDecoder(cresp.Body).Decode(&rpcReply))
	assert.True(t, rpcReply.Result.Cancelled, "cancel RPC should report a turn was in flight")

	// The turn's execution context must have been cancelled (the agent stops).
	select {
	case <-drv.done:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not observe the cancellation within 2s")
	}
	drv.mu.Lock()
	ctxErr := drv.ctxErr
	drv.mu.Unlock()
	assert.ErrorIs(t, ctxErr, context.Canceled, "turn execution ctx must be cancelled by session.cancel")

	// The stream must terminate with a "cancelled" frame, never "error".
	sawCancelled := false
	for f := range frames {
		var fr struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(f), &fr); err != nil {
			continue
		}
		if fr.Type == "error" {
			t.Fatalf("cancellation must not surface as an error frame: %s", f)
		}
		if fr.Type == "cancelled" {
			sawCancelled = true
		}
	}
	assert.True(t, sawCancelled, "stream must emit a 'cancelled' terminal frame")
}

// TestSessionCancel_NoActiveTurnIsNoOp documents the idempotent path: cancelling
// when nothing is in flight reports cancelled:false rather than erroring.
func TestSessionCancel_NoActiveTurnIsNoOp(t *testing.T) {
	srv := NewWithSource(stubSource{def: &app.AppDef{}}, WithDriver(&captureDriver{}))
	out, rerr := srv.dispatch(context.Background(), "runstatus.session.cancel",
		map[string]any{"session_id": "nobody"})
	require.Nil(t, rerr)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, m["cancelled"])
}
