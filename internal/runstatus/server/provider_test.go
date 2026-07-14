package server_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/runstatus"
	"kitsoki/internal/runstatus/server"
	"kitsoki/internal/store"
)

// stubSource is a minimal in-memory [server.Source] for the routing tests: it
// returns a fixed snapshot/AppDef and never touches a file. The multi-session
// dispatch path only needs Snapshot/Events/AppDef.
type stubSource struct {
	header runstatus.SessionHeader
	def    *app.AppDef
	events []runstatus.TraceEvent
}

func (s *stubSource) Snapshot() (runstatus.Snapshot, error) {
	return runstatus.Snapshot{Session: s.header, App: s.def, Events: s.events}, nil
}
func (s *stubSource) Events() ([]runstatus.TraceEvent, error) { return s.events, nil }
func (s *stubSource) AppDef() *app.AppDef                     { return s.def }

// stubProvider is a [server.SessionProvider] built from func fields, so each
// routing test wires only the behaviour it asserts. It is the deterministic
// stand-in the test_plan calls for — no orchestrator, no LLM.
type stubProvider struct {
	mu sync.Mutex

	entries  map[string]server.Entry
	stories  []server.StoryHeader
	newFn    func(ctx context.Context, storyPath string) (string, error)
	seededFn func(ctx context.Context, storyPath string, initialWorld map[string]any) (string, error)
	reloadFn func(ctx context.Context, sessionID string) (bool, error)
	rescanFn func() ([]server.StoryHeader, error)
}

func newStubProvider() *stubProvider {
	return &stubProvider{entries: map[string]server.Entry{}}
}

func (p *stubProvider) Get(sessionID string) (server.Entry, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[sessionID]
	return e, ok
}

func (p *stubProvider) List() []runstatus.SessionHeader {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]runstatus.SessionHeader, 0, len(p.entries))
	for _, e := range p.entries {
		snap, _ := e.Source.Snapshot()
		out = append(out, snap.Session)
	}
	return out
}

func (p *stubProvider) NewSession(ctx context.Context, storyPath string) (string, error) {
	return p.newFn(ctx, storyPath)
}

func (p *stubProvider) NewSessionSeeded(ctx context.Context, storyPath string, initialWorld map[string]any) (string, error) {
	if p.seededFn != nil {
		return p.seededFn(ctx, storyPath, initialWorld)
	}
	return p.newFn(ctx, storyPath)
}

func (p *stubProvider) Reload(ctx context.Context, sessionID string) (bool, error) {
	return p.reloadFn(ctx, sessionID)
}

// Staleness reports the session is never stale — no test exercises the
// on-disk app.yaml drift check, so the stub is a no-op.
func (p *stubProvider) Staleness(context.Context, string) (bool, string, error) {
	return false, "", nil
}

func (p *stubProvider) ListStories() []server.StoryHeader {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stories
}

func (p *stubProvider) Rescan() ([]server.StoryHeader, error) { return p.rescanFn() }

// putEntry registers a fully caller-built entry (live source, real driver)
// under sessionID — for tests that need an actual drivable session behind
// the provider (e.g. the materialize web-session path).
func (p *stubProvider) putEntry(sessionID string, entry server.Entry) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[sessionID] = entry
}

// put registers a routable entry under sessionID with a stub source carrying
// the given header + def.
func (p *stubProvider) put(sessionID string, header runstatus.SessionHeader, def *app.AppDef) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.entries[sessionID] = server.Entry{Source: &stubSource{header: header, def: def}}
}

// TestMulti_NewSessionRoundTrip proves session.new returns an id that
// subsequent session-routed reads resolve to: the stub provider creates an
// entry on NewSession, and session.get / session.app then route by that id.
func TestMulti_NewSessionRoundTrip(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	p.newFn = func(_ context.Context, storyPath string) (string, error) {
		assert.Equal(t, "/abs/story/app.yaml", storyPath)
		def := &app.AppDef{App: app.AppMeta{ID: "story-app", Version: "1.0.0"}}
		p.put("sess-new", runstatus.SessionHeader{SessionID: "sess-new", AppID: "story-app", CurrentState: "start"}, def)
		return "sess-new", nil
	}

	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	var created struct {
		SessionID string `json:"session_id"`
	}
	rpcCall(t, ts, "runstatus.session.new", map[string]any{"story_path": "/abs/story/app.yaml"}, &created)
	require.Equal(t, "sess-new", created.SessionID)

	var hdr runstatus.SessionHeader
	rpcCall(t, ts, "runstatus.session.get", map[string]any{"session_id": created.SessionID}, &hdr)
	assert.Equal(t, "sess-new", hdr.SessionID)
	assert.Equal(t, "story-app", hdr.AppID)

	var appOut app.AppDef
	rpcCall(t, ts, "runstatus.session.app", map[string]any{"session_id": created.SessionID}, &appOut)
	assert.Equal(t, "story-app", appOut.App.ID)
}

