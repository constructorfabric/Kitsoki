package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
)

// writeTrace writes the given JSONL lines to a temp trace file and returns its
// path. Each line is a slog-shaped trace record.
func writeTrace(t *testing.T, lines ...string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "run.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644))
	return path
}

// appendLine appends one JSONL line to an existing trace file (simulating a
// live run writing a new event).
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	require.NoError(t, err)
}

func testDef() *app.AppDef {
	return &app.AppDef{App: app.AppMeta{ID: "test-app", Version: "0.0.1"}}
}

// rpcCall posts a JSON-RPC request to ts and decodes result into out.
func rpcCall(t *testing.T, ts *httptest.Server, method string, params map[string]any, out any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	resp, err := http.Post(ts.URL+"/rpc", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()

	var frame struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&frame))
	require.Nil(t, frame.Error, "rpc %s returned error: %+v", method, frame.Error)
	if out != nil {
		require.NoError(t, json.Unmarshal(frame.Result, out))
	}
}

// twoTurnTrace returns a trace with two turns: turn 1 ends in "foyer", turn 2
// enters "hall".
func twoTurnTrace(t *testing.T) string {
	return writeTrace(t,
		`{"time":"2026-05-28T10:00:00Z","level":"INFO","msg":"turn.started","session_id":"s-1","turn":1,"state_path":"foyer"}`,
		`{"time":"2026-05-28T10:00:01Z","level":"INFO","msg":"state.entered","session_id":"s-1","turn":1,"state_path":"foyer"}`,
		`{"time":"2026-05-28T10:00:02Z","level":"INFO","msg":"turn.started","session_id":"s-1","turn":2,"state_path":"hall"}`,
		`{"time":"2026-05-28T10:00:03Z","level":"INFO","msg":"state.entered","session_id":"s-1","turn":2,"state_path":"hall"}`,
	)
}

func TestServer_SessionGet(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(twoTurnTrace(t), testDef()).Handler())
	defer ts.Close()

	var hdr runstatus.SessionHeader
	rpcCall(t, ts, "runstatus.session.get", map[string]any{"session_id": "s-1"}, &hdr)

	assert.Equal(t, "s-1", hdr.SessionID)
	assert.Equal(t, "test-app", hdr.AppID)
	assert.Equal(t, "hall", hdr.CurrentState) // last event's state_path
	assert.Equal(t, 2, hdr.Turn)
}

func TestServer_SessionTrace(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(twoTurnTrace(t), testDef()).Handler())
	defer ts.Close()

	var full struct {
		Events   []runstatus.TraceEvent `json:"events"`
		LastTurn int                    `json:"last_turn"`
	}
	rpcCall(t, ts, "runstatus.session.trace", map[string]any{"session_id": "s-1"}, &full)
	assert.Len(t, full.Events, 4)
	assert.Equal(t, 2, full.LastTurn)
	// state_path survives (full-fidelity JSONL path), unlike the store path.
	assert.Equal(t, "foyer", full.Events[0].StatePath)

	var since struct {
		Events []runstatus.TraceEvent `json:"events"`
	}
	rpcCall(t, ts, "runstatus.session.trace", map[string]any{"session_id": "s-1", "since_turn": 2}, &since)
	assert.Len(t, since.Events, 2)
	for _, ev := range since.Events {
		assert.Equal(t, 2, ev.Turn)
	}
}

func TestServer_AppAndMermaid(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(twoTurnTrace(t), testDef()).Handler())
	defer ts.Close()

	var appOut app.AppDef
	rpcCall(t, ts, "runstatus.session.app", map[string]any{"session_id": "s-1"}, &appOut)
	assert.Equal(t, "test-app", appOut.App.ID)

	var mer runstatus.MermaidSnapshot
	rpcCall(t, ts, "runstatus.session.mermaid", map[string]any{"session_id": "s-1"}, &mer)
	assert.NotEmpty(t, mer.Source)
}

func TestServer_SessionsList_EmptyUntilEvents(t *testing.T) {
	t.Parallel()
	// Point at a not-yet-created trace: list is empty, no error.
	missing := filepath.Join(t.TempDir(), "later.jsonl")
	ts := httptest.NewServer(server.New(missing, testDef()).Handler())
	defer ts.Close()

	var list []runstatus.SessionHeader
	rpcCall(t, ts, "runstatus.sessions.list", map[string]any{}, &list)
	assert.Empty(t, list)
}

