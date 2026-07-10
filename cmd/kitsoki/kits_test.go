package main

import (
	"context"
	"testing"
)

const syntheticKitsDir = "../../internal/app/testdata/kits"

func TestBuildKitDispatcher_EmptyDirIsDisabled(t *testing.T) {
	d, err := buildKitDispatcher("")
	if err != nil {
		t.Fatalf("buildKitDispatcher(\"\"): %v", err)
	}
	if d != nil {
		t.Errorf("buildKitDispatcher(\"\") = %v, want nil (disabled)", d)
	}
}

func TestBuildKitDispatcher_DiscoversAndCalls(t *testing.T) {
	d, err := buildKitDispatcher(syntheticKitsDir)
	if err != nil {
		t.Fatalf("buildKitDispatcher(%q): %v", syntheticKitsDir, err)
	}
	if d == nil {
		t.Fatal("buildKitDispatcher returned nil for a populated kits dir")
	}
	if d.Kits().Len() != 1 {
		t.Fatalf("Kits().Len() = %d, want 1", d.Kits().Len())
	}

	result, err := d.Call(context.Background(), "synthetic", "reporter", "announce", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// host.run (the fixture's default binding) requires a "cmd" arg; without
	// one it reports a domain Result.Error rather than erroring at
	// resolution — which is exactly what this test wants to confirm: the
	// dispatcher resolved kit->iface->op->handler correctly and reached the
	// REAL RegisterBuiltins host.run handler (no cmd -> Result.Error, not a
	// Go error).
	if result.Error == "" {
		t.Fatalf("expected host.run to report a domain error for a missing cmd, got Data=%v", result.Data)
	}
}

func TestMcpKitOption_NilDispatcherIsNoop(t *testing.T) {
	// mcpKitOption(nil) must return a usable, no-op Option rather than nil or
	// panicking when applied.
	opt := mcpKitOption(nil)
	if opt == nil {
		t.Fatal("mcpKitOption(nil) returned a nil Option")
	}
}
