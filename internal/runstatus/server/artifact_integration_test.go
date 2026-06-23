package server_test

// artifact_integration_test.go exercises the full emit→serve path:
//
//  1. A real journal.ArtifactEvent is written via the in-memory journal writer
//     (the same KindArtifactEmitted entry the host.artifacts_dir handler emits).
//  2. A JournalArtifactResolver is wired to a server via NewMulti + stubProvider.
//  3. GET /artifact/{id} is called and the response is validated:
//     - 200 status + correct Content-Type (image/png) + body matches file bytes.
//  4. GET /artifact/unknown returns 404.
//
// No LLM, no subprocess, no real filesystem side-effects beyond os.TempDir().

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
)

// minimalPNG is a valid 1×1 pixel PNG file encoded as raw bytes.
// Embedded as a literal so the test has zero network/file dependencies.
// The bytes encode a 1×1 RGB PNG with a red pixel.
var minimalPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, // PNG signature
	// IHDR chunk: 1×1, 8-bit RGB
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xde,
	// IDAT chunk: deflate-compressed pixel data
	0x00, 0x00, 0x00, 0x0c, 0x49, 0x44, 0x41, 0x54,
	0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0x00, 0x00,
	0x00, 0x02, 0x00, 0x01, 0xe2, 0x21, 0xbc, 0x33,
	// IEND chunk
	0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44,
	0xae, 0x42, 0x60, 0x82,
}

// buildJournalArtifactServer builds a test HTTP server that serves
// GET /artifact/{id} backed by a real JournalArtifactResolver.
//
// It writes content to a temp file, appends a KindArtifactEmitted entry to
// an in-memory journal (the same shape the host.artifacts_dir transport emits),
// and wires a JournalArtifactResolver to the server via a stubProvider entry.
//
// Returns the httptest.Server and the handle ID recorded in the journal.
func buildJournalArtifactServer(t *testing.T, content []byte, mime string) (ts *httptest.Server, handle string) {
	return buildJournalArtifactServerPoster(t, content, mime, nil)
}

// buildJournalArtifactServerPoster is buildJournalArtifactServer plus an optional
// sibling `<stem>.poster.png` still written beside the media (the annotator's
// slidey backdrop). When posterContent is nil no poster is written, so a
// `/artifact/<id>/poster` request 404s.
func buildJournalArtifactServerPoster(t *testing.T, content []byte, mime string, posterContent []byte) (ts *httptest.Server, handle string) {
	t.Helper()

	const sessionID = "sess-journal-art"

	// ── 1. Write the artifact file to a temp dir ─────────────────────────────
	dir := t.TempDir()
	handle = "test_image#ab12cd34"
	fpath := filepath.Join(dir, "test_image.png")
	require.NoError(t, os.WriteFile(fpath, content, 0o644))
	if posterContent != nil {
		// The sibling poster: same stem, `.poster.png` (host.PosterSidecarPath).
		require.NoError(t, os.WriteFile(filepath.Join(dir, "test_image.poster.png"), posterContent, 0o644))
	}

	// ── 2. Write artifact.emitted to an in-memory journal ────────────────────
	ms := journal.NewMemStore()
	jw := journal.NewMemWriter(ms)
	jr := journal.NewMemReader(ms)

	evt := journal.ArtifactEvent{
		ID:        handle,
		Kind:      "image",
		Mime:      mime,
		Label:     "Test Image",
		Path:      fpath,
		Producer:  "host.artifacts_dir",
		SizeBytes: int64(len(content)),
		CreatedAt: time.Now(),
	}
	evtBody, err := json.Marshal(evt)
	require.NoError(t, err)

	require.NoError(t, jw.Append(journal.Entry{
		Ts:      time.Now(),
		Session: app.SessionID(sessionID),
		Turn:    1,
		Seq:     1,
		Kind:    journal.KindArtifactEmitted,
		Body:    json.RawMessage(evtBody),
	}))

	// ── 3. Wire a real JournalArtifactResolver into the server ────────────────
	resolver := &server.JournalArtifactResolver{
		Reader: jr,
		SID:    app.SessionID(sessionID),
	}

	p := newStubProvider()
	p.mu.Lock()
	p.entries[sessionID] = server.Entry{
		Source: &stubSource{
			header: runstatus.SessionHeader{SessionID: sessionID},
			def:    testDef(),
		},
		Artifacts: resolver,
	}
	p.mu.Unlock()

	ts = httptest.NewServer(server.NewMulti(p).Handler())
	t.Cleanup(ts.Close)
	return ts, handle
}

// TestArtifactIntegration_EmitAndServe is the full emit→serve integration test.
// It proves that a KindArtifactEmitted entry written to the journal is served
// correctly by the /artifact/{id} route via the real JournalArtifactResolver:
// 200 status, correct Content-Type, and exact file bytes in the response body.
func TestArtifactIntegration_EmitAndServe(t *testing.T) {
	t.Parallel()

	ts, handle := buildJournalArtifactServer(t, minimalPNG, "image/png")

	resp, err := http.Get(ts.URL + "/artifact/" + urlEncodeHandle(handle))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, minimalPNG, body, "response body must exactly match the emitted artifact bytes")
}

// TestArtifactIntegration_UnknownHandleReturns404 proves that an unknown handle
// returns 404 when the real JournalArtifactResolver (not the stub) is wired.
func TestArtifactIntegration_UnknownHandleReturns404(t *testing.T) {
	t.Parallel()

	ts, _ := buildJournalArtifactServer(t, minimalPNG, "image/png")

	resp, err := http.Get(ts.URL + "/artifact/no_such_handle%23deadbeef")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestArtifactIntegration_PosterServesSibling proves GET /artifact/<id>/poster
// serves the sibling `<stem>.poster.png` beside the media (the annotator's
// slidey backdrop) keyed by the SAME media handle — as image/png, exact bytes.
func TestArtifactIntegration_PosterServesSibling(t *testing.T) {
	t.Parallel()

	// A distinct poster body so the test can tell it apart from the media bytes.
	poster := append([]byte(nil), minimalPNG...)
	poster[len(poster)-1] ^= 0xff
	ts, handle := buildJournalArtifactServerPoster(t, minimalPNG, "video/mp4", poster)

	resp, err := http.Get(ts.URL + "/artifact/" + urlEncodeHandle(handle) + "/poster")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "image/png", resp.Header.Get("Content-Type"))
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, poster, body, "poster route must serve the sibling .poster.png bytes")
}

// TestArtifactIntegration_PosterMissingReturns404 proves a poster request for a
// media with no sibling poster 404s (the annotator then has no still backdrop).
func TestArtifactIntegration_PosterMissingReturns404(t *testing.T) {
	t.Parallel()

	ts, handle := buildJournalArtifactServer(t, minimalPNG, "video/mp4") // no poster
	resp, err := http.Get(ts.URL + "/artifact/" + urlEncodeHandle(handle) + "/poster")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
