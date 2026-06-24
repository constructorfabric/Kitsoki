package host_test

// write_mode_gate_test.go — stateless probe for the write-mode gate
// (agent-write-mode-opt-in.md "Verification"). Mirrors bash_profile_test.go's
// table style: drive the gate directly with a room posture + a stubbed operator
// grant, assert the pass/deny decision and the recorded WriteModeGranted event.
//
// No LLM, no subprocess: the gate is a deterministic function of the tool call's
// class plus the active grant; only the operator's verdict is interpretive and it
// is stubbed here (a WriteModeAsker closure), never a live human or model.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/store"
)

// gateCtx builds a ctx with an event sink + AgentCallCtx so the gate records
// WriteModeGranted events that the test can inspect.
func gateCtx() (context.Context, *captureSink) {
	sink := &captureSink{}
	ctx := host.WithAgentEventSink(context.Background(), sink)
	ctx = host.WithAgentCallCtx(ctx, host.AgentCallCtx{
		SessionID: app.SessionID("sess-wmg"),
		Turn:      app.TurnNumber(1),
		StatePath: app.StatePath("workbench"),
	})
	return ctx, sink
}

// grantAsker returns a WriteModeAsker that always grants the given scope.
func grantAsker(scope host.GrantScope) host.WriteModeAsker {
	return func(_ context.Context, _ string, _ host.MutatingEffect) (host.GrantScope, error) {
		return scope, nil
	}
}

func lastWriteModeEvent(t *testing.T, sink *captureSink) (kindCount int, payload map[string]any) {
	t.Helper()
	for _, e := range sink.events {
		if e.Kind != store.WriteModeGranted {
			continue
		}
		kindCount++
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			t.Fatalf("unmarshal WriteModeGranted payload: %v", err)
		}
		payload = p
	}
	return kindCount, payload
}

// ── (a) read tools pass through with no gate and no event ─────────────────────

func TestWriteModeGate_ReadToolsPass(t *testing.T) {
	ctx, sink := gateCtx()
	g := host.NewWriteModeGate(true, host.ScopeNone, nil) // headless
	for _, tc := range []host.ToolCall{
		{Name: "Read"},
		{Name: "Grep"},
		{Name: "Glob"},
		{Name: "Bash", Command: "git log --oneline"},
		{Name: "Bash", Command: "grep -rn foo ."},
		{Name: "WebFetch"},
	} {
		dec := g.Resolve(ctx, tc)
		if !dec.Granted {
			t.Errorf("read tool %q/%q must pass the gate; got denied", tc.Name, tc.Command)
		}
	}
	if n, _ := lastWriteModeEvent(t, sink); n != 0 {
		t.Errorf("read-only exploration must record no WriteModeGranted event; got %d", n)
	}
}

// ── (b) mutating call, no operator → intercepted + denied, denial recorded ────

func TestWriteModeGate_HeadlessDeniesMutation(t *testing.T) {
	for _, tc := range []host.ToolCall{
		{Name: "Write"},
		{Name: "Edit"},
		{Name: "MultiEdit"},
		{Name: "NotebookEdit"},
		{Name: "Bash", Command: "git push"},
		{Name: "Bash", Command: "rm -rf /tmp/x"},
	} {
		ctx, sink := gateCtx()
		g := host.NewWriteModeGate(true, host.ScopeNone, nil) // no asker = headless
		dec := g.Resolve(ctx, tc)
		if dec.Granted {
			t.Errorf("mutating %q/%q must be denied headless; got granted", tc.Name, tc.Command)
		}
		if dec.By != "headless_denied" {
			t.Errorf("by: want headless_denied, got %q", dec.By)
		}
		n, p := lastWriteModeEvent(t, sink)
		if n != 1 {
			t.Fatalf("want exactly 1 WriteModeGranted (denial) event, got %d", n)
		}
		if p["granted"] != false {
			t.Errorf("denial event granted: want false, got %v", p["granted"])
		}
		if p["by"] != "headless_denied" {
			t.Errorf("denial event by: want headless_denied, got %v", p["by"])
		}
	}
}

