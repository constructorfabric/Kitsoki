package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileSetupHandlerDiscoverAndApply(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".kitsoki.yaml"), []byte("default_profile: base\nharness_profiles:\n  base:\n    backend: codex\n    model: test-model\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	discovered, err := ProfileSetupHandler(context.Background(), map[string]any{"op": "discover", "target_path": root})
	if err != nil {
		t.Fatal(err)
	}
	if discovered.Error != "" || discovered.Data["default_profile"] != "base" {
		t.Fatalf("discover = %#v", discovered)
	}
	result, err := ProfileSetupHandler(context.Background(), map[string]any{
		"op": "apply", "target_path": root,
		"candidate": map[string]any{"action": "upsert_openai", "name": "local", "backend": "codex", "model": "test-model", "env_var": "OPENAI_API_KEY"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || result.Data["status"] != "applied" {
		t.Fatalf("apply = %#v", result)
	}
	raw, err := os.ReadFile(filepath.Join(root, ".kitsoki.local.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !containsAll(text, "default_profile: local", "local:", "${OPENAI_API_KEY}") {
		t.Fatalf("local config lost expected content:\n%s", text)
	}
}

func TestProfileSetupHandlerRejectsRawSecret(t *testing.T) {
	result, err := ProfileSetupHandler(context.Background(), map[string]any{
		"op": "apply", "target_path": t.TempDir(),
		"candidate": map[string]any{"action": "upsert_openai", "name": "bad", "backend": "codex", "model": "test", "env_var": "sk-raw-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error != "" || !containsAll(fmt.Sprint(result.Data["error"]), "env var", "raw key") {
		t.Fatalf("result = %#v", result)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
