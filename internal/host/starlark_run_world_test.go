package host

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestStarlarkRunHandler_ReadsInjectedWorldSnapshot proves the orchestrator's
// WithWorldSnapshot injection reaches a script's ctx.world.get — i.e. ctx.world
// reflects live world state with no with.world plumbing by the author. This is
// the regression guard for the world-threading seam in host_dispatch.go.
func TestStarlarkRunHandler_ReadsInjectedWorldSnapshot(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "echo.star")
	if err := os.WriteFile(script, []byte(
		"def main(ctx):\n    return {\"who\": ctx.world.get(\"user\") or \"<none>\"}\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(script+".yaml", []byte(
		"outputs:\n  who: { type: string }\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	// No with.world supplied: the only source of "user" is the injected snapshot.
	ctx := WithWorldSnapshot(context.Background(), map[string]any{"user": "ada"})
	res, err := StarlarkRunHandler(ctx, map[string]any{"script": script})
	if err != nil {
		t.Fatalf("StarlarkRunHandler: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected domain error: %s", res.Error)
	}
	if got := res.Data["who"]; got != "ada" {
		t.Fatalf("ctx.world.get(\"user\") = %v, want \"ada\" (world snapshot not threaded)", got)
	}

	// Absent keys read back as None, surfaced here through the script's `or`.
	res2, err := StarlarkRunHandler(WithWorldSnapshot(context.Background(), map[string]any{}), map[string]any{"script": script})
	if err != nil {
		t.Fatalf("StarlarkRunHandler (empty world): %v", err)
	}
	if got := res2.Data["who"]; got != "<none>" {
		t.Fatalf("ctx.world.get(absent) = %v, want None (rendered <none>)", got)
	}
}