// ── (c) scope=action grant lets exactly that call through, then re-asks ────────

func TestWriteModeGate_ActionScopeOneShot(t *testing.T) {
	ctx, sink := gateCtx()
	calls := 0
	asker := func(_ context.Context, _ string, _ host.MutatingEffect) (host.GrantScope, error) {
		calls++
		return host.ScopeAction, nil
	}
	g := host.NewWriteModeGate(true, host.ScopeNone, asker)

	d1 := g.Resolve(ctx, host.ToolCall{Name: "Write"})
	if !d1.Granted || d1.Scope != host.ScopeAction {
		t.Fatalf("first Write: want granted action, got granted=%v scope=%q", d1.Granted, d1.Scope)
	}
	// A second mutating call must re-ask (action is one-shot, no active scope set).
	d2 := g.Resolve(ctx, host.ToolCall{Name: "Edit"})
	if !d2.Granted {
		t.Fatalf("second Edit with re-grant: want granted, got denied")
	}
	if calls != 2 {
		t.Errorf("action scope must re-ask each call: want 2 asks, got %d", calls)
	}
	if n, _ := lastWriteModeEvent(t, sink); n != 2 {
		t.Errorf("want 2 grant events (one per action), got %d", n)
	}
}

// ── (d) scope=turn grant: a second call passes WITHOUT re-asking ──────────────

func TestWriteModeGate_TurnScopeShortCircuits(t *testing.T) {
	ctx, sink := gateCtx()
	calls := 0
	asker := func(_ context.Context, _ string, _ host.MutatingEffect) (host.GrantScope, error) {
		calls++
		return host.ScopeTurn, nil
	}
	g := host.NewWriteModeGate(true, host.ScopeNone, asker)

	if d := g.Resolve(ctx, host.ToolCall{Name: "Write"}); !d.Granted || d.Scope != host.ScopeTurn {
		t.Fatalf("first Write: want granted turn, got granted=%v scope=%q", d.Granted, d.Scope)
	}
	// Subsequent writes in the same dispatch short-circuit on the active turn grant.
	for i := 0; i < 3; i++ {
		d := g.Resolve(ctx, host.ToolCall{Name: "Edit"})
		if !d.Granted {
			t.Fatalf("Edit %d under active turn grant: want granted, got denied", i)
		}
		if d.By != "active_scope" {
			t.Errorf("Edit %d by: want active_scope, got %q", i, d.By)
		}
	}
	if calls != 1 {
		t.Errorf("turn scope must ask exactly once: got %d asks", calls)
	}
	// Exactly one grant event recorded (the short-circuited writes record nothing new).
	if n, _ := lastWriteModeEvent(t, sink); n != 1 {
		t.Errorf("turn scope: want 1 grant event, got %d", n)
	}
	if g.ActiveScope() != host.ScopeTurn {
		t.Errorf("ActiveScope after turn grant: want turn, got %q", g.ActiveScope())
	}
}

// ── seeded turn scope (from the world key) short-circuits with no ask at all ──

func TestWriteModeGate_SeededTurnScopeNoAsk(t *testing.T) {
	ctx, sink := gateCtx()
	asker := func(_ context.Context, _ string, _ host.MutatingEffect) (host.GrantScope, error) {
		t.Fatal("asker must not be called when an active turn grant is seeded")
		return host.ScopeNone, nil
	}
	g := host.NewWriteModeGate(true, host.ScopeTurn, asker)
	if d := g.Resolve(ctx, host.ToolCall{Name: "Write"}); !d.Granted || d.By != "active_scope" {
		t.Fatalf("seeded turn grant: want granted via active_scope, got granted=%v by=%q", d.Granted, d.By)
	}
	if n, _ := lastWriteModeEvent(t, sink); n != 0 {
		t.Errorf("a pre-seeded grant records no new event; got %d", n)
	}
}

// ── external effect always re-asks even under an active turn grant ────────────

