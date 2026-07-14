package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPunchVerifyHandlerRunsDeterministicCommand(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := map[string]any{"items": []any{map[string]any{
		"id":     "demo",
		"verify": []any{map[string]any{"kind": "command", "cmd": "printf verified"}},
	}}}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := PunchVerifyHandler(context.Background(), map[string]any{
		"state_path": statePath,
		"item_id":    "demo",
	})
	if err != nil {
		t.Fatal(err)
	}
	verify := result.Data["verify_result"].(map[string]any)
	if verify["status"] != "passed" {
		t.Fatalf("status = %v, want passed", verify["status"])
	}
	checks := verify["checks"].([]any)
	if checks[0].(map[string]any)["output"] != "verified" {
		t.Fatalf("output = %v, want verified", checks[0].(map[string]any)["output"])
	}
}

func TestPunchVerifyHandlerBlocksLiveLLMCommand(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	state := map[string]any{"items": []any{map[string]any{
		"id":     "demo",
		"verify": []any{map[string]any{"kind": "command", "cmd": "codex exec --live"}},
	}}}
	raw, _ := json.Marshal(state)
	if err := os.WriteFile(statePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := PunchVerifyHandler(context.Background(), map[string]any{"state_path": statePath, "item_id": "demo"})
	if err != nil {
		t.Fatal(err)
	}
	verify := result.Data["verify_result"].(map[string]any)
	if verify["status"] != "failed" {
		t.Fatalf("status = %v, want failed", verify["status"])
	}
}

func TestPunchVerifyStoryScriptUsesAllowListedHostFunction(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	raw := []byte(`{"items":[{"id":"demo","verify":[{"kind":"command","cmd":"printf from-story"}]}]}`)
	if err := os.WriteFile(statePath, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join(root, "..", "..", "stories", "punch-list", "scripts", "punch_verify.star"))
	if err != nil {
		t.Fatal(err)
	}

	registry := NewRegistry()
	RegisterBuiltins(registry)
	handler := NewStarlarkRunHandler(registry)
	result, err := handler(context.Background(), map[string]any{
		"script": script,
		"inputs": map[string]any{"state_path": statePath, "item_id": "demo"},
		"capabilities": map[string]any{
			"host": map[string]any{"verbs": []any{"host.punch.verify"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" {
		t.Fatalf("story handler error: %s", result.Error)
	}
	verify := result.Data["verify_result"].(map[string]any)
	if verify["status"] != "passed" {
		t.Fatalf("status = %v, want passed", verify["status"])
	}
}
