// Package agent — registry tests.
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRegistry_RegisterAndGet verifies basic registration and retrieval.
func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()

	// Use a closingAgent (pointer-based) so identity comparison works.
	o := &closingAgent{}
	reg.Register("agent.claude", o)

	got, ok := reg.Get("agent.claude")
	if !ok {
		t.Fatal("Get: agent.claude not found")
	}
	// Compare by calling Ask on both — same pointer means same agent.
	if got != o {
		t.Error("Get: returned different agent than registered")
	}
}

// TestRegistry_DuplicatePanics verifies that registering the same name twice
// panics.
func TestRegistry_DuplicatePanics(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	o := New(AskFunc(func(_ context.Context, _ AskRequest) (AskResponse, error) {
		return AskResponse{}, nil
	}))
	reg.Register("agent.claude", o)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration, got none")
		}
	}()
	reg.Register("agent.claude", o)
}

// TestRegistry_ResolveDefault verifies that Resolve("") falls back to
// "agent.claude".
func TestRegistry_ResolveDefault(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	o := &closingAgent{}
	reg.Register("agent.claude", o)

	got, err := reg.Resolve("")
	if err != nil {
		t.Fatalf("Resolve(''): unexpected error: %v", err)
	}
	if got != o {
		t.Error("Resolve(''): returned different agent than registered")
	}
}

// TestRegistry_ResolveUnknown verifies that Resolve returns an error for
// unknown names with no fallback.
func TestRegistry_ResolveUnknown(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	// No agent registered.

	_, err := reg.Resolve("agent.nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "agent.nonexistent") {
		t.Errorf("error should mention the name; got: %v", err)
	}
}

// TestRegistry_ResolveNamedFallsBackToDefault verifies that a named agent
// that's absent falls back to agent.claude when it exists.
func TestRegistry_ResolveNamedFallsBackToDefault(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	defaultAgent := &closingAgent{}
	reg.Register("agent.claude", defaultAgent)

	// Resolve a name that doesn't exist; should fall back to agent.claude.
	got, err := reg.Resolve("agent.nonexistent")
	if err != nil {
		t.Fatalf("expected fallback to default, got error: %v", err)
	}
	if got != defaultAgent {
		t.Error("fallback: did not return agent.claude")
	}
}

// TestRegistry_Close closes all agents without error.
func TestRegistry_Close(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	var closed1, closed2 bool
	makeCloser := func(flag *bool) Agent {
		return closingAgent{flag: flag}
	}
	reg.Register("agent.a", makeCloser(&closed1))
	reg.Register("agent.b", makeCloser(&closed2))

	if err := reg.Close(); err != nil {
		t.Fatalf("Close: unexpected error: %v", err)
	}
	if !closed1 {
		t.Error("agent.a was not closed")
	}
	if !closed2 {
		t.Error("agent.b was not closed")
	}
}

// TestPluginNotSupportedError verifies the error message.
func TestPluginNotSupportedError(t *testing.T) {
	t.Parallel()
	err := &PluginNotSupportedError{Plugin: "mcp_http"}
	if !strings.Contains(err.Error(), "mcp_http") {
		t.Errorf("error message should mention plugin name; got: %v", err)
	}
	if !strings.Contains(err.Error(), "B-3") {
		t.Errorf("error message should mention B-3; got: %v", err)
	}
}

// TestRegistry_ConcurrentAccess verifies the registry is safe for concurrent
// read after setup.
func TestRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	o := &closingAgent{}
	reg.Register("agent.claude", o)
	reg.Register("agent.b", o)

	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			got, err := reg.Resolve("agent.claude")
			if err != nil {
				errs <- err
				return
			}
			if got != o {
				errs <- errors.New("got unexpected agent")
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Resolve: %v", err)
		}
	}
}

// closingAgent is an Agent that tracks whether Close was called.
type closingAgent struct {
	flag *bool
}

func (c closingAgent) Ask(_ context.Context, _ AskRequest) (AskResponse, error) {
	return AskResponse{}, nil
}

func (c closingAgent) Close() error {
	if c.flag != nil {
		*c.flag = true
	}
	return nil
}