func TestMulti_NewSessionInitialWorld(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	p.seededFn = func(_ context.Context, storyPath string, initialWorld map[string]any) (string, error) {
		assert.Equal(t, "/abs/story/app.yaml", storyPath)
		assert.Equal(t, "B-123", initialWorld["ticket_id"])
		assert.Equal(t, "no-gate", initialWorld["oversight_mode"])
		def := &app.AppDef{App: app.AppMeta{ID: "bugfix", Version: "1.0.0"}}
		p.put("sess-seeded", runstatus.SessionHeader{SessionID: "sess-seeded", AppID: "bugfix", CurrentState: "idle"}, def)
		return "sess-seeded", nil
	}

	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	var created struct {
		SessionID string `json:"session_id"`
	}
	rpcCall(t, ts, "runstatus.session.new", map[string]any{
		"story_path": "/abs/story/app.yaml",
		"initial_world": map[string]any{
			"ticket_id":      "B-123",
			"oversight_mode": "no-gate",
		},
	}, &created)
	require.Equal(t, "sess-seeded", created.SessionID)
}

// TestMulti_UnknownSession proves a session-routed RPC with an id the provider
// does not know returns a structured not-found error, never a nil-deref.
func TestMulti_UnknownSession(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	code, msg := rpcCallExpectError(t, ts, "runstatus.session.get", map[string]any{"session_id": "ghost"})
	assert.Equal(t, -32002, code, "unknown session_id should return codeNotFound")
	assert.Contains(t, msg, "ghost")
}

// TestMulti_SessionReload proves session.reload returns prev_state_exists
// mirroring Orchestrator.Reload semantics, for both a valid reload (true) and a
// reload where the current state was removed (false).
func TestMulti_SessionReload(t *testing.T) {
	t.Parallel()

	t.Run("prev state still exists", func(t *testing.T) {
		t.Parallel()
		p := newStubProvider()
		p.put("s1", runstatus.SessionHeader{SessionID: "s1"}, testDef())
		p.reloadFn = func(_ context.Context, sid string) (bool, error) {
			assert.Equal(t, "s1", sid)
			return true, nil
		}
		ts := httptest.NewServer(server.NewMulti(p).Handler())
		defer ts.Close()

		var res struct {
			OK              bool `json:"ok"`
			PrevStateExists bool `json:"prev_state_exists"`
		}
		rpcCall(t, ts, "runstatus.session.reload", map[string]any{"session_id": "s1"}, &res)
		assert.True(t, res.OK)
		assert.True(t, res.PrevStateExists)
	})

	t.Run("prev state removed", func(t *testing.T) {
		t.Parallel()
		p := newStubProvider()
		p.put("s2", runstatus.SessionHeader{SessionID: "s2"}, testDef())
		p.reloadFn = func(_ context.Context, _ string) (bool, error) { return false, nil }
		ts := httptest.NewServer(server.NewMulti(p).Handler())
		defer ts.Close()

		var res struct {
			OK              bool `json:"ok"`
			PrevStateExists bool `json:"prev_state_exists"`
		}
		rpcCall(t, ts, "runstatus.session.reload", map[string]any{"session_id": "s2"}, &res)
		assert.True(t, res.OK)
		assert.False(t, res.PrevStateExists, "removed current state must report prev_state_exists:false")
	})
}

