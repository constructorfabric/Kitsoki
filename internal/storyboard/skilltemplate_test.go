package storyboard

import (
	"path/filepath"
	"runtime"
	"testing"
)

// TestSkillTemplateValidates keeps the worked example shipped with the
// kitsoki-ui-demo skill lint-clean against the real repo — if the story/flow
// it binds to moves, or a lint rule tightens, this catches the drift.
func TestSkillTemplateValidates(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate source file")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	template := filepath.Join(root, ".agents", "skills", "kitsoki-ui-demo", "templates", "storyboard.example.yaml")

	sb, err := Load(template)
	if err != nil {
		t.Fatal(err)
	}
	issues := sb.Validate(ValidateOptions{Root: root})
	if len(issues) != 0 {
		t.Fatalf("skill template must validate with zero issues, got: %v", issues)
	}
}
