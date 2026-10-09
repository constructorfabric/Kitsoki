package orchestrator_test

import (
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// writeStory writes a minimal on-disk story with one agent prompt and returns
// the app.yaml path.
func writeStory(t *testing.T, promptBody string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "prompts", "ask.md"), []byte(promptBody), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}
	appYAML := `app: { id: t, version: "1" }
hosts: [host.agent.ask]
world:
  out: { type: string, default: "" }
root: idle
states:
  idle:
    view: "hi"
    on_enter:
      - invoke: host.agent.ask
        with:
          prompt: prompts/ask.md
        bind:
          out: stdout
`
	p := filepath.Join(dir, "app.yaml")
	if err := os.WriteFile(p, []byte(appYAML), 0o644); err != nil {
		t.Fatalf("write app.yaml: %v", err)
	}
	return p
}

func orchFor(t *testing.T, appPath string) *orchestrator.Orchestrator {
	t.Helper()
	def, err := app.Load(appPath)
	if err != nil {
		t.Fatalf("app.Load: %v", err)
	}
	m, err := machine.New(def)
	if err != nil {
		t.Fatalf("machine.New: %v", err)
	}
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("store.OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return orchestrator.New(def, m, s, noopOrchestratorHarness{})
}

// TestValidatePromptExtensions_GoodAndBad: a story whose prompt is valid passes
// the load-time pass; one whose prompt {% extends %} a missing target fails
// with a located message — at load, not at first dispatch.
func TestValidatePromptExtensions_Good(t *testing.T) {
	orch := orchFor(t, writeStory(t, "Hello {% block spec_x %}{% endblock %}"))
	if errs := orch.ValidatePromptExtensions(); len(errs) != 0 {
		t.Fatalf("valid prompt should pass, got: %v", errs)
	}
}

func TestValidatePromptExtensions_Bad(t *testing.T) {
	orch := orchFor(t, writeStory(t, `{% extends "@story/prompts/missing.md" %}`))
	errs := orch.ValidatePromptExtensions()
	if len(errs) == 0 {
		t.Fatal("unresolved extends should fail load-time validation")
	}
	if err := orchestrator.PromptValidationError(errs); err == nil {
		t.Fatal("PromptValidationError should wrap a non-empty error")
	}
}

// TestValidatePromptExtensions_MissingOverlay: a configured overlay dir that
// doesn't exist is caught (otherwise the overlay silently no-ops).
func TestValidatePromptExtensions_MissingOverlay(t *testing.T) {
	appPath := writeStory(t, "ok {% block spec_x %}{% endblock %}")
	def, err := app.Load(appPath)
	if err != nil {
		t.Fatalf("app.Load: %v", err)
	}
	m, err := machine.New(def)
	if err != nil {
		t.Fatalf("machine.New: %v", err)
	}
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatalf("store.OpenMemory: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	orch := orchestrator.New(def, m, s, noopOrchestratorHarness{},
		orchestrator.WithPromptOverlay(filepath.Join(t.TempDir(), "nonexistent")))
	if errs := orch.ValidatePromptExtensions(); len(errs) == 0 {
		t.Fatal("a missing overlay dir should fail validation")
	}
}

// TestValidatePromptExtensions_RealStories guards against false positives: the
// shipped stories (including oregon-trail's @import overlay-extend and its
// imported children) must pass the load-time pass, or `kitsoki run` would
// reject valid stories.
func TestValidatePromptExtensions_RealStories(t *testing.T) {
	for _, rel := range []string{"bugfix", "dev-story", "oregon-trail", "frontier_event"} {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			appPath, _ := filepath.Abs(filepath.Join("..", "..", "stories", rel, "app.yaml"))
			if _, err := os.Stat(appPath); err != nil {
				t.Skipf("story %s not present", rel)
			}
			orch := orchFor(t, appPath)
			if errs := orch.ValidatePromptExtensions(); len(errs) != 0 {
				t.Fatalf("real story %s should pass prompt validation, got: %v", rel, errs)
			}
		})
	}
}

func TestValidatePromptExtensions_ProjectKitsokiDev(t *testing.T) {
	appPath, _ := filepath.Abs(filepath.Join("..", "..", ".kitsoki", "stories", "kitsoki-dev", "app.yaml"))
	if _, err := os.Stat(appPath); err != nil {
		t.Skipf("project kitsoki-dev story not present: %v", err)
	}
	orch := orchFor(t, appPath)
	if errs := orch.ValidatePromptExtensions(); len(errs) != 0 {
		t.Fatalf("project kitsoki-dev story should pass prompt validation, got: %v", errs)
	}
}
