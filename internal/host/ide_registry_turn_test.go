package host_test

// Slice-1 headless verification (item 1): host.ide.* through the REAL host
// registry with a STUBBED link — no editor, no socket.
//
// These tests drive host.ide.get_diagnostics the way the orchestrator does at
// runtime: register the builtins on a host.Registry, inject a fake host.IDELink
// into ctx with host.WithIDELink, and Invoke the verb by its dotted name. They
// assert (a) the diagnostics land in the verb's Result.Data slot exactly as a
// story `bind:` would read them, and (b) the read verb emits the
// ide.context_captured journal datapoint with the IDE provenance (port,
// workspace) and a response digest — without the raw diagnostic text. The fake
// link never dials a socket, so the test is fast and deterministic.
//
// This is the registry-level counterpart to the handler-direct tests in
// ide_handlers_test.go: it proves the verb resolves and dispatches end-to-end
// through Registry.Invoke + ctx injection, which is the seam the orchestrator's
// dispatchHostCalls uses.

import (
	"context"
	"encoding/json"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// TestIDEGetDiagnostics_ThroughRegistry drives host.ide.get_diagnostics through
// host.Registry.Invoke (the orchestrator's dispatch seam) with a fake link in
// ctx and asserts the diagnostics bind into the Result.Data["diagnostics"]
// slot — the slot a story's `bind:` resolves against.
func TestIDEGetDiagnostics_ThroughRegistry(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	link := &fakeLink{
		connected: true,
		results: map[string]json.RawMessage{
			"getDiagnostics": envelope(`{"diagnostics":[
				{"file":"/ws/a.go","message":"undefined: x","severity":"error","source":"compiler"},
				{"file":"/ws/b.go","message":"unused import","severity":"warning","source":"staticcheck"}
			]}`, false),
		},
	}
	ctx := host.WithIDELink(context.Background(), link)

	res, err := r.Invoke(ctx, "host.ide.get_diagnostics", map[string]any{"path": "/ws/a.go"})
	if err != nil {
		t.Fatalf("Invoke host.ide.get_diagnostics: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected domain error: %q", res.Error)
	}

	// The verb dispatched to the editor's getDiagnostics tool, forwarding
	// path→path (CONFIRMED wire shape — see IDEGetDiagnosticsHandler's doc
	// comment; a real-socket capture against the vscode-kitsoki extension
	// found it reads `args.path`, not `uri`).
	if link.lastTool != "getDiagnostics" {
		t.Fatalf("tool: want getDiagnostics, got %q", link.lastTool)
	}
	if link.lastArgs["path"] != "/ws/a.go" {
		t.Fatalf("path must be forwarded as path, got %v", link.lastArgs)
	}

	// connected:true is the universal signal a story branches on.
	if res.Data["connected"] != true {
		t.Fatalf("connected: want true, got %v", res.Data["connected"])
	}

	// The diagnostics bind into the expected world slot — a story `bind:`
	// against data.diagnostics would resolve to this list.
	diags, ok := res.Data["diagnostics"].([]any)
	if !ok {
		t.Fatalf("diagnostics slot wrong shape: %T %v", res.Data["diagnostics"], res.Data["diagnostics"])
	}
	if len(diags) != 2 {
		t.Fatalf("diagnostics: want 2 entries, got %d (%v)", len(diags), diags)
	}
	first, ok := diags[0].(map[string]any)
	if !ok || first["message"] != "undefined: x" || first["severity"] != "error" {
		t.Fatalf("first diagnostic shape wrong: %v", diags[0])
	}
}

// TestIDEGetDiagnostics_ThroughRegistry_EmitsContextCaptured proves the read
// verb records the ide.context_captured journal datapoint on the EventSink in
// ctx (the same sink the orchestrator wires for agent events). It pins the IDE
// provenance — port, workspace, a response digest — and crucially does NOT leak
// the raw diagnostic text into the trace (selection-privacy lean).
func TestIDEGetDiagnostics_ThroughRegistry_EmitsContextCaptured(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	const secretMsg = "undefined: superSecretSymbol"
	link := &fakeLink{
		connected: true,
		results: map[string]json.RawMessage{
			"getDiagnostics": envelope(`{"diagnostics":[{"file":"/ws/a.go","message":"`+secretMsg+`"}]}`, false),
		},
	}

	sink := &memSink{}
	ctx := host.WithIDELink(context.Background(), link)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("ide-test"),
		Turn:      app.TurnNumber(3),
		StatePath: app.StatePath("triage"),
	})
	ctx = host.WithAgentEventSink(ctx, sink)

	if _, err := r.Invoke(ctx, "host.ide.get_diagnostics", nil); err != nil {
		t.Fatalf("Invoke: %v", err)
	}

	// Exactly one ide.context_captured event, on the read verb.
	var captured *store.Event
	for i := range sink.events {
		if sink.events[i].Kind == store.IDEContextCaptured {
			if captured != nil {
				t.Fatal("expected exactly one ide.context_captured event")
			}
			captured = &sink.events[i]
		}
	}
	if captured == nil {
		t.Fatal("read verb must emit an ide.context_captured event")
	}
	if captured.StatePath != app.StatePath("triage") {
		t.Fatalf("event state_path: want triage, got %q", captured.StatePath)
	}

	var body struct {
		Verb           string `json:"verb"`
		Port           int    `json:"port"`
		Workspace      string `json:"workspace"`
		ResponseDigest string `json:"response_digest"`
	}
	if err := json.Unmarshal(captured.Payload, &body); err != nil {
		t.Fatalf("unmarshal event body: %v", err)
	}
	if body.Verb != "get_diagnostics" {
		t.Fatalf("verb: want get_diagnostics, got %q", body.Verb)
	}
	if body.Port != 12345 || body.Workspace != "/ws" {
		t.Fatalf("provenance: want port=12345 workspace=/ws, got port=%d workspace=%q", body.Port, body.Workspace)
	}
	if body.ResponseDigest == "" {
		t.Fatal("response_digest must be populated")
	}

	// Privacy lean: the raw diagnostic message must NOT appear anywhere in the
	// trace payload — only the digest stands in for the response.
	if containsSubstr(string(captured.Payload), secretMsg) {
		t.Fatalf("raw diagnostic text leaked into the trace payload: %s", captured.Payload)
	}
}

// TestIDEGetDiagnostics_ThroughRegistry_NoLink confirms the backward-compat
// path: with no link in ctx (the headless/flow-test default) Invoke returns the
// typed not-connected Result — connected:false with an empty diagnostics slot —
// and no Go error, so a legacy story branches cleanly on data.connected.
func TestIDEGetDiagnostics_ThroughRegistry_NoLink(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	res, err := r.Invoke(context.Background(), "host.ide.get_diagnostics", nil)
	if err != nil {
		t.Fatalf("no-link Invoke must not error, got %v", err)
	}
	if res.Data["connected"] != false {
		t.Fatalf("connected: want false with no link, got %v", res.Data["connected"])
	}
	diags, ok := res.Data["diagnostics"].([]any)
	if !ok || len(diags) != 0 {
		t.Fatalf("diagnostics slot must be present-but-empty, got %v", res.Data["diagnostics"])
	}
}

// containsSubstr is a tiny strings.Contains shim so this file doesn't import
// strings for a single privacy assertion (matches host_test.go's contains).
func containsSubstr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