func TestWriteModeGate_ExternalAlwaysReAsks(t *testing.T) {
	ctx, _ := gateCtx()
	calls := 0
	asker := func(_ context.Context, _ string, eff host.MutatingEffect) (host.GrantScope, error) {
		calls++
		return host.ScopeAction, nil
	}
	// Seed an active TURN grant — it covers writes but must NOT cover external.
	g := host.NewWriteModeGate(true, host.ScopeTurn, asker)

	// A write short-circuits on the seeded grant (no ask).
	if d := g.Resolve(ctx, host.ToolCall{Name: "Write"}); d.By != "active_scope" {
		t.Fatalf("write under seeded turn grant should short-circuit; got by=%q", d.By)
	}
	// An external call re-asks despite the active turn grant.
	d := g.Resolve(ctx, host.ToolCall{Name: "Bash", Command: "git push", Effect: host.EffectExternal})
	if !d.Granted {
		t.Fatalf("external with action grant: want granted, got denied")
	}
	if calls != 1 {
		t.Errorf("external must re-ask despite active turn grant: want 1 ask, got %d", calls)
	}
}

// ── operator decline holds the agent read-only, records a denial ──────────────

func TestWriteModeGate_OperatorDeclineDenies(t *testing.T) {
	ctx, sink := gateCtx()
	g := host.NewWriteModeGate(true, host.ScopeNone, grantAsker(host.ScopeNone)) // ScopeNone = deny
	d := g.Resolve(ctx, host.ToolCall{Name: "Write"})
	if d.Granted {
		t.Fatalf("operator decline: want denied, got granted")
	}
	if d.By != "operator" {
		t.Errorf("by: want operator, got %q", d.By)
	}
	n, p := lastWriteModeEvent(t, sink)
	if n != 1 || p["granted"] != false || p["by"] != "operator" {
		t.Errorf("decline event: want 1/granted=false/by=operator, got %d/%v/%v", n, p["granted"], p["by"])
	}
}

// ── open / nil room is a pure pass-through (no gate, no event) ─────────────────

func TestWriteModeGate_OpenRoomPassThrough(t *testing.T) {
	ctx, sink := gateCtx()
	// Open room.
	g := host.NewWriteModeGate(false, host.ScopeNone, nil)
	if d := g.Resolve(ctx, host.ToolCall{Name: "Write"}); !d.Granted {
		t.Fatalf("open room must pass every call; Write denied")
	}
	// nil gate.
	var nilGate *host.WriteModeGate
	if d := nilGate.Resolve(ctx, host.ToolCall{Name: "Edit"}); !d.Granted {
		t.Fatalf("nil gate must pass every call; Edit denied")
	}
	if n, _ := lastWriteModeEvent(t, sink); n != 0 {
		t.Errorf("open/nil room records no event; got %d", n)
	}
}

// ── bash MCP wrapper routes a mutating command through the gate ───────────────
//
// This is the per-tool interception path generalized from bash_mcp.go: a
// read-only-profile-conforming command runs; a profile-rejected command is NOT a
// flat deny but routed through the gate (headless ⇒ tool-error; granted ⇒ runs).

func TestBashMCP_WriteModeGate_ReadCommandRuns(t *testing.T) {
	srv := host.NewBashMCPServer(&host.BashProfile{Kind: host.BashProfileReadOnly}, t.TempDir()).
		WithGate(host.NewWriteModeGate(true, host.ScopeNone, nil))
	res := srv.InvokeForTest(t, `{"command":"echo hello"}`)
	if res.IsError {
		t.Fatalf("read-only command should run under the gate; got error: %s", res.Text)
	}
}

func TestBashMCP_WriteModeGate_HeadlessBlocksMutation(t *testing.T) {
	srv := host.NewBashMCPServer(&host.BashProfile{Kind: host.BashProfileReadOnly}, t.TempDir()).
		WithGate(host.NewWriteModeGate(true, host.ScopeNone, nil)) // headless
	res := srv.InvokeForTest(t, `{"command":"rm -rf /tmp/should-not-run"}`)
	if !res.IsError {
		t.Fatalf("mutating command headless must be blocked by the gate; got success: %s", res.Text)
	}
}

