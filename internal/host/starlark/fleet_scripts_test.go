package starlark_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

func TestFleetLoadBriefKeyMapping(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "stories/fleet/scripts/fleet_load.star"))
	if err != nil {
		t.Fatalf("read fleet_load.star: %v", err)
	}

	tmp := t.TempDir()
	manifest := "decomposition.yaml"
	if err := os.WriteFile(filepath.Join(tmp, manifest), []byte(`briefs:
  - id: x
    brief: a sufficiently long brief string
    gate_command: go build ./...
`), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := starlarkhost.WithInspector(context.Background(), starlarkhost.NewProductionInspector(tmp))
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "fleet_load.star",
		Source:       src,
		Capabilities: inspectCaps(),
		Inputs: map[string]any{
			"decomposition_path": manifest,
			"fleet_state_path":   "",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	briefs, ok := res.Outputs["fleet_briefs"].([]any)
	if !ok || len(briefs) != 1 {
		t.Fatalf("fleet_briefs = %#v, want one entry", res.Outputs["fleet_briefs"])
	}
	first, ok := briefs[0].(map[string]any)
	if !ok {
		t.Fatalf("fleet_briefs[0] = %#v, want object", briefs[0])
	}
	if got := first["brief"]; got != "a sufficiently long brief string" {
		t.Fatalf("brief = %#v, want brief key value", got)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
}
