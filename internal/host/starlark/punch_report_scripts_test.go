package starlark_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	goyaml "github.com/goccy/go-yaml"

	starlarkhost "kitsoki/internal/host/starlark"
)

func TestPunchReportScript(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "stories/punch-list/scripts/punch_report.star"))
	if err != nil {
		t.Fatalf("read punch_report.star: %v", err)
	}
	sidecar, err := starlarkhost.LoadSidecar(filepath.Join(root, "stories/punch-list/scripts/punch_report.star.yaml"))
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	casBytes, err := os.ReadFile(filepath.Join(root, "stories/punch-list/cassettes/punch_report.inspect.yaml"))
	if err != nil {
		t.Fatalf("read cassette: %v", err)
	}
	var cas starlarkhost.InspectCassette
	if err := goyaml.Unmarshal(casBytes, &cas); err != nil {
		t.Fatalf("unmarshal cassette: %v", err)
	}

	ctx := starlarkhost.WithInspector(context.Background(), starlarkhost.NewReplayInspector(&cas))
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "punch_report.star",
		Source:       src,
		Sidecar:      sidecar,
		Capabilities: inspectCaps(),
		Inputs: map[string]any{
			"state_path": ".artifacts/punch-list/report-fixture.state.json",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["report_path"]; got != ".artifacts/punch-list/report.md" {
		t.Fatalf("report_path = %#v, want report path", got)
	}
	if got := res.Outputs["summary"]; got != "1 passed, 0 partial, 1 failed, 0 skipped, 0 pending" {
		t.Fatalf("summary = %#v", got)
	}
	if len(res.Inspections) != 2 {
		t.Fatalf("inspections = %d, want read + write", len(res.Inspections))
	}
}

func TestPunchPolicyScript(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "stories/punch-list/scripts/punch_policy.star"))
	if err != nil {
		t.Fatalf("read punch_policy.star: %v", err)
	}
	sidecar, err := starlarkhost.LoadSidecar(filepath.Join(root, "stories/punch-list/scripts/punch_policy.star.yaml"))
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	casBytes, err := os.ReadFile(filepath.Join(root, "stories/punch-list/cassettes/punch_policy.inspect.yaml"))
	if err != nil {
		t.Fatalf("read cassette: %v", err)
	}
	var cas starlarkhost.InspectCassette
	if err := goyaml.Unmarshal(casBytes, &cas); err != nil {
		t.Fatalf("unmarshal cassette: %v", err)
	}

	ctx := starlarkhost.WithInspector(context.Background(), starlarkhost.NewReplayInspector(&cas))
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "punch_policy.star",
		Source:       src,
		Sidecar:      sidecar,
		Capabilities: inspectCaps(),
		Inputs: map[string]any{
			"state_path": ".artifacts/punch-list/policy-fixture.state.json",
			"item_id":    "policy-demo",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	policy, ok := res.Outputs["policy_result"].(map[string]any)
	if !ok {
		t.Fatalf("policy_result = %#v, want object", res.Outputs["policy_result"])
	}
	if got := policy["status"]; got != "ok" {
		t.Fatalf("policy status = %#v, want ok", got)
	}
	if got := res.Outputs["error"]; got != "" {
		t.Fatalf("error = %#v, want empty", got)
	}
}

