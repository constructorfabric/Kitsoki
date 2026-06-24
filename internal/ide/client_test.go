package ide

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// shortCtx returns a ctx with a generous-but-bounded deadline so a hung test
// fails fast rather than hanging the suite.
func shortCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestDial_AuthSuccessHandshakeAndTools(t *testing.T) {
	s := newStubServer(t)
	c, err := Dial(shortCtx(t), s.lock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	tools := c.Tools()
	if len(tools) != 5 {
		t.Fatalf("want 5 advertised tools, got %d (%+v)", len(tools), tools)
	}
	if tools[0].Name != "getDiagnostics" {
		t.Fatalf("first tool = %q", tools[0].Name)
	}
}

func TestDial_BadTokenRejected1008(t *testing.T) {
	s := newStubServer(t)
	bad := s.lock()
	bad.AuthToken = "WRONG"
	_, err := Dial(shortCtx(t), bad)
	if err == nil {
		t.Fatal("dial with bad token must fail")
	}
	// Pin the auth MECHANISM, not just "handshake failed somehow": the server
	// must reject with a 1008 policy-violation close (the verified contract, wire
	// note §3), and the client must surface that exact close status. The error is
	// wrapped with %w through roundTripInline → handshake → Dial, so the
	// underlying websocket.CloseError survives the chain. A regression that
	// stopped sending the x-claude-code-ide-authorization header would still 1008
	// here (no header ⇒ mismatch), so this asserts the header path is load-bearing
	// rather than some unrelated handshake failure (timeout, parse error).
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("bad-token dial: want close status %d (StatusPolicyViolation), got %d (err: %v)",
			websocket.StatusPolicyViolation, got, err)
	}
}

func TestCallTool_RoundTrip(t *testing.T) {
	s := newStubServer(t)
	c, err := Dial(shortCtx(t), s.lock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.Close()

	raw, err := c.CallTool(shortCtx(t), "getDiagnostics", map[string]any{"uri": "/abs/file.go"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	// raw is the MCP result envelope {content:[{text}], isError}.
	var env struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}
	if env.IsError || len(env.Content) != 1 {
		t.Fatalf("bad envelope: %+v", env)
	}
	var payload struct {
		Diagnostics []map[string]any `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(env.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Diagnostics) != 1 {
		t.Fatalf("want 1 diagnostic, got %+v", payload.Diagnostics)
	}
	if got := s.gotCalls(); len(got) != 1 || got[0] != "getDiagnostics" {
		t.Fatalf("stub recorded calls = %v", got)
	}
}

func TestCallTool_NilArgsSendsEmptyObject(t *testing.T) {
	s := newStubServer(t)
	c, err := Dial(shortCtx(t), s.lock())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.CallTool(shortCtx(t), "getOpenEditors", nil); err != nil {
		t.Fatalf("nil args should send {}: %v", err)
	}
}

func TestCallTool_CtxCancelUnblocks(t *testing.T) {
	s := newStubServer(t)
	c, err := Dial(shortCtx(t), s.lock())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// A cancelled context must surface as context.Canceled — not hang, and not
	// ErrNotConnected. Use an ALREADY-cancelled context so the assertion is
	// deterministic: the previous version cancelled on a goroutine and raced the
	// call, which on a tightly-scheduled runner could let a connection drop win
	// and return ErrNotConnected before the cancel was observed (CI flake). The
	// contract is enforced by the ctx.Err() short-circuit at the top of CallTool.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.CallTool(ctx, "getDiagnostics", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestCallTool_DropFailsInFlight(t *testing.T) {
	s := newStubServer(t)
	// The stub closes the socket right after answering the first tools/call;
	// the read-pump observes the close and the NEXT call fails not-connected.
	s.dropAfter = 1
	c, err := Dial(shortCtx(t), s.lock())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// First call succeeds, then the server drops.
	if _, err := c.CallTool(shortCtx(t), "getDiagnostics", nil); err != nil {
		t.Fatalf("first call should succeed before drop: %v", err)
	}

	// The pump needs to observe the close; subsequent calls must fail with
	// ErrNotConnected. Poll briefly to let the pump's Read return.
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err = c.CallTool(shortCtx(t), "getDiagnostics", nil)
		if errors.Is(err, ErrNotConnected) {
			return // success: drop failed the call as not-connected
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected ErrNotConnected after drop, got %v", err)
		}
	}
}

func TestClose_Idempotent(t *testing.T) {
	s := newStubServer(t)
	c, err := Dial(shortCtx(t), s.lock())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	_ = c.Close() // must not panic / double-close
	if _, err := c.CallTool(shortCtx(t), "getDiagnostics", nil); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("call after Close => ErrNotConnected, got %v", err)
	}
}