// TestMulti_StoriesRescan proves stories.rescan reflects a story added between
// calls: the provider's catalogue grows and stories.list/rescan return the new
// entry.
func TestMulti_StoriesRescan(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	p.stories = []server.StoryHeader{{Path: "/a/app.yaml", AppID: "a", Title: "A"}}
	p.rescanFn = func() ([]server.StoryHeader, error) {
		p.mu.Lock()
		p.stories = append(p.stories, server.StoryHeader{Path: "/b/app.yaml", AppID: "b", Title: "B"})
		out := p.stories
		p.mu.Unlock()
		return out, nil
	}
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	var before []server.StoryHeader
	rpcCall(t, ts, "runstatus.stories.list", map[string]any{}, &before)
	require.Len(t, before, 1)
	assert.Equal(t, "a", before[0].AppID)

	var after []server.StoryHeader
	rpcCall(t, ts, "runstatus.stories.rescan", map[string]any{}, &after)
	require.Len(t, after, 2, "rescan should surface the newly-added story")
	assert.Equal(t, "b", after[1].AppID)

	// stories.list now reflects the rescanned catalogue.
	var relisted []server.StoryHeader
	rpcCall(t, ts, "runstatus.stories.list", map[string]any{}, &relisted)
	assert.Len(t, relisted, 2)
}

// TestMulti_SessionsList proves sessions.list returns the provider's live
// sessions (not a single trace's snap.Session) — one header per live entry.
func TestMulti_SessionsList(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	p.put("s1", runstatus.SessionHeader{SessionID: "s1", AppID: "a"}, testDef())
	p.put("s2", runstatus.SessionHeader{SessionID: "s2", AppID: "b"}, testDef())
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	var list []runstatus.SessionHeader
	rpcCall(t, ts, "runstatus.sessions.list", map[string]any{}, &list)
	require.Len(t, list, 2)
	ids := map[string]bool{}
	for _, h := range list {
		ids[h.SessionID] = true
	}
	assert.True(t, ids["s1"] && ids["s2"], "both live sessions listed, got %+v", list)
}

// TestMulti_NewSessionInvalidStory proves an invalid story surfaces as a
// structured error (so the UI can show it before navigating), not a panic.
func TestMulti_NewSessionInvalidStory(t *testing.T) {
	t.Parallel()
	p := newStubProvider()
	p.newFn = func(context.Context, string) (string, error) {
		return "", assert.AnError
	}
	ts := httptest.NewServer(server.NewMulti(p).Handler())
	defer ts.Close()

	code, _ := rpcCallExpectError(t, ts, "runstatus.session.new", map[string]any{"story_path": "/bad.yaml"})
	assert.Equal(t, -32000, code, "an invalid story should return a structured server error")
}

// TestMulti_SSERoutesBySession proves the per-session SSE path: a subscription
// captures its session_id and the poller resolves THAT session's live Source
// each tick, so an event appended to the subscribed session arrives — and an
// event appended to a different session does not bleed into the stream. This is
// the routing analogue of TestServer_SubscribeAndStream.
func TestMulti_SSERoutesBySession(t *testing.T) {
	t.Parallel()
	def := testDef()
	_, liveA := openLiveSink(t, def, "sa", "main")
	_, liveB := openLiveSink(t, def, "sb", "main")

	p := newStubProvider()
	p.mu.Lock()
	p.entries["sa"] = server.Entry{Source: liveA}
	p.entries["sb"] = server.Entry{Source: liveB}
	p.mu.Unlock()

	srv := server.NewMulti(p, server.WithPollInterval(20*time.Millisecond))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var sub struct {
		SubscriptionID string `json:"subscription_id"`
	}
	rpcCall(t, ts, "runstatus.session.subscribe", map[string]any{"session_id": "sa"}, &sub)
	require.NotEmpty(t, sub.SubscriptionID)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		ts.URL+"/rpc/events?subscription_id="+sub.SubscriptionID, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	type result struct {
		state string
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
				Params struct {
					Event runstatus.TraceEvent `json:"event"`
				} `json:"params"`
			}
			if json.Unmarshal([]byte(data), &frame) != nil {
				continue
			}
			once.Do(func() { resCh <- result{frame.Params.Event.StatePath, true} })
			return
		}
		once.Do(func() { resCh <- result{found: false} })
	}()

	// Append to session B FIRST — it must NOT appear on session A's stream — then
	// to A, which must.
	require.NoError(t, liveB.Append(store.Event{
		Turn: 1, Kind: store.StateEntered, StatePath: "wrong-session",
		Payload: json.RawMessage(`{}`),
	}))
	require.NoError(t, liveA.Append(store.Event{
		Turn: 1, Kind: store.StateEntered, StatePath: "right-session",
		Payload: json.RawMessage(`{}`),
	}))

	select {
	case res := <-resCh:
		require.True(t, res.found, "expected an event on session A's stream")
		assert.Equal(t, "right-session", res.state, "stream must carry only the subscribed session's events")
	case <-ctx.Done():
		t.Fatal("timed out waiting for SSE event")
	}
}

