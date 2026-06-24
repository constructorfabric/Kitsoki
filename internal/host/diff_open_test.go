package host_test

// Tests for host.diff.open — the surface-resolving diff front door.
//
// No live editor, no real claude, no network: the IDE path runs against the
// same in-process fakeLink the host.ide.* tests use (envelope() canned MCP
// result envelopes); the difftool path shells a FAKE difftool via
// $KITSOKI_DIFFTOOL (a tiny script that records its argv and exits 0); the
// no-surface path needs neither. The IDE verdict wire shape is the CONTRACT the
// stub is coded to (ide-integration.md #1) — pinned here until a real-socket
// capture confirms it.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// --- IDE path: accept and reject verdicts captured from the stubbed link. ---

func TestDiffOpen_IDE_Verdict(t *testing.T) {
	cases := []struct {
		name        string
		envelope    json.RawMessage
		wantVerdict any
	}{
		// Structured payload {"verdict": ...} — the headline contract shape.
		{"accept-structured", envelope(`{"verdict":"accept"}`, false), "accept"},
		{"reject-structured", envelope(`{"verdict":"reject"}`, false), "reject"},
		// {"accepted": bool} — an accepted shorthand the contract also accepts.
		{"accept-bool", envelope(`{"accepted":true}`, false), "accept"},
		{"reject-bool", envelope(`{"accepted":false}`, false), "reject"},
		// Bare text token (Claude Code's documented openDiff acknowledgements).
		{"accept-token", envelope("FILE_SAVED", false), "accept"},
		{"reject-token", envelope("DIFF_REJECTED", false), "reject"},
		// Tab closed without a decision → no verdict (never fabricated).
		{"no-decision", envelope("DIFF_TAB_OPENED", false), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			link := &fakeLink{
				connected: true,
				results:   map[string]json.RawMessage{"openDiff": tc.envelope},
			}
			ctx := host.WithIDELink(context.Background(), link)
			res, err := host.DiffOpenHandler(ctx, map[string]any{
				"path": "/a.go", "new_text": "x", "title": "review",
			})
			if err != nil {
				t.Fatalf("DiffOpenHandler: %v", err)
			}
			if link.lastTool != "openDiff" {
				t.Fatalf("tool: want openDiff, got %q", link.lastTool)
			}
			if res.Data["surface"] != "ide" {
				t.Fatalf("surface: want ide, got %v", res.Data["surface"])
			}
			if res.Data["reviewed"] != true {
				t.Fatalf("reviewed: want true, got %v", res.Data["reviewed"])
			}
			if res.Data["verdict"] != tc.wantVerdict {
				t.Fatalf("verdict: want %v, got %v", tc.wantVerdict, res.Data["verdict"])
			}
		})
	}
}

// --- IDE path records a gate_decided event for the verdict (the moat). ---

func TestDiffOpen_IDE_RecordsGateDecision(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results:   map[string]json.RawMessage{"openDiff": envelope(`{"verdict":"accept"}`, false)},
	}
	sink := &memSink{}
	ctx := host.WithIDELink(context.Background(), link)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("diff-test"),
		Turn:      app.TurnNumber(4),
		StatePath: app.StatePath("design_refine"),
	})
	ctx = host.WithAgentEventSink(ctx, sink)

	if _, err := host.DiffOpenHandler(ctx, map[string]any{
		"paths": []any{"a.go", "b.go"}, "base": "HEAD", "title": "review",
	}); err != nil {
		t.Fatalf("DiffOpenHandler: %v", err)
	}

	var gate *store.Event
	for i := range sink.events {
		if sink.events[i].Kind == store.GateDecided {
			gate = &sink.events[i]
		}
	}
	if gate == nil {
		t.Fatal("IDE verdict must record a gate_decided event")
	}
	if gate.StatePath != app.StatePath("design_refine") {
		t.Fatalf("gate state_path: want design_refine, got %q", gate.StatePath)
	}
	var body struct {
		Surface      string         `json:"surface"`
		Verdict      string         `json:"verdict"`
		ChosenIntent string         `json:"chosen_intent"`
		Decider      string         `json:"decider"`
		Diff         map[string]any `json:"diff"`
	}
	if err := json.Unmarshal(gate.Payload, &body); err != nil {
		t.Fatalf("unmarshal gate payload: %v", err)
	}
	if body.Surface != "ide" || body.Verdict != "accept" || body.ChosenIntent != "accept" {
		t.Fatalf("gate payload wrong: %+v", body)
	}
	if body.Decider != "human" {
		t.Fatalf("decider: want human, got %q", body.Decider)
	}
	if body.Diff["base"] != "HEAD" {
		t.Fatalf("diff identity must pin base=HEAD, got %v", body.Diff)
	}
}

// --- IDE path with NO verdict emits NO gate event (never fabricated). ---

