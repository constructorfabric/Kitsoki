package kitverify

import (
	"fmt"
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/host/opschema"
	"kitsoki/internal/kit"
)

const conformantDir = "testdata/kits/conformant-kit"
const nonconformantDir = "testdata/kits/nonconformant-kit"

func loadStory(t *testing.T, kitDir, story string) *app.AppDef {
	t.Helper()
	manifest, err := kit.LoadDir(kitDir)
	if err != nil {
		t.Fatalf("kit.LoadDir(%q): %v", kitDir, err)
	}
	def, err := app.Load(manifest.StoryDir(story) + "/app.yaml")
	if err != nil {
		t.Fatalf("app.Load(%s/%s): %v", kitDir, story, err)
	}
	return def
}

func TestCheckExitRequires(t *testing.T) {
	t.Run("conformant", func(t *testing.T) {
		def := loadStory(t, conformantDir, "checker")
		if issues := CheckExitRequires(def); len(issues) != 0 {
			t.Fatalf("expected no issues, got %v", issues)
		}
	})
	t.Run("nonconformant", func(t *testing.T) {
		def := loadStory(t, nonconformantDir, "checker")
		issues := CheckExitRequires(def)
		if len(issues) == 0 {
			t.Fatalf("expected an issue for the unmet exits.done.requires[pr_url]")
		}
		if !strings.Contains(issues[0], "pr_url") || !strings.Contains(issues[0], "@exit:done") {
			t.Fatalf("issue message doesn't name the offending exit/key: %v", issues)
		}
	})
}

func TestCheckExportsIntents(t *testing.T) {
	t.Run("conformant", func(t *testing.T) {
		def := loadStory(t, conformantDir, "checker")
		if issues := CheckExportsIntents(def); len(issues) != 0 {
			t.Fatalf("expected no issues, got %v", issues)
		}
	})
	t.Run("nonconformant", func(t *testing.T) {
		def := loadStory(t, nonconformantDir, "checker")
		issues := CheckExportsIntents(def)
		if len(issues) != 1 {
			t.Fatalf("expected exactly one issue (the undefined 'ghost' export), got %v", issues)
		}
		if !strings.Contains(issues[0], `"ghost"`) {
			t.Fatalf("issue doesn't name 'ghost': %v", issues[0])
		}
	})
}

func TestCheckInterfaceOpShapes(t *testing.T) {
	registry := opschema.Builtins()
	t.Run("conformant", func(t *testing.T) {
		def := loadStory(t, conformantDir, "checker")
		if issues := CheckInterfaceOpShapes(def, registry); len(issues) != 0 {
			t.Fatalf("expected no issues, got %v", issues)
		}
	})
	t.Run("nonconformant", func(t *testing.T) {
		def := loadStory(t, nonconformantDir, "checker")
		issues := CheckInterfaceOpShapes(def, registry)
		if len(issues) < 2 {
			t.Fatalf("expected at least 2 issues (limit type mismatch + undeclared 'note' output), got %v", issues)
		}
		joined := strings.Join(issues, "\n")
		if !strings.Contains(joined, "limit") {
			t.Errorf("expected an issue about the mismatched 'limit' field, got %v", issues)
		}
		if !strings.Contains(joined, "note") {
			t.Errorf("expected an issue about the undeclared 'note' output field, got %v", issues)
		}
	})
	t.Run("unregistered handler is not an error", func(t *testing.T) {
		def := loadStory(t, conformantDir, "checker")
		def.HostInterfaces["ticket"].Default = "host.some_unregistered_handler"
		if issues := CheckInterfaceOpShapes(def, registry); len(issues) != 0 {
			t.Fatalf("expected an unregistered handler to be skipped (not an error), got %v", issues)
		}
	})
}

