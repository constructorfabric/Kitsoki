package server_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/runstatus/server"
)

// turnStreamFrame mirrors the unexported server-side type for test decoding.
type turnStreamFrame struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Tool    string          `json:"tool"`
	Preview string          `json:"preview"`
	Result  json.RawMessage `json:"result"`
	Message string          `json:"message"`
}

// readTurnStreamFrames POST to /rpc/turn-stream and reads all SSE frames until
// the stream closes. Returns the list of decoded frames.
func readTurnStreamFrames(t *testing.T, ts *httptest.Server, body map[string]any) []turnStreamFrame {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)

	resp, err := http.Post(ts.URL+"/rpc/turn-stream", "application/json", strings.NewReader(string(b)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode, "expected 200 on turn-stream")
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	var frames []turnStreamFrame
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		raw := strings.TrimPrefix(line, "data: ")
		var f turnStreamFrame
		require.NoError(t, json.Unmarshal([]byte(raw), &f), "unmarshal frame: %s", raw)
		frames = append(frames, f)
	}
	return frames
}

// TestTurnStream_SubmitHappyPath drives the cloak fixture via the SSE endpoint
// (method=submit). No LLM is involved — SubmitDirect bypasses routing.
func TestTurnStream_SubmitHappyPath(t *testing.T) {
	t.Parallel()
	ts := buildLiveCloak(t)

	frames := readTurnStreamFrames(t, ts, map[string]any{
		"method": "submit",
		"intent": "go",
		"slots":  map[string]any{"direction": "south"},
	})

	require.NotEmpty(t, frames, "expected at least a done frame")
	last := frames[len(frames)-1]
	assert.Equal(t, "done", last.Type, "last frame must be done")
	assert.NotEmpty(t, last.Result, "done frame must carry a result")

	// Verify the result is a valid turnResult shape.
	var result turnResultWire
	require.NoError(t, json.Unmarshal(last.Result, &result))
	assert.Equal(t, "transitioned", result.Mode)
	assert.True(t, strings.HasPrefix(result.State, "bar"),
		"foyer --go south--> bar, got %q", result.State)
	assert.NotEmpty(t, result.View)

	// Any intermediate delta/tool frames must precede the done.
	for i, f := range frames[:len(frames)-1] {
		assert.NotEqual(t, "done", f.Type, "frame %d should not be done before the last", i)
		assert.NotEqual(t, "error", f.Type, "unexpected error frame at index %d: %s", i, f.Message)
	}
}

// TestTurnStream_SubmitRejected confirms that a rejected intent returns a done
// frame with mode=rejected (not an error frame) — same semantics as the RPC.
func TestTurnStream_SubmitRejected(t *testing.T) {
	t.Parallel()
	ts := buildLiveCloak(t)

	frames := readTurnStreamFrames(t, ts, map[string]any{
		"method": "submit",
		"intent": "hang_cloak",
	})

	require.NotEmpty(t, frames)
	last := frames[len(frames)-1]
	assert.Equal(t, "done", last.Type)

	var result turnResultWire
	require.NoError(t, json.Unmarshal(last.Result, &result))
	assert.Equal(t, "rejected", result.Mode)
	assert.NotEmpty(t, result.ErrorMessage)
}

// TestTurnStream_BadMethod confirms that an unknown method returns 400 before
// any streaming begins.
func TestTurnStream_BadMethod(t *testing.T) {
	t.Parallel()
	ts := buildLiveCloak(t)

	b, _ := json.Marshal(map[string]any{"method": "invalid"})
	resp, err := http.Post(ts.URL+"/rpc/turn-stream", "application/json", strings.NewReader(string(b)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestTurnStream_ReadOnlySurface confirms that a server with no Driver returns
// 403 instead of streaming.
func TestTurnStream_ReadOnlySurface(t *testing.T) {
	t.Parallel()
	def := testDef()
	_, live := openLiveSink(t, def, "ro-1", "main")
	ts := httptest.NewServer(server.NewWithSource(live).Handler()) // no WithDriver
	defer ts.Close()

	b, _ := json.Marshal(map[string]any{"method": "submit", "intent": "go"})
	resp, err := http.Post(ts.URL+"/rpc/turn-stream", "application/json", strings.NewReader(string(b)))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
}

// TestTurnStream_GetMethodNotAllowed confirms the endpoint rejects GET.
func TestTurnStream_GetMethodNotAllowed(t *testing.T) {
	t.Parallel()
	ts := buildLiveCloak(t)

	resp, err := http.Get(ts.URL + "/rpc/turn-stream")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
}
