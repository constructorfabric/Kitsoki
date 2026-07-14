package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCapsuleCIProjectChecksBuildsTypedVerdictAndEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kitsoki"), 0o755); err != nil {
		t.Fatal(err)
	}
	profile := "schema: project-profile/v1\nid: example\ncommands:\n  test: go test ./...\n  build: go build ./...\n"
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "project-profile.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := CapsuleCICommandRunnerFunc(func(_ context.Context, _ string, command string) (string, int, error) {
		if command == "go build ./..." {
			return "compile failed\n", 1, nil
		}
		return "ok\n", 0, nil
	})
	result, err := NewCapsuleCIProjectChecksHandler(runner)(context.Background(), map[string]any{"workdir": root, "job_id": "job/unsafe", "pipeline": "change", "source_digest": "source", "story_digest": "story", "environment_digest": "environment", "envelope_digest": "envelope"})
	if err != nil || result.Error != "" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	verdict := result.Data["verdict"].(map[string]any)
	if verdict["outcome"] != "failed" || verdict["promotion_eligible"] != false {
		t.Fatalf("verdict = %#v", verdict)
	}
	evidence := filepath.Join(root, ".artifacts", "capsule-ci", "checks", "jobunsafe.json")
	raw, err := os.ReadFile(evidence)
	if err != nil {
		t.Fatal(err)
	}
	var artifact map[string]any
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact["outcome"] != "failed" {
		t.Fatalf("artifact = %#v", artifact)
	}
}

func TestCapsuleCIProjectChecksParksWhenCommandsAreMissing(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kitsoki"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kitsoki", "project-profile.yaml"), []byte("schema: project-profile/v1\nid: empty\ncommands: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := NewCapsuleCIProjectChecksHandler(CapsuleCICommandRunnerFunc(func(context.Context, string, string) (string, int, error) {
		t.Fatal("runner should not be called")
		return "", 0, nil
	}))(context.Background(), map[string]any{"workdir": root, "pipeline": "change"})
	if err != nil {
		t.Fatal(err)
	}
	verdict := result.Data["verdict"].(map[string]any)
	if verdict["outcome"] != "needs_input" || verdict["promotion_eligible"] != false {
		t.Fatalf("verdict = %#v", verdict)
	}
}
