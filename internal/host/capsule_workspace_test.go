package host_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/host"
)

func TestCapsuleWorkspace_CreateGetClose(t *testing.T) {
	project := t.TempDir()
	writeSyntheticCapsuleDefinition(t, project, "clean")

	ctx := context.Background()
	created, err := host.CapsuleWorkspaceHandler(ctx, map[string]any{
		"op":         "create",
		"repo":       project,
		"id":         "run",
		"definition": "clean",
		"owner":      "agent",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if created.Error != "" {
		t.Fatalf("domain: %s", created.Error)
	}
	if created.Data["id"] != "run" {
		t.Fatalf("id: %v", created.Data["id"])
	}
	if created.Data["ok"] != true {
		t.Fatalf("created ok: %v", created.Data)
	}
	if created.Data["diagnostics"] == nil {
		t.Fatalf("created diagnostics missing: %v", created.Data)
	}
	path, _ := created.Data["path"].(string)
	wantRoot := filepath.Join(project, ".capsules", "workspaces")
	if realRoot, err := filepath.EvalSymlinks(wantRoot); err == nil {
		wantRoot = realRoot
	}
	if !filepath.IsAbs(path) || filepath.Dir(path) != wantRoot {
		t.Fatalf("path %q not under %q", path, wantRoot)
	}
	if _, err := os.Stat(filepath.Join(path, "initial.txt")); err != nil {
		t.Fatalf("workspace materialization: %v", err)
	}

	got, err := host.CapsuleWorkspaceHandler(ctx, map[string]any{
		"op":    "get",
		"repo":  project,
		"id":    "run",
		"owner": "agent",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if got.Error != "" {
		t.Fatalf("domain: %s", got.Error)
	}
	if got.Data["path"] != path {
		t.Fatalf("get path: %v", got.Data["path"])
	}
	if fmt.Sprint(got.Data["state"]) != "ready" {
		t.Fatalf("state: %v", got.Data["state"])
	}

	listed, err := host.CapsuleWorkspaceHandler(ctx, map[string]any{
		"op":    "list",
		"repo":  project,
		"owner": "agent",
	})
	if err != nil {
		t.Fatalf("list infra: %v", err)
	}
	if listed.Error != "" {
		t.Fatalf("list domain: %s", listed.Error)
	}
	workspaces, _ := listed.Data["workspaces"].([]map[string]any)
	if len(workspaces) != 1 || workspaces[0]["id"] != "run" || workspaces[0]["path"] != path {
		t.Fatalf("list workspaces: %v", listed.Data["workspaces"])
	}

	cleanup, err := host.CapsuleWorkspaceHandler(ctx, map[string]any{
		"op":    "cleanup_scan",
		"repo":  project,
		"owner": "agent",
	})
	if err != nil {
		t.Fatalf("cleanup_scan infra: %v", err)
	}
	if cleanup.Error != "" {
		t.Fatalf("cleanup_scan domain: %s", cleanup.Error)
	}
	candidates, _ := cleanup.Data["candidates"].([]map[string]any)
	if len(candidates) != 1 || candidates[0]["id"] != "run" || candidates[0]["recommended"] == true {
		t.Fatalf("cleanup candidates: %v", cleanup.Data["candidates"])
	}
	if cleanup.Data["recommended_count"] != 0 {
		t.Fatalf("recommended_count: %v", cleanup.Data["recommended_count"])
	}

	for _, op := range []string{"status", "sync"} {
		status, err := host.CapsuleWorkspaceHandler(ctx, map[string]any{
			"op":    op,
			"repo":  project,
			"id":    "run",
			"owner": "agent",
		})
		if err != nil {
			t.Fatalf("%s infra: %v", op, err)
		}
		if status.Error != "" {
			t.Fatalf("%s domain: %s", op, status.Error)
		}
		if status.Data["path"] != path || status.Data["diagnostics"] == nil {
			t.Fatalf("%s diagnostics/path: %v", op, status.Data)
		}
		if op == "sync" && status.Data["log"] == "" {
			t.Fatalf("sync log missing: %v", status.Data)
		}
	}

	closed, err := host.CapsuleWorkspaceHandler(ctx, map[string]any{
		"op":    "close",
		"repo":  project,
		"id":    "run",
		"owner": "agent",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if closed.Error != "" {
		t.Fatalf("domain: %s", closed.Error)
	}
	if closed.Data["ok"] != true {
		t.Fatalf("close: %v", closed.Data)
	}
	if closed.Data["closed"] != true || closed.Data["diagnostics"] == nil {
		t.Fatalf("close diagnostics: %v", closed.Data)
	}
}

func TestCapsuleWorkspace_CreateDefaultsToDevelopmentDefinition(t *testing.T) {
	project := t.TempDir()
	writeSyntheticCapsuleDefinition(t, project, "development")

	created, err := host.CapsuleWorkspaceHandler(context.Background(), map[string]any{
		"op":    "create",
		"repo":  project,
		"id":    "legacy-contract",
		"owner": "agent",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if created.Error != "" {
		t.Fatalf("domain: %s", created.Error)
	}
	if created.Data["definition"] != "development" {
		t.Fatalf("definition: %v", created.Data["definition"])
	}
	diagnostics, _ := created.Data["diagnostics"].(map[string]any)
	if diagnostics["definition_defaulted"] != true {
		t.Fatalf("definition_defaulted missing: %v", diagnostics)
	}
}

func TestCapsuleWorkspace_ErrorCarriesDiagnostics(t *testing.T) {
	result, err := host.CapsuleWorkspaceHandler(context.Background(), map[string]any{
		"op":   "create",
		"repo": t.TempDir(),
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if result.Error == "" {
		t.Fatalf("expected domain error: %v", result.Data)
	}
	diagnostics, _ := result.Data["diagnostics"].(map[string]any)
	if diagnostics["hint"] == "" || diagnostics["handler"] != "host.capsule_workspace" {
		t.Fatalf("diagnostics: %v", diagnostics)
	}
}

func writeSyntheticCapsuleDefinition(t *testing.T, project, id string) {
	t.Helper()
	spec := filepath.Join(project, ".kitsoki", "capsules", id+".yaml")
	if err := os.MkdirAll(filepath.Dir(spec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec, []byte("schema: capsule-definition/v1\nid: "+id+"\nsource:\n  kind: synthetic\n  synthetic_spec: capsules/"+id+"/capsule.yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacySpec := filepath.Join(project, "capsules", id, "capsule.yaml")
	if err := os.MkdirAll(filepath.Dir(legacySpec), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacySpec, []byte("name: "+id+"\nsource:\n  synthetic: true\n  steps:\n    - action: write\n      path: initial.txt\n      content: initial\n    - action: commit\n      message: init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