func TestServer_UnknownMethod(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(twoTurnTrace(t), testDef()).Handler())
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"runstatus.bogus","params":{}}`
	resp, err := http.Post(ts.URL+"/rpc", "application/json", strings.NewReader(body))
	require.NoError(t, err)
	defer resp.Body.Close()
	var frame struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&frame))
	require.NotNil(t, frame.Error)
	assert.Equal(t, -32601, frame.Error.Code)
}

func TestServer_FileReadAllowsMarkdownAndDiffArtifacts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	files := map[string]string{
		"brief.md":    "# Brief\n",
		"change.diff": "diff --git a/a b/a\n",
		"fix.patch":   "diff --git a/b b/b\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope"), 0o644))

	ts := httptest.NewServer(server.New(twoTurnTrace(t), testDef()).Handler())
	defer ts.Close()

	for name, want := range files {
		var out struct {
			Content string `json:"content"`
		}
		rpcCall(t, ts, "runstatus.file.read", map[string]any{"path": filepath.Join(dir, name)}, &out)
		assert.Equal(t, want, out.Content)
	}

	_, msg := rpcCallExpectError(t, ts, "runstatus.file.read", map[string]any{"path": filepath.Join(dir, "secret.txt")})
	assert.Contains(t, msg, "only .md, .diff, and .patch files are served")
}

// TestServer_SubscribeAndStream verifies the subscribe → SSE flow: a
// subscription streams only events appended *after* subscribe, as
// runstatus.event notifications, preserving full-fidelity fields.
func TestServer_SubscribeAndStream(t *testing.T) {
	t.Parallel()
	trace := twoTurnTrace(t)
	srv := server.New(trace, testDef(), server.WithPollInterval(20*time.Millisecond))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var sub struct {
		SubscriptionID string `json:"subscription_id"`
	}
	rpcCall(t, ts, "runstatus.session.subscribe", map[string]any{"session_id": "s-1"}, &sub)
	require.NotEmpty(t, sub.SubscriptionID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/rpc/events?subscription_id="+sub.SubscriptionID, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Read SSE frames in a goroutine until we see the turn-3 event.
	type result struct {
		ev    runstatus.TraceEvent
		found bool
	}
	resCh := make(chan result, 1)
	var once sync.Once
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			data, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var frame struct {
				Method string `json:"method"`
				Params struct {
					SubscriptionID string               `json:"subscription_id"`
					Event          runstatus.TraceEvent `json:"event"`
				} `json:"params"`
			}
			if json.Unmarshal([]byte(data), &frame) != nil {
				continue
			}
			if frame.Params.Event.Turn == 3 {
				once.Do(func() { resCh <- result{frame.Params.Event, true} })
				return
			}
		}
		once.Do(func() { resCh <- result{found: false} })
	}()

	// Append a turn-3 event after subscribing; it must arrive on the stream.
	appendLine(t, trace,
		`{"time":"2026-05-28T10:01:00Z","level":"INFO","msg":"turn.started","session_id":"s-1","turn":3,"state_path":"exit"}`)

	select {
	case res := <-resCh:
		require.True(t, res.found, "expected to receive the turn-3 event over SSE")
		assert.Equal(t, "turn.started", res.ev.Msg)
		assert.Equal(t, "exit", res.ev.StatePath)
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE event")
	}
}

// ── /artifact/{id} route tests ───────────────────────────────────────────────

// stubArtifactResolver is a minimal ArtifactResolver that maps handle IDs to
// (path, mime) pairs. It is used to wire the server's artifact route without
// touching a real journal or orchestrator.
type stubArtifactResolver struct {
	entries map[string]artifactEntry
}

type artifactEntry struct {
	path string
	mime string
}

func newStubResolver(entries map[string]artifactEntry) *stubArtifactResolver {
	return &stubArtifactResolver{entries: entries}
}

func (r *stubArtifactResolver) LookupArtifact(id string) (path, mime string, ok bool) {
	e, ok := r.entries[id]
	return e.path, e.mime, ok
}

// buildArtifactServer spins up a test server whose single-entry provider has
// an ArtifactResolver stub. It returns the httptest.Server and the absolute
// path of the fixture file written into a temp directory.
func buildArtifactServer(t *testing.T, handle, mimeType string, content []byte, ext string) (*httptest.Server, string) {
	t.Helper()
	dir := t.TempDir()
	fname := handle + ext
	fpath := filepath.Join(dir, fname)
	require.NoError(t, os.WriteFile(fpath, content, 0o644))

	resolver := newStubResolver(map[string]artifactEntry{
		handle: {path: fpath, mime: mimeType},
	})

	// Build a server via NewMulti with a stub provider that has the resolver.
	p := newStubProvider()
	p.mu.Lock()
	p.entries["sess-art"] = server.Entry{
		Source:    &stubSource{header: runstatus.SessionHeader{SessionID: "sess-art"}, def: testDef()},
		Artifacts: resolver,
	}
	p.mu.Unlock()

	ts := httptest.NewServer(server.NewMulti(p).Handler())
	t.Cleanup(ts.Close)
	return ts, fpath
}

// urlEncodeHandle percent-encodes the '#' in a handle ID so the HTTP client
// does not treat it as a URL fragment separator. The server strips the
// /artifact/ prefix from the raw URL path, so the encoded form must be used
// in the path.
func urlEncodeHandle(h string) string {
	return strings.ReplaceAll(h, "#", "%23")
}

// TestArtifact_ServesFixtureWithContentType proves GET /artifact/{id} returns
// 200 with the correct Content-Type header and the exact file content.
func TestArtifact_ServesFixtureWithContentType(t *testing.T) {
	t.Parallel()
	handle := "demo_video#ab12cd34"
	content := []byte("FAKEVIDEO")
	ts, _ := buildArtifactServer(t, handle, "video/mp4", content, ".mp4")

	resp, err := http.Get(ts.URL + "/artifact/" + urlEncodeHandle(handle))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "video/mp4", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, content, body)
}

// TestArtifact_HonorsRangeRequest proves the server supports HTTP Range
// requests (needed for video seeking in the browser). It requests the last
// 4 bytes and expects a 206 Partial Content response with the correct slice.
func TestArtifact_HonorsRangeRequest(t *testing.T) {
	t.Parallel()
	handle := "range_video#00000001"
	content := []byte("ABCDEFGHIJ") // 10 bytes
	ts, _ := buildArtifactServer(t, handle, "video/mp4", content, ".mp4")

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/artifact/"+urlEncodeHandle(handle), nil)
	require.NoError(t, err)
	req.Header.Set("Range", "bytes=6-9") // 4 bytes: G H I J

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusPartialContent, resp.StatusCode, "Range request should return 206")
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, []byte("GHIJ"), body, "Range slice must match bytes 6-9 of the fixture")
}

// TestArtifact_UnknownIDReturns404 proves an unknown handle returns 404
// without leaking path information.
func TestArtifact_UnknownIDReturns404(t *testing.T) {
	t.Parallel()
	handle := "known_video#aaaabbbb"
	ts, _ := buildArtifactServer(t, handle, "video/mp4", []byte("X"), ".mp4")

	resp, err := http.Get(ts.URL + "/artifact/no_such_handle%23ffffffff")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestArtifact_PathEscapeGuard proves that URL paths containing encoded slashes
// (which could smuggle ".." path components) after the /artifact/ prefix are
// rejected with 404 — the server must not serve files outside the artifacts
// root. Note: literal "../.." in URLs is cleaned by net/http's ServeMux before
// reaching the handler; the meaningful attack vector is percent-encoded slashes
// (%2F) that bypass the mux's clean step but still contain "/" after decoding.
func TestArtifact_PathEscapeGuard(t *testing.T) {
	t.Parallel()
	ts, _ := buildArtifactServer(t, "legit#00000001", "video/mp4", []byte("X"), ".mp4")

	// These all contain "/" in the decoded id segment — the server rejects them.
	for _, rawSuffix := range []string{
		"..%2F..%2Fetc%2Fpasswd",          // ../ decoded by server
		"legit%2300000001%2Fextra",         // slash after handle
		"legit%2300000001%2F..%2Fsecret",   // slash + traversal after handle
	} {
		resp, err := http.Get(ts.URL + "/artifact/" + rawSuffix)
		require.NoError(t, err)
		_ = resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode,
			"path %q should be rejected as 404", rawSuffix)
	}
}
