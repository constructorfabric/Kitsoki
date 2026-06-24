package ide

// Comprehensive end-to-end regression suite for the /ide runtime substrate.
//
// This is the backbone that lets us trust /ide WITHOUT a live editor: it stands
// up the in-process stub ws MCP server + a temp lock file in a temp HOME and
// drives the REAL path top to bottom — real discovery, real auth header, real
// MCP initialize/tools/list handshake, the real *ide.Link, the real
// host.ide.* handlers (resolved through host.Registry.Invoke exactly as the
// orchestrator's dispatchHostCalls does), the real journal EventSink seam, and
// the real single-flight reconnect on a socket drop. Nothing here mocks the
// transport: every byte crosses a real loopback WebSocket. It stays fast (no
// sleeps, bounded ctx) and hermetic (no real editor, no real claude, no network
// off-box).
//
// package ide (white-box) so it can reuse the stubServer helper from
// stubserver_test.go AND import kitsoki/internal/host to drive the verb
// handlers against a real *ide.Link — host never imports ide, so this test-only
// import introduces no cycle.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// compile-time proof the real *Link satisfies the host boundary the e2e drives.
var _ host.IDELink = (*Link)(nil)

// e2eSink is a minimal concurrency-safe in-memory EventSink for the e2e: it
// records every journal event so the suite can assert the ide.context_captured
// datapoints land with the right provenance and without leaking raw text.
type e2eSink struct {
	mu     sync.Mutex
	events []store.Event
}

func (s *e2eSink) Append(ev store.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

func (s *e2eSink) History() store.History {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Event, len(s.events))
	copy(out, s.events)
	return store.History(out)
}

func (s *e2eSink) captured() []store.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Event
	for _, e := range s.events {
		if e.Kind == store.IDEContextCaptured {
			out = append(out, e)
		}
	}
	return out
}

// e2eHostCtx builds the ctx the orchestrator hands host.ide.* handlers: the
// real link injected as a host.IDELink, plus the agent call-ctx + event sink
// the journal datapoint rides on. Mirrors dispatchHostCalls' wiring.
func e2eHostCtx(parent context.Context, l *Link, sink store.EventSink) context.Context {
	ctx := host.WithIDELink(parent, l)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("ide-e2e"),
		Turn:      app.TurnNumber(7),
		StatePath: app.StatePath("triage"),
	})
	return host.WithAgentEventSink(ctx, sink)
}