func TestPunchBoardScript(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "stories/punch-list/scripts/punch_board.star"))
	if err != nil {
		t.Fatalf("read punch_board.star: %v", err)
	}
	sidecar, err := starlarkhost.LoadSidecar(filepath.Join(root, "stories/punch-list/scripts/punch_board.star.yaml"))
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	casBytes, err := os.ReadFile(filepath.Join(root, "stories/punch-list/cassettes/punch_board.inspect.yaml"))
	if err != nil {
		t.Fatalf("read cassette: %v", err)
	}
	var cas starlarkhost.InspectCassette
	if err := goyaml.Unmarshal(casBytes, &cas); err != nil {
		t.Fatalf("unmarshal cassette: %v", err)
	}

	ctx := starlarkhost.WithInspector(context.Background(), starlarkhost.NewReplayInspector(&cas))
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "punch_board.star",
		Source:       src,
		Sidecar:      sidecar,
		Capabilities: inspectCaps(),
		Inputs: map[string]any{
			"state_path":  ".artifacts/punch-list/board-fixture.state.json",
			"mark_id":     "first",
			"mark_status": "passed",
			"mark_error":  "ok summary",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["route"]; got != "dispatch" {
		t.Fatalf("route = %#v, want dispatch", got)
	}
	if got := res.Outputs["processed_count"]; got != int64(1) {
		t.Fatalf("processed_count = %#v, want 1", got)
	}
	if got := res.Outputs["passed_count"]; got != int64(1) {
		t.Fatalf("passed_count = %#v, want 1", got)
	}
	if got := res.Outputs["pending_count"]; got != int64(1) {
		t.Fatalf("pending_count = %#v, want 1", got)
	}
	if got := res.Outputs["next_item_id"]; got != "second" {
		t.Fatalf("next_item_id = %#v, want second", got)
	}
	if len(res.Inspections) != 3 {
		t.Fatalf("inspections = %d, want exists + read + write", len(res.Inspections))
	}
}

func TestPunchLoadScript(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "stories/punch-list/scripts/punch_load.star"))
	if err != nil {
		t.Fatalf("read punch_load.star: %v", err)
	}
	sidecar, err := starlarkhost.LoadSidecar(filepath.Join(root, "stories/punch-list/scripts/punch_load.star.yaml"))
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(root, "stories/punch-list/testdata/two_items.yaml"))
	if err != nil {
		t.Fatalf("read fixture manifest: %v", err)
	}

	work := t.TempDir()
	for _, dir := range []string{
		"stories/punch-list/testdata",
		"stories/punch-list",
		"stories/cherny-loop",
	} {
		if err := os.MkdirAll(filepath.Join(work, dir), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(work, "stories/punch-list/testdata/two_items.yaml"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for _, app := range []string{
		"stories/punch-list/app.yaml",
		"stories/cherny-loop/app.yaml",
	} {
		if err := os.WriteFile(filepath.Join(work, app), []byte("name: fixture\n"), 0o644); err != nil {
			t.Fatalf("write %s: %v", app, err)
		}
	}

	ctx := starlarkhost.WithInspector(context.Background(), starlarkhost.NewProductionInspector(work))
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "punch_load.star",
		Source:       src,
		Sidecar:      sidecar,
		Capabilities: inspectCaps(),
		Inputs: map[string]any{
			"manifest_path": "two items",
			"state_path":    ".artifacts/punch-list/load-fixture.state.json",
			"run_id":        "test-run",
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := res.Outputs["manifest_path"]; got != "stories/punch-list/testdata/two_items.yaml" {
		t.Fatalf("manifest_path = %#v, want resolved testdata manifest", got)
	}
	if got := res.Outputs["item_count"]; got != "2" {
		t.Fatalf("item_count = %#v, want 2", got)
	}
	if got := res.Outputs["error"]; got != "" {
		t.Fatalf("error = %#v, want empty", got)
	}

	stateBytes, err := os.ReadFile(filepath.Join(work, ".artifacts/punch-list/load-fixture.state.json"))
	if err != nil {
		t.Fatalf("read written state: %v", err)
	}
	var state map[string]any
	if err := goyaml.Unmarshal(stateBytes, &state); err != nil {
		t.Fatalf("unmarshal written state: %v", err)
	}
	if got := state["run_id"]; got != "test-run" {
		t.Fatalf("run_id = %#v, want test-run", got)
	}
	items, _ := state["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if len(res.Inspections) < 5 {
		t.Fatalf("inspections = %d, want read/exists/glob/write activity", len(res.Inspections))
	}
}
