package main

import (
	"os"
	"testing"
)

func resetSemanticRoutingGlobals(t *testing.T) {
	t.Helper()
	oldFlag := semanticRoutingFlag
	oldSet := semanticRoutingFlagSet
	oldEnv, hadEnv := os.LookupEnv("KITSOKI_SEMANTIC_ROUTING")
	semanticRoutingFlag = false
	semanticRoutingFlagSet = false
	if err := os.Unsetenv("KITSOKI_SEMANTIC_ROUTING"); err != nil {
		t.Fatalf("unset KITSOKI_SEMANTIC_ROUTING: %v", err)
	}
	t.Cleanup(func() {
		semanticRoutingFlag = oldFlag
		semanticRoutingFlagSet = oldSet
		if hadEnv {
			_ = os.Setenv("KITSOKI_SEMANTIC_ROUTING", oldEnv)
		} else {
			_ = os.Unsetenv("KITSOKI_SEMANTIC_ROUTING")
		}
	})
}

func TestSemanticRoutingOverrideUnsetDefersToAppConfig(t *testing.T) {
	resetSemanticRoutingGlobals(t)

	_, ok := semanticRoutingOverride()
	if ok {
		t.Fatal("unset flag/env should not force an orchestrator override")
	}
	// Unset must produce NO options at all — appending a default-off
	// WithSemanticRouting(false) here would silently override every app's
	// own routing.enabled (default true), which is the bug this pins
	// against (see .context/dwf2-d1-findings.md).
	if got := semanticRoutingOptions(); len(got) != 0 {
		t.Fatalf("unset flag/env should defer to per-app routing.enabled (no override option), got %d option(s)", len(got))
	}
}

func TestSemanticRoutingOverrideReadsEnv(t *testing.T) {
	resetSemanticRoutingGlobals(t)
	if err := os.Setenv("KITSOKI_SEMANTIC_ROUTING", "on"); err != nil {
		t.Fatalf("set KITSOKI_SEMANTIC_ROUTING: %v", err)
	}

	enabled, ok := semanticRoutingOverride()
	if !ok || !enabled {
		t.Fatalf("env override = (%v, %v), want (true, true)", enabled, ok)
	}
	if got := semanticRoutingOptions(); len(got) != 1 {
		t.Fatalf("env override should produce one option, got %d", len(got))
	}
}

func TestSemanticRoutingOverrideFlagBeatsEnv(t *testing.T) {
	resetSemanticRoutingGlobals(t)
	if err := os.Setenv("KITSOKI_SEMANTIC_ROUTING", "true"); err != nil {
		t.Fatalf("set KITSOKI_SEMANTIC_ROUTING: %v", err)
	}
	semanticRoutingFlag = false
	semanticRoutingFlagSet = true

	enabled, ok := semanticRoutingOverride()
	if !ok || enabled {
		t.Fatalf("flag override = (%v, %v), want (false, true)", enabled, ok)
	}
}

func TestSemanticRoutingOverrideIgnoresInvalidEnv(t *testing.T) {
	resetSemanticRoutingGlobals(t)
	if err := os.Setenv("KITSOKI_SEMANTIC_ROUTING", "maybe"); err != nil {
		t.Fatalf("set KITSOKI_SEMANTIC_ROUTING: %v", err)
	}

	_, ok := semanticRoutingOverride()
	if ok {
		t.Fatal("invalid env without an explicit flag should not force an override")
	}
}