// containsRaw reports whether s contains sub (a tiny privacy-assertion shim so
// this file doesn't reach for strings just for one check).
func containsRaw(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ── 1 + 2: full real path — discovery → dial → handshake → every verb ────────

// TestE2E_FullPath_DiscoverDialAndAllVerbs is the spine of the suite. It stands
// up the stub editor + lock file in a temp HOME, runs the REAL Link.Connect
// (real discovery, real auth header, real MCP initialize, real tools/list),
// asserts LinkInfo + the advertised tool set, then drives EVERY host.ide.* verb
// through the REAL host registry against the REAL link → real client → stub
// server, asserting each verb's bound Result.Data shape, that every get_*
// records an ide.context_captured journal event, and that the link reports
// connected (the ide.connected world signal source).
func TestE2E_FullPath_DiscoverDialAndAllVerbs(t *testing.T) {
	s := newStubServer(t)
	cwd := "/home/u/code/proj"
	s.writeLock(cwd) // <port>.lock in the stub's temp ~/.claude/ide

	// REAL discovery + dial + handshake.
	l := NewLink(cwd, s.discoverer(nil))
	defer l.Close()
	info, err := l.Connect(shortCtx(t))
	if err != nil {
		t.Fatalf("Connect (real discovery+dial+handshake): %v", err)
	}

	// LinkInfo: ideName, workspace, port all from the discovered lock.
	if !info.Connected {
		t.Fatal("LinkInfo.Connected must be true after a successful Connect")
	}
	if info.IDEName != "Stub Code" {
		t.Fatalf("LinkInfo.IDEName = %q, want %q", info.IDEName, "Stub Code")
	}
	if info.Workspace != cwd {
		t.Fatalf("LinkInfo.Workspace = %q, want %q", info.Workspace, cwd)
	}
	if info.Port != s.port {
		t.Fatalf("LinkInfo.Port = %d, want %d", info.Port, s.port)
	}
	// ide.connected world-key source: the link must report live.
	if !l.Connected() {
		t.Fatal("link.Connected() must be true (drives the ide.connected world key)")
	}

	// Tool set captured at Dial via the real tools/list handshake.
	gotTools := map[string]bool{}
	for _, ti := range l.client.Tools() {
		gotTools[ti.Name] = true
	}
	for _, want := range []string{"getDiagnostics", "getCurrentSelection", "getOpenEditors", "openFile", "openDiff"} {
		if !gotTools[want] {
			t.Fatalf("tools/list missing %q; got %v", want, gotTools)
		}
	}

	// Drive every verb through the REAL host registry with the REAL link.
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	sink := &e2eSink{}
	ctx := e2eHostCtx(shortCtx(t), l, sink)

	// get_diagnostics → getDiagnostics, diagnostics[] bound, connected:true.
	res, err := r.Invoke(ctx, "host.ide.get_diagnostics", map[string]any{"path": "/abs/file.go"})
	if err != nil {
		t.Fatalf("get_diagnostics: %v", err)
	}
	if res.Data["connected"] != true {
		t.Fatalf("get_diagnostics connected: want true, got %v", res.Data["connected"])
	}
	if diags, ok := res.Data["diagnostics"].([]any); !ok || len(diags) != 1 {
		t.Fatalf("get_diagnostics diagnostics slot wrong: %v", res.Data["diagnostics"])
	}

	// get_selection → getCurrentSelection (LIVE), file/text/range bound.
	res, err = r.Invoke(ctx, "host.ide.get_selection", nil)
	if err != nil {
		t.Fatalf("get_selection: %v", err)
	}
	if res.Data["connected"] != true || res.Data["file"] != "/abs/file.go" || res.Data["text"] != "selected text" {
		t.Fatalf("get_selection shape wrong: %v", res.Data)
	}
	if res.Data["range"] == nil {
		t.Fatal("get_selection range must be populated")
	}

	// get_open_editors → getOpenEditors, editors[] bound.
	res, err = r.Invoke(ctx, "host.ide.get_open_editors", nil)
	if err != nil {
		t.Fatalf("get_open_editors: %v", err)
	}
	if eds, ok := res.Data["editors"].([]any); !ok || len(eds) != 1 {
		t.Fatalf("get_open_editors editors slot wrong: %v", res.Data["editors"])
	}

	// open_file → openFile, ok:true (side-effect verb, no journal datapoint).
	res, err = r.Invoke(ctx, "host.ide.open_file", map[string]any{"path": "/abs/file.go"})
	if err != nil {
		t.Fatalf("open_file: %v", err)
	}
	if res.Data["ok"] != true || res.Data["connected"] != true {
		t.Fatalf("open_file shape wrong: %v", res.Data)
	}

	// open_diff → openDiff, NON-BLOCKING ack ok:true.
	res, err = r.Invoke(ctx, "host.ide.open_diff", map[string]any{"path": "/abs/file.go", "new_text": "x", "title": "t"})
	if err != nil {
		t.Fatalf("open_diff: %v", err)
	}
	if res.Data["ok"] != true || res.Data["connected"] != true {
		t.Fatalf("open_diff shape wrong: %v", res.Data)
	}

	// The stub server saw the real tools/call names, in order.
	wantCalls := []string{"getDiagnostics", "getCurrentSelection", "getOpenEditors", "openFile", "openDiff"}
	gotCalls := s.gotCalls()
	if len(gotCalls) != len(wantCalls) {
		t.Fatalf("stub saw %v, want %v", gotCalls, wantCalls)
	}
	for i, w := range wantCalls {
		if gotCalls[i] != w {
			t.Fatalf("tools/call[%d] = %q, want %q (full order %v)", i, gotCalls[i], w, gotCalls)
		}
	}

	// Exactly the three read verbs recorded ide.context_captured datapoints —
	// open_file/open_diff are side-effect-only and emit none.
	caps := sink.captured()
	if len(caps) != 3 {
		t.Fatalf("want 3 ide.context_captured events (the 3 read verbs), got %d", len(caps))
	}
	wantVerbs := map[string]bool{"get_diagnostics": false, "get_selection": false, "get_open_editors": false}
	for _, ev := range caps {
		var body struct {
			Verb           string `json:"verb"`
			Port           int    `json:"port"`
			Workspace      string `json:"workspace"`
			ResponseDigest string `json:"response_digest"`
		}
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			t.Fatalf("unmarshal captured body: %v", err)
		}
		if _, ok := wantVerbs[body.Verb]; !ok {
			t.Fatalf("unexpected captured verb %q", body.Verb)
		}
		wantVerbs[body.Verb] = true
		// Provenance: real port + workspace from the live link.
		if body.Port != s.port || body.Workspace != cwd {
			t.Fatalf("captured provenance for %s: port=%d workspace=%q, want port=%d workspace=%q",
				body.Verb, body.Port, body.Workspace, s.port, cwd)
		}
		if body.ResponseDigest == "" {
			t.Fatalf("captured %s: response_digest must be populated", body.Verb)
		}
		if ev.StatePath != app.StatePath("triage") || ev.Turn != app.TurnNumber(7) {
			t.Fatalf("captured %s: wrong turn/state (%v/%v)", body.Verb, ev.Turn, ev.StatePath)
		}
		// Privacy lean: the raw selection text must never reach the trace.
		if containsRaw(string(ev.Payload), "selected text") {
			t.Fatalf("captured %s leaked raw selection text into the trace: %s", body.Verb, ev.Payload)
		}
	}
	for v, seen := range wantVerbs {
		if !seen {
			t.Fatalf("read verb %q did not record an ide.context_captured event", v)
		}
	}
}

