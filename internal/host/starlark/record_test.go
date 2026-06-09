package starlark_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"
	starlarkhost "kitsoki/internal/host/starlark"
)

// recordingTransport is a fake HTTPClient standing in for the real network when
// exercising record modes without a socket.
type recordingTransport struct {
	calls int
	resp  *starlarkhost.HTTPResponse
}

func (t *recordingTransport) Do(_ context.Context, _, _ string, _ map[string]string, _ []byte) (*starlarkhost.HTTPResponse, error) {
	t.calls++
	r := *t.resp
	return &r, nil
}

func recorded(method, url string, status int, body string) starlarkhost.HTTPEpisode {
	return starlarkhost.HTTPEpisode{
		Request:  &starlarkhost.HTTPRequestRecord{Method: method, URL: url},
		Response: starlarkhost.HTTPCanned{Status: status, Body: body},
	}
}

// ─── match_on ────────────────────────────────────────────────────────────────

func TestMatchOn_HostPathQuery(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{
		MatchOn: []string{"method", "host", "path", "query"},
		Exchanges: []starlarkhost.HTTPEpisode{
			recorded("GET", "https://api.example.com/v1/items?b=2&a=1", 200, `{"ok":true}`),
		},
	}
	rc := starlarkhost.NewReplayClient(cas)
	// Same host/path, query params in a different order, plus an unmatched-on
	// fragment difference — must still match.
	resp, err := rc.Do(context.Background(), "GET", "https://api.example.com/v1/items?a=1&b=2", nil, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
}

func TestMatchOn_BodyAndHeaders(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{
		MatchOn: []string{"method", "url", "body", "headers"},
		Exchanges: []starlarkhost.HTTPEpisode{{
			Request: &starlarkhost.HTTPRequestRecord{
				Method:  "POST",
				URL:     "https://api.example.com/widgets",
				Headers: map[string]string{"X-Tenant": "acme"},
				Body:    `{"name":"bolt"}`,
			},
			Response: starlarkhost.HTTPCanned{Status: 201, Body: "ok"},
		}},
	}
	rc := starlarkhost.NewReplayClient(cas)

	// Wrong body → miss.
	if _, err := rc.Do(context.Background(), "POST", "https://api.example.com/widgets",
		map[string]string{"X-Tenant": "acme"}, []byte(`{"name":"nut"}`)); err == nil {
		t.Fatal("expected miss on wrong body")
	}
	// Correct body + header subset → hit.
	resp, err := rc.Do(context.Background(), "POST", "https://api.example.com/widgets",
		map[string]string{"X-Tenant": "acme", "Extra": "ignored"}, []byte(`{"name":"bolt"}`))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.Status != 201 {
		t.Fatalf("status = %d, want 201", resp.Status)
	}
}

func TestMatchOn_LegacyEpisodeStillWorks(t *testing.T) {
	// A legacy match:/url_pattern episode must keep matching even when a
	// (recorded-only) match_on is configured on the cassette.
	cas := &starlarkhost.HTTPCassette{
		MatchOn: []string{"method", "body"},
		Exchanges: []starlarkhost.HTTPEpisode{{
			Match:    starlarkhost.HTTPMatch{Method: "GET", URLPattern: `/widgets/[0-9]+$`},
			Response: starlarkhost.HTTPCanned{Status: 200, Body: "legacy"},
		}},
	}
	rc := starlarkhost.NewReplayClient(cas)
	resp, err := rc.Do(context.Background(), "GET", "https://api.example.com/widgets/42", nil, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if string(resp.Body) != "legacy" {
		t.Fatalf("body = %q, want legacy", resp.Body)
	}
}

// ─── record modes ─────────────────────────────────────────────────────────────

func TestRecordMode_None_MissIsError(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{Exchanges: []starlarkhost.HTTPEpisode{recorded("GET", "https://x/a", 200, "a")}}
	rc := starlarkhost.NewRecordReplayClient(cas, starlarkhost.RecordModeNone, nil)
	if _, err := rc.Do(context.Background(), "GET", "https://x/miss", nil, nil); err == nil {
		t.Fatal("expected miss error in none mode")
	}
	if rc.Recorded() {
		t.Fatal("none mode must not record")
	}
}

func TestRecordMode_NewEpisodes_RecordsMiss(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{Exchanges: []starlarkhost.HTTPEpisode{recorded("GET", "https://x/a", 200, "a")}}
	tr := &recordingTransport{resp: &starlarkhost.HTTPResponse{Status: 200, Body: []byte("fresh")}}
	rc := starlarkhost.NewRecordReplayClient(cas, starlarkhost.RecordModeNewEpisodes, tr)

	// Existing episode replays without touching the transport.
	if resp, err := rc.Do(context.Background(), "GET", "https://x/a", nil, nil); err != nil || string(resp.Body) != "a" {
		t.Fatalf("replay existing: resp=%v err=%v", resp, err)
	}
	if tr.calls != 0 {
		t.Fatalf("transport called %d times on replay, want 0", tr.calls)
	}
	// A miss records via the transport and returns the real body.
	resp, err := rc.Do(context.Background(), "GET", "https://x/new", nil, nil)
	if err != nil || string(resp.Body) != "fresh" {
		t.Fatalf("record miss: resp=%v err=%v", resp, err)
	}
	if tr.calls != 1 || !rc.Recorded() {
		t.Fatalf("transport calls=%d recorded=%v, want 1/true", tr.calls, rc.Recorded())
	}
}

func TestRecordMode_All_AlwaysRecords(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{Exchanges: []starlarkhost.HTTPEpisode{recorded("GET", "https://x/a", 200, "old")}}
	tr := &recordingTransport{resp: &starlarkhost.HTTPResponse{Status: 200, Body: []byte("new")}}
	rc := starlarkhost.NewRecordReplayClient(cas, starlarkhost.RecordModeAll, tr)
	// Even a request that an existing episode would match is re-recorded.
	resp, err := rc.Do(context.Background(), "GET", "https://x/a", nil, nil)
	if err != nil || string(resp.Body) != "new" {
		t.Fatalf("all-mode: resp=%v err=%v", resp, err)
	}
	if tr.calls != 1 {
		t.Fatalf("transport calls = %d, want 1 (no replay in all mode)", tr.calls)
	}
}

func TestRecordMode_Once(t *testing.T) {
	// Empty cassette → records.
	empty := &starlarkhost.HTTPCassette{}
	tr := &recordingTransport{resp: &starlarkhost.HTTPResponse{Status: 200, Body: []byte("rec")}}
	rc := starlarkhost.NewRecordReplayClient(empty, starlarkhost.RecordModeOnce, tr)
	if _, err := rc.Do(context.Background(), "GET", "https://x/a", nil, nil); err != nil {
		t.Fatalf("once/empty should record: %v", err)
	}
	if tr.calls != 1 {
		t.Fatalf("once/empty transport calls = %d, want 1", tr.calls)
	}
	// Non-empty cassette → replay-only; a miss errors.
	full := &starlarkhost.HTTPCassette{Exchanges: []starlarkhost.HTTPEpisode{recorded("GET", "https://x/a", 200, "a")}}
	tr2 := &recordingTransport{resp: &starlarkhost.HTTPResponse{Status: 200, Body: []byte("rec")}}
	rc2 := starlarkhost.NewRecordReplayClient(full, starlarkhost.RecordModeOnce, tr2)
	if _, err := rc2.Do(context.Background(), "GET", "https://x/miss", nil, nil); err == nil {
		t.Fatal("once/non-empty should not record a miss")
	}
	if tr2.calls != 0 {
		t.Fatalf("once/non-empty transport calls = %d, want 0", tr2.calls)
	}
}

func TestRecordMode_AllowPlaybackRepeats(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{
		AllowPlaybackRepeats: true,
		Exchanges:            []starlarkhost.HTTPEpisode{recorded("GET", "https://x/a", 200, "a")},
	}
	rc := starlarkhost.NewReplayClient(cas)
	for i := 0; i < 3; i++ {
		if _, err := rc.Do(context.Background(), "GET", "https://x/a", nil, nil); err != nil {
			t.Fatalf("repeat %d: %v", i, err)
		}
	}
}

func TestIgnoreHosts_PassThrough(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{IgnoreHosts: []string{"metrics.internal"}}
	tr := &recordingTransport{resp: &starlarkhost.HTTPResponse{Status: 204, Body: nil}}
	rc := starlarkhost.NewRecordReplayClient(cas, starlarkhost.RecordModeNone, tr)
	// none mode would normally error on any request, but an ignored host passes
	// straight to the transport and is not recorded.
	resp, err := rc.Do(context.Background(), "POST", "https://metrics.internal/event", nil, []byte("x"))
	if err != nil || resp.Status != 204 {
		t.Fatalf("ignore-host passthrough: resp=%v err=%v", resp, err)
	}
	if tr.calls != 1 || rc.Recorded() {
		t.Fatalf("ignored host: calls=%d recorded=%v, want 1/false", tr.calls, rc.Recorded())
	}
}

// ─── redaction on write ───────────────────────────────────────────────────────

func TestFlush_RedactsSecrets(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{
		FilterHeaders:            []string{"X-Api-Key"},
		FilterQueryParameters:    []string{"token"},
		FilterPostDataParameters: []string{"password"},
	}
	tr := &recordingTransport{resp: &starlarkhost.HTTPResponse{
		Status:  200,
		Headers: map[string]string{"Set-Cookie": "session=abc"},
		Body:    []byte("ok"),
	}}
	rc := starlarkhost.NewRecordReplayClient(cas, starlarkhost.RecordModeAll, tr)
	live, err := rc.Do(context.Background(), "POST",
		"https://api.example.com/login?token=supersecret",
		map[string]string{"Authorization": "Bearer xyz", "X-Api-Key": "k1"},
		[]byte(`{"user":"a","password":"hunter2"}`))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	// The live response served to the script is NOT redacted.
	if live.Headers["Set-Cookie"] != "session=abc" {
		t.Fatalf("live Set-Cookie = %q, want unredacted", live.Headers["Set-Cookie"])
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "c.http.yaml")
	if err := rc.Flush(path, ""); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(out)
	for _, secret := range []string{"Bearer xyz", "k1", "supersecret", "hunter2", "session=abc"} {
		if strings.Contains(s, secret) {
			t.Fatalf("written cassette leaks secret %q:\n%s", secret, s)
		}
	}
	if !strings.Contains(s, "REDACTED") {
		t.Fatalf("written cassette has no REDACTED placeholder:\n%s", s)
	}
	// And it round-trips back into a usable cassette.
	var reloaded starlarkhost.HTTPCassette
	if err := goyaml.Unmarshal(out, &reloaded); err != nil {
		t.Fatalf("reload written cassette: %v", err)
	}
	if len(reloaded.Exchanges) != 1 {
		t.Fatalf("reloaded exchanges = %d, want 1", len(reloaded.Exchanges))
	}
}

func TestMarshalCassette_JSON(t *testing.T) {
	cas := &starlarkhost.HTTPCassette{Kind: "http_cassette", Exchanges: []starlarkhost.HTTPEpisode{recorded("GET", "https://x/a", 200, "a")}}
	b, err := starlarkhost.MarshalCassette(cas, "json")
	if err != nil {
		t.Fatalf("MarshalCassette: %v", err)
	}
	if !strings.Contains(string(b), `"kind"`) {
		t.Fatalf("json output missing kind: %s", b)
	}
}

// ─── production transport + httptest round-trip ───────────────────────────────

// TestRecordingClient_RealServer covers the previously-untested production
// RecordingClient.Do against a real (in-process) HTTP server: request building,
// header propagation, body read, status, and the body-free exchange summary.
func TestRecordingClient_RealServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			body, _ := io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"echo":` + string(body) + `,"ct":"` + r.Header.Get("Content-Type") + `"}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("hello " + r.Header.Get("X-Name")))
	}))
	defer srv.Close()

	rc := starlarkhost.NewRecordingClient()
	resp, err := rc.Do(context.Background(), "GET", srv.URL+"/greet", map[string]string{"X-Name": "ada"}, nil)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.Status != 200 || string(resp.Body) != "hello ada" {
		t.Fatalf("GET resp = %d %q", resp.Status, resp.Body)
	}
	if len(rc.Exchanges) != 1 || rc.Exchanges[0].Status != 200 {
		t.Fatalf("exchanges = %+v, want one 200", rc.Exchanges)
	}
}