func TestBashMCP_WriteModeGate_GrantUnblocks(t *testing.T) {
	dir := t.TempDir()
	srv := host.NewBashMCPServer(&host.BashProfile{Kind: host.BashProfileReadOnly}, dir).
		WithGate(host.NewWriteModeGate(true, host.ScopeNone, grantAsker(host.ScopeTurn)))
	// `touch` is not on the read-only allowlist → a mutating step. The granting
	// asker lets it through; the file is actually created.
	res := srv.InvokeForTest(t, `{"command":"touch gated.txt"}`)
	if res.IsError {
		t.Fatalf("granted mutating command should run; got error: %s", res.Text)
	}
}

// ── read-only floor rewrite + no-regression for open rooms ────────────────────

// applyReadOnlyFloorCLIArgs downgrades bypassPermissions → default and adds the
// read-only deny set as a hard backstop, merging the existing AskUserQuestion deny.
func TestReadOnlyFloorCLIArgs_DowngradesAndDenies(t *testing.T) {
	base := []string{
		"-p",
		"--permission-mode", "bypassPermissions",
		"--model", "claude-x",
		"--disallowedTools", "AskUserQuestion",
	}
	out := host.ApplyReadOnlyFloorCLIArgsExport(base)
	joined := strings.Join(out, " ")
	// bypassPermissions must be gone; default must be present.
	if strings.Contains(joined, "bypassPermissions") {
		t.Errorf("read-only floor must drop bypassPermissions; got %q", joined)
	}
	if !strings.Contains(joined, "--permission-mode default") {
		t.Errorf("read-only floor must set --permission-mode default; got %q", joined)
	}
	// Every read-only-denied tool plus AskUserQuestion must be in --disallowedTools.
	deny := denyFlagValue(t, out)
	for _, want := range append(host.ReadOnlyDeniedToolsExport(), "AskUserQuestion") {
		if !strings.Contains(deny, want) {
			t.Errorf("read-only floor --disallowedTools missing %q; got %q", want, deny)
		}
	}
	// No duplicate AskUserQuestion entry.
	if strings.Count(deny, "AskUserQuestion") != 1 {
		t.Errorf("AskUserQuestion must appear exactly once in deny set; got %q", deny)
	}
}

// No-regression: IsReadOnlyWriteMode is false for the absent/open postures, so the
// read_only block (and its CLI rewrite) never runs for a story without write_mode.
func TestIsReadOnlyWriteMode_OpenPostures(t *testing.T) {
	for _, posture := range []string{"", "open"} {
		if host.IsReadOnlyWriteMode(posture) {
			t.Errorf("posture %q must NOT trigger the read-only floor (today's behavior preserved)", posture)
		}
	}
	if !host.IsReadOnlyWriteMode("read_only") {
		t.Errorf("read_only posture must trigger the gate")
	}
}

// denyFlagValue extracts the --disallowedTools CSV value from a CLI arg slice.
func denyFlagValue(t *testing.T, args []string) string {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--disallowedTools" {
			return args[i+1]
		}
	}
	t.Fatalf("no --disallowedTools flag in %v", args)
	return ""
}

// ── the action label recorded on the event is reconstructable ─────────────────

func TestWriteModeGate_ActionLabelRecorded(t *testing.T) {
	ctx, sink := gateCtx()
	g := host.NewWriteModeGate(true, host.ScopeNone, grantAsker(host.ScopeAction))
	g.Resolve(ctx, host.ToolCall{Name: "Bash", Command: "git push origin main"})
	_, p := lastWriteModeEvent(t, sink)
	if got := p["action"]; got != "Bash: git push origin main" {
		t.Errorf("action label: want %q, got %v", "Bash: git push origin main", got)
	}
	if got := p["state"]; got != "workbench" {
		t.Errorf("state: want workbench, got %v", got)
	}
}