// ── 3: lifecycle — socket drop fails in-flight + next call, then reconnect ───

// TestE2E_Lifecycle_DropThenReconnect drives the real failure/recovery arc
// through the host verb handlers (the surface a story actually sees). The first
// editor drops the socket after one tools/call; a fresh editor + lock come up
// in the SAME lock dir, and a follow-up host.ide call must transparently
// single-flight reconnect and succeed against the new server.
func TestE2E_Lifecycle_DropThenReconnect(t *testing.T) {
	first := newStubServer(t)
	first.dropAfter = 1 // close the socket right after the first tools/call
	cwd := "/home/u/code/proj"
	first.writeLock(cwd)

	l := NewLink(cwd, first.discoverer(nil))
	defer l.Close()
	if _, err := l.Connect(shortCtx(t)); err != nil {
		t.Fatalf("initial connect: %v", err)
	}

	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	sink := &e2eSink{}
	ctx := e2eHostCtx(shortCtx(t), l, sink)

	// First call succeeds; the stub then drops the socket.
	res, err := r.Invoke(ctx, "host.ide.get_open_editors", nil)
	if err != nil {
		t.Fatalf("first call before drop: %v", err)
	}
	if res.Data["connected"] != true {
		t.Fatalf("first call should be connected, got %v", res.Data)
	}

	// Stand up a replacement editor and rewrite the lock dir to point at it.
	second := newStubServer(t)
	if err := removeLock(first); err != nil {
		t.Fatalf("remove stale lock: %v", err)
	}
	writeLockInto(t, first.lockDir, second, cwd)

	// The next host.ide call observes the drop and single-flight reconnects to
	// the freshest lock. The handler retries once internally; we poll the verb
	// (each Invoke is a fresh CallTool) until the reconnect lands, bounded so a
	// regression fails fast rather than hanging.
	deadline := time.Now().Add(3 * time.Second)
	var reconnected bool
	for time.Now().Before(deadline) {
		res, err = r.Invoke(ctx, "host.ide.get_open_editors", nil)
		if err != nil {
			t.Fatalf("post-drop Invoke must not surface a Go error: %v", err)
		}
		if res.Data["connected"] == true {
			reconnected = true
			break
		}
		// connected:false is the typed not-connected result while the dead
		// client is still in place; keep polling — the reconnect is single-flight.
	}
	if !reconnected {
		t.Fatal("host.ide call did not transparently reconnect to the fresh editor")
	}
	if l.Port() != second.port {
		t.Fatalf("after reconnect link.Port() = %d, want freshest %d", l.Port(), second.port)
	}
	// The fresh server actually served the reconnected call.
	if len(second.gotCalls()) == 0 {
		t.Fatal("the replacement editor served no tools/call after reconnect")
	}
}