// TestRecordReplay_RoundTrip_HTTPTest proves record→write→reload→replay against
// an in-process server, fully offline and CI-safe — the deterministic stand-in
// for the gated live jsonplaceholder run.
func TestRecordReplay_RoundTrip_HTTPTest(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":1,"name":"Ada"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "round.http.yaml")

	// Phase 1: record (empty cassette, once-mode) against the real server.
	rec := starlarkhost.NewRecordReplayClient(&starlarkhost.HTTPCassette{}, starlarkhost.RecordModeOnce, starlarkhost.NewRecordingClient())
	resp, err := rec.Do(context.Background(), "GET", srv.URL+"/users/1", nil, nil)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	if string(resp.Body) != `{"id":1,"name":"Ada"}` {
		t.Fatalf("recorded body = %q", resp.Body)
	}
	if err := rec.Flush(path, ""); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if hits != 1 {
		t.Fatalf("server hits after record = %d, want 1", hits)
	}

	// Phase 2: reload the written cassette and replay — no server contact.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var reloaded starlarkhost.HTTPCassette
	if err := goyaml.Unmarshal(raw, &reloaded); err != nil {
		t.Fatalf("reload: %v", err)
	}
	rep := starlarkhost.NewReplayClient(&reloaded)
	resp2, err := rep.Do(context.Background(), "GET", srv.URL+"/users/1", nil, nil)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if string(resp2.Body) != `{"id":1,"name":"Ada"}` {
		t.Fatalf("replayed body = %q", resp2.Body)
	}
	if hits != 1 {
		t.Fatalf("server hits after replay = %d, want still 1 (replay must not hit network)", hits)
	}
}
