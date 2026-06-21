package server

import (
	"context"
	"encoding/json"
	"io"
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

// detachDriver blocks inside Turn until the test releases it, then records
// whether the context it was driven with had been cancelled. It reuses
// captureDriver (identity_test.go) for the rest of the Driver surface.
type detachDriver struct {
	captureDriver
	started chan struct{} // closed when Turn is entered (turn is in-flight)
	release chan struct{} // closed by the test after it disconnects the client
	done    chan struct{} // closed when Turn returns

	mu     sync.Mutex
	ctxErr error
}

func (d *detachDriver) Turn(ctx context.Context, _ string) (*orchestrator.TurnOutcome, error) {
	close(d.started)
	// Return as soon as EITHER our execution context is cancelled (the bug: the
	// turn was tied to the dropped request) OR the test explicitly releases us
	// (the fix: the context survived the disconnect). Recording ctx.Err() then
	// distinguishes the two deterministically — no reliance on cancellation racing
	// a fixed read point.
	select {
	case <-ctx.Done():
	case <-d.release:
	}
	d.mu.Lock()
	d.ctxErr = ctx.Err()
	d.mu.Unlock()
	close(d.done)
	return &orchestrator.TurnOutcome{}, nil
}

// TestTurnStream_ClientDisconnectDoesNotCancelTurn is the regression guard for
// the VS Code "context canceled" report: closing the chat surface mid-turn drops
// the SSE connection (cancelling r.Context()), but the turn's EXECUTION context
// must survive so the turn lands a real outcome instead of failing with
// "context canceled" — which the room's on_error arc would otherwise bake into
// the persisted view, making every later reopen show "context canceled".
func TestTurnStream_ClientDisconnectDoesNotCancelTurn(t *testing.T) {
	drv := &detachDriver{
		started: make(chan struct{}),
		release: make(chan struct{}),
		done:    make(chan struct{}),
	}
	ts := httptest.NewServer(NewWithSource(stubSource{def: &app.AppDef{}}, WithDriver(drv)).Handler())
	defer ts.Close()

	ctx, cancel := context.WithCancel(context.Background())
	body, err := json.Marshal(map[string]any{"method": "turn", "input": "hi"})
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/rpc/turn-stream", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("content-type", "application/json")

	// The handler flushes headers before the turn runs, so Do() returns early with
	// an open body. Hold the connection by draining the body until cancel() tears
	// it down; errc then fires, proving the server saw the disconnect.
	errc := make(chan error, 1)
	go func() {
		resp, derr := http.DefaultClient.Do(req)
		if derr != nil {
			errc <- derr
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		errc <- nil
	}()

	<-drv.started // turn is in-flight on the server
	cancel()      // operator closes the surface: SSE connection drops
	<-errc        // client connection fully torn down (server's r.Context() cancelled)

	// If the turn's context was tied to the request (the bug), the disconnect
	// cancels it and Turn returns on its own. Wait a bounded window for that; if
	// it does NOT happen, the context survived (the fix) and we release the turn.
	select {
	case <-drv.done:
		// Turn returned without a release — only possible if ctx was cancelled.
	case <-time.After(2 * time.Second):
		close(drv.release)
		<-drv.done
	}

	drv.mu.Lock()
	ctxErr := drv.ctxErr
	drv.mu.Unlock()
	assert.NoError(t, ctxErr, "turn execution ctx must survive a client disconnect, got %v", ctxErr)
}
