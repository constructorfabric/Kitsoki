package host

import (
	"context"
	"slices"
	"testing"
)

func hasEnv(env []string, kv string) bool { return slices.Contains(env, kv) }

func hasKey(env []string, key string) bool {
	for _, e := range env {
		if len(e) > len(key) && e[:len(key)+1] == key+"=" {
			return true
		}
	}
	return false
}

// envScrubIDE drops the SSE port seed and forces auto-connect off, leaving
// every other entry untouched, on a fresh slice.
func TestEnvScrubIDE(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"CLAUDE_CODE_SSE_PORT=51234",
		"KITSOKI_SESSION_ID=abc",
	}
	out := envScrubIDE(in)

	if hasKey(out, "CLAUDE_CODE_SSE_PORT") {
		t.Fatal("CLAUDE_CODE_SSE_PORT must be removed")
	}
	if !hasEnv(out, "CLAUDE_CODE_AUTO_CONNECT_IDE=false") {
		t.Fatalf("AUTO_CONNECT must be set false, got %v", out)
	}
	if !hasEnv(out, "PATH=/usr/bin") || !hasEnv(out, "KITSOKI_SESSION_ID=abc") {
		t.Fatalf("unrelated entries must be preserved, got %v", out)
	}
	// Input not mutated.
	if !hasEnv(in, "CLAUDE_CODE_SSE_PORT=51234") {
		t.Fatal("input slice must not be mutated")
	}
}

// An existing AUTO_CONNECT=true is replaced (not duplicated) with false.
func TestEnvScrubIDE_ReplacesAutoConnect(t *testing.T) {
	out := envScrubIDE([]string{"CLAUDE_CODE_AUTO_CONNECT_IDE=true"})
	count := 0
	for _, e := range out {
		if e == "CLAUDE_CODE_AUTO_CONNECT_IDE=false" {
			count++
		}
		if e == "CLAUDE_CODE_AUTO_CONNECT_IDE=true" {
			t.Fatal("the true value must be replaced")
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one AUTO_CONNECT=false entry, got %d in %v", count, out)
	}
}

// The scrub gate is a no-op when ctx carries no link: IDELinkFromContext is nil
// so callers never invoke envScrubIDE and the env is byte-identical.
func TestEnvScrubGate_NoLinkIsNoOp(t *testing.T) {
	ctx := context.Background()
	if IDELinkFromContext(ctx) != nil {
		t.Fatal("no link should resolve to nil")
	}
	// WithIDELink(nil) is also a no-op.
	if IDELinkFromContext(WithIDELink(ctx, nil)) != nil {
		t.Fatal("WithIDELink(nil) must leave the link nil")
	}
}