func TestCheckParameters(t *testing.T) {
	manifest := &kit.Def{
		Parameters: map[string]kit.Parameter{
			"greeting":            {Type: "string", Default: "hi"},
			"required_no_default": {Type: "string", Required: true},
			"bad_default":         {Type: "int", Default: "not-an-int"},
			"no_default_optional": {Type: "string"},
		},
	}
	issues := CheckParameters(manifest, nil)
	joined := strings.Join(issues, "\n")
	if strings.Contains(joined, "greeting") {
		t.Errorf("greeting should be clean: %v", issues)
	}
	if strings.Contains(joined, "required_no_default") {
		t.Errorf("a required parameter needs no default to pass the manifest-only check: %v", issues)
	}
	if !strings.Contains(joined, "bad_default") {
		t.Errorf("expected a type-mismatch issue for bad_default: %v", issues)
	}
	if !strings.Contains(joined, "no_default_optional") {
		t.Errorf("expected a not-required-no-default issue for no_default_optional: %v", issues)
	}

	t.Run("provided", func(t *testing.T) {
		provided := map[string]any{
			"greeting":            "hello",
			"required_no_default": 5, // wrong type: declared string
			"unknown_param":       true,
		}
		issues := CheckParameters(manifest, provided)
		joined := strings.Join(issues, "\n")
		if !strings.Contains(joined, "unknown_param") {
			t.Errorf("expected an issue for the undeclared provided key: %v", issues)
		}
		if !strings.Contains(joined, "required_no_default") {
			t.Errorf("expected a type-mismatch issue for required_no_default: %v", issues)
		}
	})
}

func TestVerifyKit(t *testing.T) {
	t.Run("conformant", func(t *testing.T) {
		report, err := VerifyKit(conformantDir, Options{})
		if err != nil {
			t.Fatalf("VerifyKit: %v", err)
		}
		if !report.OK() {
			t.Fatalf("expected OK report, got: stories=%+v params=%v flows=%+v", report.Stories, report.ParamIssues, report.Flows)
		}
		if len(report.Flows) != 1 || report.Flows[0].Report == nil || report.Flows[0].Report.Failed != 0 || report.Flows[0].Report.Passed != 1 {
			t.Fatalf("expected the conformance flow to run and pass, got %+v", report.Flows)
		}
	})

	t.Run("nonconformant", func(t *testing.T) {
		report, err := VerifyKit(nonconformantDir, Options{})
		if err != nil {
			t.Fatalf("VerifyKit: %v", err)
		}
		if report.OK() {
			t.Fatalf("expected a non-OK report")
		}
		if len(report.Stories) != 1 || len(report.Stories[0].Issues) == 0 {
			t.Fatalf("expected story-level issues, got %+v", report.Stories)
		}
	})
}

// TestRunExtends exercises D6/S4's "base-kit suites re-run against
// downstream extensions": a kit that `extends:` another kit should have the
// base kit's own conformance suite re-run as part of verifying the
// extension, given a resolver that can find the base kit on disk.
func TestRunExtends(t *testing.T) {
	manifest := &kit.Def{Extends: []kit.Dependency{{Kit: "@kitsoki-test/conformant"}}}

	t.Run("no resolver configured", func(t *testing.T) {
		reports := runExtends(manifest, Options{})
		if len(reports) != 1 || reports[0].Err == nil {
			t.Fatalf("expected one ExtendsReport with a non-nil Err when no ExtendsResolver is set, got %+v", reports)
		}
	})

	t.Run("resolver finds the base kit", func(t *testing.T) {
		resolver := func(identity string) (string, error) {
			if identity == "@kitsoki-test/conformant" {
				return conformantDir, nil
			}
			return "", fmt.Errorf("unknown kit %q", identity)
		}
		reports := runExtends(manifest, Options{Extends: resolver})
		if len(reports) != 1 {
			t.Fatalf("expected exactly one ExtendsReport, got %+v", reports)
		}
		r := reports[0]
		if r.Err != nil {
			t.Fatalf("unexpected Err: %v", r.Err)
		}
		if r.Report == nil || !r.Report.OK() {
			t.Fatalf("expected the base kit's own conformance suite to re-run and pass, got %+v", r.Report)
		}
		if len(r.Report.Flows) != 1 || r.Report.Flows[0].Report == nil || r.Report.Flows[0].Report.Passed != 1 {
			t.Fatalf("expected the base kit's flow suite to have actually run, got %+v", r.Report.Flows)
		}
	})

	t.Run("unresolvable base kit is reported, not fatal", func(t *testing.T) {
		resolver := func(identity string) (string, error) {
			return "", fmt.Errorf("no such kit")
		}
		reports := runExtends(manifest, Options{Extends: resolver})
		if len(reports) != 1 || reports[0].Err == nil {
			t.Fatalf("expected the resolve failure to surface as ExtendsReport.Err, got %+v", reports)
		}
	})
}
