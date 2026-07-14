package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
	"kitsoki/internal/kit"
	"kitsoki/internal/kitendpoint"
	"kitsoki/internal/kitverify"
)

// TestObjectGraphKit_Verify runs `kitsoki kit verify kits/object-graph`'s
// engine (kitverify.VerifyKit, bypassing the CLI's os.Exit) end to end — the
// S5 acceptance check that @kitsoki/object-graph's contract checks and its
// no-LLM conformance flow (flows/basic.yaml, driven against the kit's own
// example/catalog.yaml) actually pass.
//
// flows/basic.yaml's catalog_path is repo-root-relative (matching the
// existing `kitsoki graph lint <path>` / `make features` convention, both
// invoked from the repo root), so this test chdirs to the repo root for the
// duration of the call.
func TestObjectGraphKit_Verify(t *testing.T) {
	repoRoot := repoRootFromCmdKitsoki(t)
	restoreCwd := chdir(t, repoRoot)
	defer restoreCwd()

	report, err := kitverify.VerifyKit("kits/object-graph", kitverify.Options{})
	if err != nil {
		t.Fatalf("VerifyKit: %v", err)
	}
	if !report.OK() {
		for _, sr := range report.Stories {
			for _, iss := range sr.Issues {
				t.Errorf("story %s: %s", sr.Story, iss)
			}
		}
		for _, pi := range report.ParamIssues {
			t.Errorf("param issue: %s", pi)
		}
		for _, f := range report.Flows {
			if f.Err != nil {
				t.Errorf("flow pattern %s: %v", f.Pattern, f.Err)
			}
			if f.Report != nil && f.Report.Failed > 0 {
				t.Errorf("flow pattern %s: %d failed", f.Pattern, f.Report.Failed)
				for _, res := range f.Report.Results {
					if res.Passed {
						continue
					}
					for _, turn := range res.Turns {
						if !turn.Passed {
							t.Errorf("  %s turn %d: %v", res.File, turn.TurnIndex, turn.Failures)
						}
					}
				}
			}
		}
		t.Fatalf("report not OK: %+v", report)
	}
}

// TestObjectGraphKit_DispatchSurface exercises kit.object-graph.graph.<op>
// through the SAME internal/kitendpoint.Dispatcher the JSON-RPC
// kit.<kit>.<iface>.<op> fallback and the kit_call MCP tool both call —
// proving the S5 acceptance bar's "served via JSON-RPC + kit_call" claim at
// the one mechanism both carriers share, without needing a live server.
// Covers both the Go-backed ops (load/lint) and the starlark-backed
// "presentation" op (D2.1's concrete proof), including that _kit_dir really
// does get injected (dispatched through the surface) vs. missing (a bare
// GraphHandler call, covered separately in internal/host).
func TestObjectGraphKit_DispatchSurface(t *testing.T) {
	repoRoot := repoRootFromCmdKitsoki(t)

	manifest, err := kit.LoadDir(filepath.Join(repoRoot, "kits", "object-graph"))
	if err != nil {
		t.Fatalf("kit.LoadDir: %v", err)
	}
	kits := kit.NewRegistry()
	if err := kits.Add(manifest); err != nil {
		t.Fatalf("kits.Add: %v", err)
	}
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	d := kitendpoint.NewDispatcher(kits, reg)

	ctx := context.Background()
	catalogPath := filepath.Join(repoRoot, "kits", "object-graph", "example", "catalog.yaml")

	loadRes, err := d.Call(ctx, "object-graph", "graph", "load", map[string]any{"catalog_path": catalogPath})
	if err != nil {
		t.Fatalf("Call(graph.load): %v", err)
	}
	if loadRes.Error != "" {
		t.Fatalf("Call(graph.load): result error: %s", loadRes.Error)
	}
	if got, want := loadRes.Data["node_count"], 3; got != want {
		t.Errorf("node_count = %v, want %v", got, want)
	}

	presRes, err := d.Call(ctx, "object-graph", "graph", "presentation", map[string]any{})
	if err != nil {
		t.Fatalf("Call(graph.presentation): %v", err)
	}
	if presRes.Error != "" {
		t.Fatalf("Call(graph.presentation): result error: %s", presRes.Error)
	}
	layers, ok := presRes.Data["layers"].([]any)
	if !ok || len(layers) == 0 {
		t.Fatalf("Call(graph.presentation): expected non-empty layers list, got %#v", presRes.Data["layers"])
	}
	first, ok := layers[0].(map[string]any)
	if !ok || first["id"] != "actors" {
		t.Errorf("layers[0] = %#v, want id=actors", layers[0])
	}
}

// repoRootFromCmdKitsoki returns the repo root, two levels up from this
// package's directory (repo-root/cmd/kitsoki).
func repoRootFromCmdKitsoki(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("computed repo root %q has no go.mod: %v", root, err)
	}
	return root
}

// chdir changes the process's working directory to dir and returns a
// restore func. Not safe to run in parallel with other tests that also
// change the working directory (none of this package's do).
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q): %v", dir, err)
	}
	return func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restore Chdir(%q): %v", orig, err)
		}
	}
}
