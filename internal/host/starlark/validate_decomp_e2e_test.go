package starlark_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	starlarkhost "kitsoki/internal/host/starlark"
)

// scriptDir returns the absolute path to the work-decomposition scripts dir,
// resolved relative to this test file regardless of where `go test` is run.
func scriptDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// internal/host/starlark → repo root → .agents/skills/work-decomposition/scripts
	root := filepath.Join(filepath.Dir(file), "..", "..", "..")
	return filepath.Join(root, ".agents", "skills", "work-decomposition", "scripts")
}

func loadValidateDecompScript(t *testing.T) (src []byte, sidecar *starlarkhost.Sidecar) {
	t.Helper()
	dir := scriptDir(t)
	src, err := os.ReadFile(filepath.Join(dir, "validate_decomposition.star"))
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "validate_decomposition.star.yaml"))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	sidecar, err = starlarkhost.ParseSidecar(raw)
	if err != nil {
		t.Fatalf("parse sidecar: %v", err)
	}
	return src, sidecar
}

func runValidate(t *testing.T, src []byte, sidecar *starlarkhost.Sidecar, manifest any) map[string]any {
	t.Helper()
	res, err := starlarkhost.Run(context.Background(), starlarkhost.Params{
		Script:  "validate_decomposition.star",
		Source:  src,
		Sidecar: sidecar,
		Inputs:  map[string]any{"manifest": manifest},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res.Outputs
}

func assertOK(t *testing.T, out map[string]any) {
	t.Helper()
	if ok, _ := out["ok"].(bool); !ok {
		t.Errorf("expected ok=true, got errors: %v", out["errors"])
	}
}

func assertErrors(t *testing.T, out map[string]any, wantSubstrings ...string) {
	t.Helper()
	if ok, _ := out["ok"].(bool); ok {
		t.Error("expected ok=false, got ok=true")
		return
	}
	errs, _ := out["errors"].([]any)
	for _, want := range wantSubstrings {
		found := false
		for _, e := range errs {
			if s, ok := e.(string); ok {
				if contains(s, want) {
					found = true
					break
				}
			}
		}
		if !found {
			t.Errorf("want error containing %q; got: %v", want, errs)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}

// minimalBrief returns a brief map with all required fields populated.
func minimalBrief(id string) map[string]any {
	return map[string]any{
		"id":         id,
		"title":      "title for " + id,
		"kind":       "runtime",
		"goal":       "something meaningful and long enough",
		"scope":      []any{"internal/" + id + "/"},
		"depends_on": []any{},
		"acceptance": []any{"it works"},
		"test_plan":  "go test ./internal/" + id + "/...",
		"agent_brief": "do the thing: implement the feature in internal/" + id +
			" with the full context that the implementer needs",
	}
}

func validManifest() map[string]any {
	a := minimalBrief("aaa")
	b := minimalBrief("bbb")
	b["depends_on"] = []any{"aaa"}
	return map[string]any{
		"coverage_note": "aaa and bbb together cover the whole proposal with no gaps.",
		"briefs":        []any{a, b},
	}
}

// TestValidateDecomp_CleanManifest passes a well-formed manifest and expects ok=true.
func TestValidateDecomp_CleanManifest(t *testing.T) {
	src, sidecar := loadValidateDecompScript(t)
	out := runValidate(t, src, sidecar, validManifest())
	assertOK(t, out)
	errs, _ := out["errors"].([]any)
	if len(errs) != 0 {
		t.Errorf("expected empty errors, got %v", errs)
	}
}

// TestValidateDecomp_DuplicateID reports a duplicate brief id.
func TestValidateDecomp_DuplicateID(t *testing.T) {
	src, sidecar := loadValidateDecompScript(t)
	m := validManifest()
	briefs := m["briefs"].([]any)
	dup := minimalBrief("aaa") // same id as briefs[0]
	m["briefs"] = append(briefs, dup)
	out := runValidate(t, src, sidecar, m)
	assertErrors(t, out, "duplicate brief id", "aaa")
}

// TestValidateDecomp_DanglingDep reports an unresolved depends_on reference.
func TestValidateDecomp_DanglingDep(t *testing.T) {
	src, sidecar := loadValidateDecompScript(t)
	m := validManifest()
	briefs := m["briefs"].([]any)
	bbb := briefs[1].(map[string]any)
	bbb["depends_on"] = []any{"nonexistent"}
	out := runValidate(t, src, sidecar, m)
	assertErrors(t, out, "depends_on unknown id", "nonexistent")
}

// TestValidateDecomp_Cycle detects a dependency cycle (aaa→bbb→aaa).
func TestValidateDecomp_Cycle(t *testing.T) {
	src, sidecar := loadValidateDecompScript(t)
	a := minimalBrief("aaa")
	b := minimalBrief("bbb")
	a["depends_on"] = []any{"bbb"}
	b["depends_on"] = []any{"aaa"}
	m := map[string]any{
		"coverage_note": "cycle test",
		"briefs":        []any{a, b},
	}
	out := runValidate(t, src, sidecar, m)
	assertErrors(t, out, "dependency cycle")
}

// TestValidateDecomp_MissingAcceptance reports a brief with no acceptance criteria.
func TestValidateDecomp_MissingAcceptance(t *testing.T) {
	src, sidecar := loadValidateDecompScript(t)
	m := validManifest()
	briefs := m["briefs"].([]any)
	bbb := briefs[1].(map[string]any)
	bbb["acceptance"] = []any{}
	out := runValidate(t, src, sidecar, m)
	assertErrors(t, out, "no acceptance criteria", "bbb")
}

// TestValidateDecomp_MissingTestPlan reports a brief with a blank test_plan.
func TestValidateDecomp_MissingTestPlan(t *testing.T) {
	src, sidecar := loadValidateDecompScript(t)
	m := validManifest()
	briefs := m["briefs"].([]any)
	bbb := briefs[1].(map[string]any)
	bbb["test_plan"] = "   " // whitespace-only
	out := runValidate(t, src, sidecar, m)
	assertErrors(t, out, "no test_plan", "bbb")
}

// TestValidateDecomp_MultipleErrors surfaces multiple errors in one run.
func TestValidateDecomp_MultipleErrors(t *testing.T) {
	src, sidecar := loadValidateDecompScript(t)
	bad := minimalBrief("bad")
	bad["acceptance"] = []any{}
	bad["test_plan"] = ""
	m := map[string]any{
		"coverage_note": "broken",
		"briefs":        []any{bad},
	}
	out := runValidate(t, src, sidecar, m)
	assertErrors(t, out, "no acceptance criteria", "no test_plan")
}
