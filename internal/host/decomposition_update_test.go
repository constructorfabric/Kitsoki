package host

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecompositionUpdateSelfTest(t *testing.T) {
	result, err := DecompositionUpdateHandler(context.Background(), map[string]any{"op": "self-test"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Data["ok"] != true || result.Data["route"] != "ok" {
		t.Fatalf("self-test = %#v", result)
	}
}

func TestDecompositionUpdateRejectsMissingProvenanceWithoutWrites(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base.yaml")
	delta := filepath.Join(root, "delta.yaml")
	if err := os.WriteFile(base, []byte("changes:\n  - id: base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(delta, []byte("trigger: test\noperations:\n  - op: add_change\n    change: {id: added}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "out.yaml")
	result, err := DecompositionUpdateHandler(context.Background(), map[string]any{
		"base":         base,
		"delta":        delta,
		"out":          out,
		"versions_dir": filepath.Join(root, "versions"),
		"event_log":    filepath.Join(root, "events.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["route"] != "fail" || result.Data["ok"] != false {
		t.Fatalf("result = %#v", result)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("rejected transaction wrote output: %v", err)
	}
}

func TestDecompositionUpdateRejectsDependencyCycle(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "base.yaml")
	delta := filepath.Join(root, "delta.yaml")
	if err := os.WriteFile(base, []byte("changes:\n  - id: base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(delta, []byte("trigger: test\nprovenance: {kind: test, ref: fixture}\noperations:\n  - op: add_change\n    change: {id: cycle, depends_on: [cycle]}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := DecompositionUpdateHandler(context.Background(), map[string]any{
		"base":         base,
		"delta":        delta,
		"out":          filepath.Join(root, "out.yaml"),
		"versions_dir": filepath.Join(root, "versions"),
		"event_log":    filepath.Join(root, "events.jsonl"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Data["route"] != "fail" || !strings.Contains(stringValue(result.Data, "error"), "dependency cycle") {
		t.Fatalf("result = %#v", result)
	}
}