// TestReadOnlySurface_LifecycleUnsupported proves the single-entry adapter
// behind server.New / NewWithSource reports a structured codeReadOnly for the
// lifecycle RPCs (no orchestrator/registry), never nil-derefing, while reads
// keep working.
func TestReadOnlySurface_LifecycleUnsupported(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(server.New(twoTurnTrace(t), testDef()).Handler())
	defer ts.Close()

	for _, m := range []struct {
		method string
		params map[string]any
	}{
		{"runstatus.session.new", map[string]any{"story_path": "/x.yaml"}},
		{"runstatus.session.reload", map[string]any{"session_id": "s-1"}},
		{"runstatus.stories.rescan", map[string]any{}},
	} {
		code, msg := rpcCallExpectError(t, ts, m.method, m.params)
		assert.Equal(t, -32001, code, "%s should report codeReadOnly", m.method)
		assert.Contains(t, msg, "read-only", "%s message should explain the surface is read-only", m.method)
	}

	// stories.list is a tolerant read on the read-only surface: empty, not an error.
	var stories []server.StoryHeader
	rpcCall(t, ts, "runstatus.stories.list", map[string]any{}, &stories)
	assert.Empty(t, stories)

	// Reads still route to the single entry (session_id ignored).
	var hdr runstatus.SessionHeader
	rpcCall(t, ts, "runstatus.session.get", map[string]any{"session_id": "s-1"}, &hdr)
	assert.Equal(t, "s-1", hdr.SessionID)
}

// ── JournalArtifactResolver unit tests ───────────────────────────────────────

// makeArtifactEntry creates a real journal.Entry for an artifact.emitted event
// using the canonical ArtifactEvent body shape.
func makeArtifactEntry(t *testing.T, sid app.SessionID, ev journal.ArtifactEvent) journal.Entry {
	t.Helper()
	body, err := json.Marshal(ev)
	require.NoError(t, err)
	return journal.Entry{
		Session: sid,
		Turn:    1,
		Seq:     1,
		Kind:    journal.KindArtifactEmitted,
		Body:    json.RawMessage(body),
	}
}

// TestJournalArtifactResolver_Found proves LookupArtifact returns the correct
// path and mime when the journal contains a matching artifact.emitted entry.
func TestJournalArtifactResolver_Found(t *testing.T) {
	t.Parallel()
	sid := app.SessionID("sess-jar-1")
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	// Write a non-artifact typed entry first (should be skipped).
	otherBody, _ := json.Marshal(map[string]string{"foo": "bar"})
	require.NoError(t, w.Append(journal.Entry{
		Session: sid, Turn: 1, Seq: 0,
		Kind: journal.KindHostInvoked,
		Body: json.RawMessage(otherBody),
	}))

	// Write the target artifact entry.
	require.NoError(t, w.Append(makeArtifactEntry(t, sid, journal.ArtifactEvent{
		ID:       "clip#ab12cd34",
		Kind:     "video",
		Mime:     "video/mp4",
		Label:    "Demo clip",
		Path:     "/artifacts/clip.mp4",
		Producer: "host.artifacts_dir",
	})))

	resolver := &server.JournalArtifactResolver{Reader: r, SID: sid}
	path, mime, ok := resolver.LookupArtifact("clip#ab12cd34")

	require.True(t, ok, "expected artifact to be found")
	assert.Equal(t, "/artifacts/clip.mp4", path)
	assert.Equal(t, "video/mp4", mime)
}

// TestJournalArtifactResolver_SkipsWrongKind proves that non-artifact.emitted
// typed entries (e.g. host.invoked) are skipped and do not interfere with
// the lookup result.
func TestJournalArtifactResolver_SkipsWrongKind(t *testing.T) {
	t.Parallel()
	sid := app.SessionID("sess-jar-2")
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	// Only write entries of a different kind — no artifact.emitted at all.
	otherBody, _ := json.Marshal(map[string]string{"verb": "decide"})
	for i := range 3 {
		require.NoError(t, w.Append(journal.Entry{
			Session: sid, Turn: 1, Seq: i,
			Kind: journal.KindHostReturned,
			Body: json.RawMessage(otherBody),
		}))
	}

	resolver := &server.JournalArtifactResolver{Reader: r, SID: sid}
	path, mime, ok := resolver.LookupArtifact("clip#anything")

	assert.False(t, ok, "no artifact.emitted entries → should not be found")
	assert.Empty(t, path)
	assert.Empty(t, mime)
}

