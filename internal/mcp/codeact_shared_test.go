package mcp

import (
	"context"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

func TestEvaluateCodeactUsesInjectedInspector(t *testing.T) {
	caps, err := starlarkhost.ParseCapabilities(map[string]any{
		"fs": map[string]any{"read": []any{"a.txt"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := EvaluateCodeact(context.Background(), CodeactEvaluationConfig{
		WorkingDir: t.TempDir(), Capabilities: caps,
		Inspector: starlarkhost.NewReplayInspector(&starlarkhost.InspectCassette{Interactions: []starlarkhost.InspectInteraction{{Op: "read", Target: "a.txt", Out: "hello\n"}}}),
		Args: CodeactEvalArgs{Snippet: `def main(ctx):
    return {"body": ctx.fs.read("a.txt")}
`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Outputs["body"] != "hello\n" || len(out.Inspections) != 1 || out.Inspections[0].Target != "a.txt" {
		t.Fatalf("unexpected shared evaluation: %#v", out)
	}
}

func TestCodeactCapabilityHashIsOrderIndependent(t *testing.T) {
	first, err := starlarkhost.ParseCapabilities(map[string]any{"fs": map[string]any{"read": []any{"b.txt", "a.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := starlarkhost.ParseCapabilities(map[string]any{"fs": map[string]any{"read": []any{"a.txt", "b.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if CodeactCapabilityHash(first) != CodeactCapabilityHash(second) {
		t.Fatal("equivalent capabilities must hash identically")
	}
}
