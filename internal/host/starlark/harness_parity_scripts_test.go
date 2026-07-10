package starlark_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

func TestHarnessParityReportScript(t *testing.T) {
	root := repoRoot(t)
	src, err := os.ReadFile(filepath.Join(root, "stories/harness-parity-qa/scripts/parity_report.star"))
	if err != nil {
		t.Fatalf("read parity_report.star: %v", err)
	}
	sidecar, err := starlarkhost.LoadSidecar(filepath.Join(root, "stories/harness-parity-qa/scripts/parity_report.star.yaml"))
	if err != nil {
		t.Fatalf("LoadSidecar: %v", err)
	}

	work := t.TempDir()
	ctx := starlarkhost.WithInspector(context.Background(), starlarkhost.NewProductionInspector(work))
	res, err := starlarkhost.Run(ctx, starlarkhost.Params{
		Script:       "parity_report.star",
		Source:       src,
		Sidecar:      sidecar,
		Capabilities: inspectCaps(),
		Inputs: map[string]any{
			"surfaces":      "tui,web,vscode",
			"output_root":   ".artifacts/harness-parity-qa",
			"markdown_path": ".context/harness-parity-qa.md",
			"visual_policy": "deterministic",
			"provider_result": map[string]any{
				"ok":        true,
				"exit_code": 0,
				"stdout":    "ok provider\n",
			},
			"tui_result": map[string]any{
				"ok":        true,
				"exit_code": 0,
				"stdout":    "ok tui\n",
			},
			"web_result": map[string]any{
				"ok":        true,
				"exit_code": 0,
				"stdout":    "ok web\n",
			},
			"vscode_result": map[string]any{
				"ok":        true,
				"exit_code": 0,
				"stdout":    "ok vscode\n",
			},
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	result, ok := res.Outputs["parity_result"].(map[string]any)
	if !ok {
		t.Fatalf("parity_result = %T, want object", res.Outputs["parity_result"])
	}
	if got := result["passed"]; got != true {
		t.Fatalf("passed = %#v, want true", got)
	}
	if got := result["summary_path"]; got != ".artifacts/harness-parity-qa/summary.json" {
		t.Fatalf("summary_path = %#v", got)
	}

	summary, err := os.ReadFile(filepath.Join(work, ".artifacts/harness-parity-qa/summary.json"))
	if err != nil {
		t.Fatalf("read summary: %v", err)
	}
	if !strings.Contains(string(summary), `"passed":true`) {
		t.Fatalf("summary missing passed=true: %s", summary)
	}
	md, err := os.ReadFile(filepath.Join(work, ".context/harness-parity-qa.md"))
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	if !strings.Contains(string(md), "# Harness Parity QA") {
		t.Fatalf("markdown missing title: %s", md)
	}
	if len(res.Inspections) != 2 {
		t.Fatalf("inspections = %d, want two writes", len(res.Inspections))
	}
}