// TestJournalArtifactResolver_MalformedBodySkipped proves that an
// artifact.emitted entry whose body is not valid JSON is silently skipped
// (the resolver continues scanning) and a subsequent valid entry is still found.
func TestJournalArtifactResolver_MalformedBodySkipped(t *testing.T) {
	t.Parallel()
	sid := app.SessionID("sess-jar-3")
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	// Malformed artifact entry (invalid JSON body) — must be skipped.
	require.NoError(t, w.Append(journal.Entry{
		Session: sid, Turn: 1, Seq: 0,
		Kind: journal.KindArtifactEmitted,
		Body: json.RawMessage(`{not valid json`),
	}))

	// Valid artifact entry after the malformed one — must still be found.
	require.NoError(t, w.Append(makeArtifactEntry(t, sid, journal.ArtifactEvent{
		ID:       "img#deadbeef",
		Kind:     "image",
		Mime:     "image/png",
		Path:     "/artifacts/shot.png",
		Producer: "host.artifacts_dir",
	})))

	resolver := &server.JournalArtifactResolver{Reader: r, SID: sid}
	path, mime, ok := resolver.LookupArtifact("img#deadbeef")

	require.True(t, ok, "valid artifact after malformed entry should be found")
	assert.Equal(t, "/artifacts/shot.png", path)
	assert.Equal(t, "image/png", mime)
}

// TestJournalArtifactResolver_NotFound proves LookupArtifact returns
// ("", "", false) when no entry in the journal matches the requested handle.
func TestJournalArtifactResolver_NotFound(t *testing.T) {
	t.Parallel()
	sid := app.SessionID("sess-jar-4")
	store := journal.NewMemStore()
	w := journal.NewMemWriter(store)
	r := journal.NewMemReader(store)

	require.NoError(t, w.Append(makeArtifactEntry(t, sid, journal.ArtifactEvent{
		ID:       "known#00000001",
		Kind:     "pdf",
		Mime:     "application/pdf",
		Path:     "/artifacts/doc.pdf",
		Producer: "host.artifacts_dir",
	})))

	resolver := &server.JournalArtifactResolver{Reader: r, SID: sid}
	path, mime, ok := resolver.LookupArtifact("unknown#ffffffff")

	assert.False(t, ok, "unknown handle must not be found")
	assert.Empty(t, path)
	assert.Empty(t, mime)
}

// errReader is a [journal.Reader] stub whose ReplayTyped returns an iterator
// that immediately stops and whose errFn always returns a non-nil error. Used
// to exercise the errFn branch in JournalArtifactResolver.
type errReader struct{ scanErr error }

func (e *errReader) ReplayTyped(_ app.SessionID) (iter.Seq[journal.Entry], func() error) {
	seq := func(yield func(journal.Entry) bool) {} // empty — yields nothing
	return seq, func() error { return e.scanErr }
}

func (e *errReader) LoadDocument(_ app.SessionID, _ journal.DocID) (json.RawMessage, journal.Version, error) {
	return nil, 0, nil
}
func (e *errReader) ReplayFrom(_ app.SessionID, _ journal.DocID, _ journal.Version) (iter.Seq[journal.Entry], func() error) {
	return func(func(journal.Entry) bool) {}, func() error { return nil }
}
func (e *errReader) LatestCheckpoint(_ app.SessionID, _ journal.DocID) (journal.Entry, bool, error) {
	return journal.Entry{}, false, nil
}
func (e *errReader) ListLiveDocs(_ app.SessionID) []journal.DocID { return nil }

// TestJournalArtifactResolver_ErrFnPath proves LookupArtifact returns
// ("", "", false) when the journal reader signals a scan error via errFn,
// treating the truncated-stream case as not-found.
func TestJournalArtifactResolver_ErrFnPath(t *testing.T) {
	t.Parallel()
	sid := app.SessionID("sess-jar-5")
	resolver := &server.JournalArtifactResolver{
		Reader: &errReader{scanErr: errors.New("db read error")},
		SID:    sid,
	}

	path, mime, ok := resolver.LookupArtifact("any#handle")

	assert.False(t, ok, "scan error must be treated as not-found")
	assert.Empty(t, path)
	assert.Empty(t, mime)
}