// TestE2E_Lifecycle_NoReplacementYieldsNotConnected proves the other branch: a
// drop with no fresh lock to reconnect to surfaces the typed not-connected
// Result (connected:false) through the verb — never a Go error — so a story can
// branch on data.connected. This is the "editor closed and stayed closed" path.
func TestE2E_Lifecycle_NoReplacementYieldsNotConnected(t *testing.T) {
	s := newStubServer(t)
	s.dropAfter = 1
	cwd := "/home/u/code/proj"
	s.writeLock(cwd)

	l := NewLink(cwd, s.discoverer(nil))
	defer l.Close()
	if _, err := l.Connect(shortCtx(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	ctx := e2eHostCtx(shortCtx(t), l, &e2eSink{})

	// First call succeeds, the stub drops, and its lock is removed so discovery
	// finds nothing to reconnect to.
	if _, err := r.Invoke(ctx, "host.ide.get_diagnostics", nil); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if err := removeLock(s); err != nil {
		t.Fatalf("remove lock: %v", err)
	}

	// Post-drop calls must ALWAYS resolve to the typed not-connected Result,
	// never a Go error — whether the drop is observed as a write failure on the
	// dead socket (the narrow race window where the read-pump hasn't drained yet)
	// or via the client's dead-guard once drained. We fire several so both paths
	// are exercised regardless of scheduling; every one must be clean.
	for i := 0; i < 5; i++ {
		res, err := r.Invoke(ctx, "host.ide.get_diagnostics", nil)
		if err != nil {
			t.Fatalf("call %d: drop-with-no-editor must not surface a Go error: %v", i, err)
		}
		if res.Data["connected"] != false {
			t.Fatalf("call %d: want connected:false after drop with no replacement, got %v", i, res.Data)
		}
		// The empty diagnostics slot must still be present so a story `bind:` resolves.
		if diags, ok := res.Data["diagnostics"].([]any); !ok || len(diags) != 0 {
			t.Fatalf("call %d: not-connected diagnostics slot must be present-but-empty, got %v", i, res.Data["diagnostics"])
		}
	}
}

// TestE2E_Lifecycle_InFlightCallFailsOnDrop asserts the concurrency-model
// guarantee at the client level: a CallTool blocked waiting on a reply when the
// socket drops is failed with ErrNotConnected (not left hanging until ctx
// timeout). The stub accepts the handshake, then on the tools/call closes the
// socket WITHOUT replying — so the read-pump's drain path is what must unblock
// the in-flight waiter. The bounded ctx proves it isn't a timeout masquerading
// as the drop.
func TestE2E_Lifecycle_InFlightCallFailsOnDrop(t *testing.T) {
	s := newStubServer(t)
	s.dropOnCall = true // close on tools/call without replying
	cwd := "/home/u/code/proj"
	s.writeLock(cwd)

	c, err := Dial(shortCtx(t), s.lock(cwd))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	// A bounded ctx: if the drain path didn't unblock the waiter this would
	// fail with ctx.DeadlineExceeded instead of ErrNotConnected.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = c.CallTool(ctx, "getDiagnostics", map[string]any{})
	if !errors.Is(err, ErrNotConnected) {
		t.Fatalf("in-flight drop must fail with ErrNotConnected (read-pump drain), got %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("the call must be failed by the drain, not by ctx timeout")
	}
}

// ── 4: env hygiene — gate input from a REAL connected/absent link ────────────

// TestE2E_EnvHygiene_GateDrivenByRealLink proves the input the agent-subprocess
// env scrub gate keys on (IDELinkFromContext(ctx) != nil && link.Connected()) is
// driven correctly by a REAL *ide.Link across its lifecycle: true while
// connected to the stub editor, false after Close. The byte-level scrub of the
// composed env is asserted at the real exec sites in package host
// (ide_env_e2e_test.go) — host can't be imported white-box here, but the gate's
// load-bearing predicate is the link state this test pins end to end.
func TestE2E_EnvHygiene_GateDrivenByRealLink(t *testing.T) {
	s := newStubServer(t)
	cwd := "/home/u/code/proj"
	s.writeLock(cwd)

	l := NewLink(cwd, s.discoverer(nil))
	if _, err := l.Connect(shortCtx(t)); err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Connected link in ctx → gate predicate true → scrub applies.
	ctx := host.WithIDELink(context.Background(), l)
	gate := func(c context.Context) bool {
		gl := host.IDELinkFromContext(c)
		return gl != nil && gl.Connected()
	}
	if !gate(ctx) {
		t.Fatal("with a connected real link the env-scrub gate must be true")
	}

	// No link in ctx → gate false → env untouched (backward-compat path).
	if gate(context.Background()) {
		t.Fatal("with no link the env-scrub gate must be false (env unchanged)")
	}

	// After Close the real link reports disconnected → gate false even though it
	// is still in ctx.
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if gate(ctx) {
		t.Fatal("after Close the env-scrub gate must be false")
	}
}

// ── 5: picker / selection — ordered candidates + SSE_PORT override ───────────

// TestE2E_Picker_OrderedCandidatesAndSSEOverride proves the /ide picker feed:
// multiple lock files in the temp HOME yield ordered Candidates (longest
// workspace prefix first, no-match last), and a CLAUDE_CODE_SSE_PORT override
// wins outright. It drives Link.Candidates (the real discovery the picker uses)
// — no socket is opened.
func TestE2E_Picker_OrderedCandidatesAndSSEOverride(t *testing.T) {
	dir := lockDir(t)
	// Three editors: a long-prefix match, a short-prefix match, and a no-match.
	writeRawLock(t, dir, 1111, map[string]any{
		"transport": "ws", "authToken": "a", "ideName": "VS",
		"workspaceFolders": []string{"/home/u/code"},
	})
	writeRawLock(t, dir, 2222, map[string]any{
		"transport": "ws", "authToken": "b", "ideName": "VS",
		"workspaceFolders": []string{"/home/u/code/proj"},
	})
	writeRawLock(t, dir, 3333, map[string]any{
		"transport": "ws", "authToken": "c", "ideName": "VS",
		"workspaceFolders": []string{"/unrelated"},
	})
	cwd := "/home/u/code/proj/pkg"

	// No env override: longest workspace prefix wins, no-match last.
	noEnv := &Discoverer{LockDir: dir, Environ: func(string) string { return "" }}
	cands, err := NewLink(cwd, noEnv).Candidates(shortCtx(t))
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(cands) != 3 {
		t.Fatalf("want 3 ordered candidates, got %d (%+v)", len(cands), cands)
	}
	if cands[0].Port != 2222 {
		t.Fatalf("longest-prefix (/home/u/code/proj) must rank first, got %d", cands[0].Port)
	}
	if cands[1].Port != 1111 {
		t.Fatalf("shorter-prefix (/home/u/code) must rank second, got %d", cands[1].Port)
	}
	if cands[2].Port != 3333 {
		t.Fatalf("no-match must rank last, got %d", cands[2].Port)
	}

	// SSE_PORT override → that lock wins outright (element 0), beating the
	// longest-prefix workspace match.
	envOverride := &Discoverer{LockDir: dir, Environ: func(k string) string {
		if k == sseEnvVar {
			return "3333"
		}
		return ""
	}}
	cands, err = NewLink(cwd, envOverride).Candidates(shortCtx(t))
	if err != nil {
		t.Fatalf("Candidates (SSE override): %v", err)
	}
	if len(cands) == 0 || cands[0].Port != 3333 {
		t.Fatalf("CLAUDE_CODE_SSE_PORT=3333 must win outright; got first = %+v", cands)
	}
}