func TestDiffOpen_IDE_NoVerdict_NoGateEvent(t *testing.T) {
	link := &fakeLink{
		connected: true,
		results:   map[string]json.RawMessage{"openDiff": envelope("DIFF_TAB_OPENED", false)},
	}
	sink := &memSink{}
	ctx := host.WithIDELink(context.Background(), link)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{StatePath: app.StatePath("s")})
	ctx = host.WithAgentEventSink(ctx, sink)

	res, err := host.DiffOpenHandler(ctx, map[string]any{"path": "/a.go", "new_text": "x"})
	if err != nil {
		t.Fatalf("DiffOpenHandler: %v", err)
	}
	if res.Data["verdict"] != nil {
		t.Fatalf("verdict: want nil, got %v", res.Data["verdict"])
	}
	for i := range sink.events {
		if sink.events[i].Kind == store.GateDecided {
			t.Fatal("no verdict ⇒ no gate_decided event must be recorded")
		}
	}
}

// --- Difftool path: a connected IDE absent, a FAKE difftool via
// $KITSOKI_DIFFTOOL records its argv and exits 0 → reviewed:true, verdict:null,
// surface "difftool:<name>". No verdict event. ---

func TestDiffOpen_Difftool_FakeTool(t *testing.T) {
	dir := t.TempDir()
	argvLog := filepath.Join(dir, "argv.txt")
	script := filepath.Join(dir, "fakediff")
	// A tiny script: record its argv, exit 0. Records the call so we can assert
	// the difftool was actually shelled (not faked by the handler).
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvLog + "\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KITSOKI_DIFFTOOL", script+" --review")

	sink := &memSink{}
	ctx := host.WithAgentCallCtx(context.Background(), host.AgentCallCtx{StatePath: app.StatePath("s")})
	ctx = host.WithAgentEventSink(ctx, sink)

	// No IDE link in ctx → the difftool branch is taken.
	res, err := host.DiffOpenHandler(ctx, map[string]any{"path": "/a.go", "new_text": "x"})
	if err != nil {
		t.Fatalf("DiffOpenHandler: %v", err)
	}
	if res.Data["surface"] != "difftool:fakediff" {
		t.Fatalf("surface: want difftool:fakediff, got %v", res.Data["surface"])
	}
	if res.Data["reviewed"] != true {
		t.Fatalf("reviewed: want true, got %v", res.Data["reviewed"])
	}
	if res.Data["verdict"] != nil {
		t.Fatalf("verdict: want nil (view-only), got %v", res.Data["verdict"])
	}
	// The fake difftool was actually exec'd: it wrote its argv.
	if _, err := os.Stat(argvLog); err != nil {
		t.Fatalf("fake difftool was not shelled (no argv log): %v", err)
	}
	logged, _ := os.ReadFile(argvLog)
	if string(logged) != "--review\n" {
		t.Fatalf("difftool argv: want --review, got %q", string(logged))
	}
	// View-only: no gate_decided event.
	for i := range sink.events {
		if sink.events[i].Kind == store.GateDecided {
			t.Fatal("difftool path is view-only — no gate_decided event must be recorded")
		}
	}
}

// --- No IDE, no difftool → surface "none", reviewed:false. ---

func TestDiffOpen_None(t *testing.T) {
	// Force resolveDifftool to find nothing: an empty $KITSOKI_DIFFTOOL and a
	// PATH with no git/code. host.SetDiffLookPathForTest swaps the lookup so the
	// test is hermetic regardless of the host's real PATH.
	t.Setenv("KITSOKI_DIFFTOOL", "")
	restore := host.SetDiffLookPathForTest(func(string) (string, error) {
		return "", os.ErrNotExist
	})
	defer restore()

	res, err := host.DiffOpenHandler(context.Background(), map[string]any{"path": "/a.go", "new_text": "x"})
	if err != nil {
		t.Fatalf("DiffOpenHandler: %v", err)
	}
	if res.Data["surface"] != "none" {
		t.Fatalf("surface: want none, got %v", res.Data["surface"])
	}
	if res.Data["reviewed"] != false {
		t.Fatalf("reviewed: want false, got %v", res.Data["reviewed"])
	}
	if res.Data["verdict"] != nil {
		t.Fatalf("verdict: want nil, got %v", res.Data["verdict"])
	}
}

// --- host.diff.open is registered and passes ValidateAllowList. ---

func TestDiffOpen_Registered(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	if _, ok := r.Get("host.diff.open"); !ok {
		t.Fatal("host.diff.open not registered")
	}
	if err := r.ValidateAllowList([]string{"host.diff.open"}); err != nil {
		t.Fatalf("ValidateAllowList: %v", err)
	}
	// host.ide.open_diff stays unchanged and registered (the IDE path it calls).
	if _, ok := r.Get("host.ide.open_diff"); !ok {
		t.Fatal("host.ide.open_diff must remain registered")
	}
}
