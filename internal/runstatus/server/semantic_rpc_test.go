package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
)

// buildSemanticServer wires a journal-backed artifact at handle `media#1` whose
// on-disk path is mediaPath. When sidecar != "" it is written verbatim beside
// the media as `<stem>.semantic.json` (the pairing host.SemanticSidecarPath
// expects). Returns the test server.
func buildSemanticServer(t *testing.T, mediaName string, sidecar string) *httptest.Server {
	t.Helper()
	const sessionID = "sess-semantic"
	dir := t.TempDir()
	handle := "media#1"
	mediaPath := filepath.Join(dir, mediaName)
	require.NoError(t, os.WriteFile(mediaPath, []byte("fake-mp4-bytes"), 0o644))
	if sidecar != "" {
		stem := mediaPath[:len(mediaPath)-len(filepath.Ext(mediaPath))]
		require.NoError(t, os.WriteFile(stem+".semantic.json", []byte(sidecar), 0o644))
	}

	ms := journal.NewMemStore()
	jw := journal.NewMemWriter(ms)
	jr := journal.NewMemReader(ms)
	evtBody, err := json.Marshal(journal.ArtifactEvent{
		ID: handle, Kind: "video", Mime: "video/mp4", Label: "Deck",
		Path: mediaPath, Producer: "host.artifacts_dir", CreatedAt: time.Now(),
	})
	require.NoError(t, err)
	require.NoError(t, jw.Append(journal.Entry{
		Ts: time.Now(), Session: app.SessionID(sessionID), Turn: 1, Seq: 1,
		Kind: journal.KindArtifactEmitted, Body: json.RawMessage(evtBody),
	}))

	p := newStubProvider()
	p.mu.Lock()
	p.entries[sessionID] = server.Entry{
		Source:    &stubSource{header: runstatus.SessionHeader{SessionID: sessionID}, def: testDef()},
		Artifacts: &server.JournalArtifactResolver{Reader: jr, SID: app.SessionID(sessionID)},
	}
	p.mu.Unlock()

	ts := httptest.NewServer(server.NewMulti(p).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// TestArtifactSemantic_ReturnsCanonicalSidecar proves the runstatus.artifact.semantic
// RPC resolves an artifact handle to its on-disk path and returns the sibling
// `<name>.semantic.json` envelope verbatim — the producer-agnostic contract the
// web SemanticOverlay consumes (string refs round-tripped, never interpreted).
func TestArtifactSemantic_ReturnsCanonicalSidecar(t *testing.T) {
	t.Parallel()
	const sidecar = `{"plugin":"slidey","schema_version":1,"elements":[` +
		`{"ref":"1/card_0","label":"Scene 1 · card 0","selector":"[data-slidey-el='1/card_0']","bbox":[140,518,535,114],"t_ms":3200}]}`
	ts := buildSemanticServer(t, "deck.mp4", sidecar)

	var got struct {
		Plugin   string `json:"plugin"`
		Schema   int    `json:"schema_version"`
		Elements []struct {
			Ref   string `json:"ref"`
			Label string `json:"label"`
			Bbox  [4]int `json:"bbox"`
		} `json:"elements"`
	}
	rpcCall(t, ts, "runstatus.artifact.semantic", map[string]any{"session_id": "sess-semantic", "handle": "media#1"}, &got)

	assert.Equal(t, "slidey", got.Plugin)
	assert.Equal(t, 1, got.Schema)
	require.Len(t, got.Elements, 1)
	assert.Equal(t, "1/card_0", got.Elements[0].Ref, "opaque string ref round-tripped verbatim")
	assert.Equal(t, [4]int{140, 518, 535, 114}, got.Elements[0].Bbox)
}

// rpcResultRaw posts an RPC and returns the raw `result` bytes ("null" when the
// handler returned a nil result), asserting no JSON-RPC error.
func rpcResultRaw(t *testing.T, ts *httptest.Server, method string, params map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	resp, err := http.Post(ts.URL+"/rpc", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	var frame struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&frame))
	require.Nil(t, frame.Error, "rpc %s returned error: %+v", method, frame.Error)
	return string(frame.Result)
}

// TestArtifactSemantic_NoSidecarReturnsNull proves an artifact with no sibling
// sidecar resolves to null (not an error) so the annotator falls back to the
// dom_node/region picker — the graceful FrameResolver-style posture.
func TestArtifactSemantic_NoSidecarReturnsNull(t *testing.T) {
	t.Parallel()
	ts := buildSemanticServer(t, "deck.mp4", "")

	raw := rpcResultRaw(t, ts, "runstatus.artifact.semantic", map[string]any{"session_id": "sess-semantic", "handle": "media#1"})
	assert.Contains(t, []string{"null", ""}, raw, "no sidecar ⇒ nil result, annotator falls back to pixel/dom picking")
}

// TestArtifactSemantic_UnknownHandleReturnsNull proves an unknown handle is a
// graceful null, never a 500 — a stale handle just offers no semantic picks.
func TestArtifactSemantic_UnknownHandleReturnsNull(t *testing.T) {
	t.Parallel()
	ts := buildSemanticServer(t, "deck.mp4", `{"plugin":"slidey","schema_version":1,"elements":[{"ref":"0/title"}]}`)

	raw := rpcResultRaw(t, ts, "runstatus.artifact.semantic", map[string]any{"session_id": "sess-semantic", "handle": "nonexistent#9"})
	assert.Contains(t, []string{"null", ""}, raw)
}
