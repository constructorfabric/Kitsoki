package graph

import "testing"

func TestLint_GoodFixturesClean(t *testing.T) {
	for _, path := range []string{"testdata/good/minimal.yaml", "testdata/good/bundle-min"} {
		t.Run(path, func(t *testing.T) {
			cat, err := LoadCatalog(path)
			if err != nil {
				t.Fatalf("LoadCatalog: %v", err)
			}
			if issues := Lint(cat); len(issues) != 0 {
				t.Errorf("Lint(%s) = %v, want no issues", path, issues)
			}
		})
	}
}

func TestLint_SeedCorpusClean(t *testing.T) {
	cat, err := LoadCatalog("../../docs/proposals/project-object-graph/seed-objects.yaml")
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if issues := Lint(cat, LintOptions{InitiativeScope: true}); len(issues) != 0 {
		t.Errorf("Lint(seed-objects.yaml) found %d issue(s), want 0: %v", len(issues), issues)
	}
}

func TestLint_BadFixtures(t *testing.T) {
	cases := []struct {
		file     string
		wantKind string
	}{
		{"testdata/lint/dangling-ref.yaml", "dangling-ref"},
		{"testdata/lint/type-mismatch.yaml", "type-mismatch"},
		{"testdata/lint/cycle.yaml", "cycle"},
		{"testdata/lint/visibility-leak.yaml", "visibility-leak"},
		{"testdata/lint/area-cycle.yaml", "cycle"},
	}
	for _, tc := range cases {
		t.Run(tc.wantKind, func(t *testing.T) {
			cat, err := LoadCatalog(tc.file)
			if err != nil {
				t.Fatalf("LoadCatalog(%s): %v", tc.file, err)
			}
			issues := Lint(cat)
			if len(issues) == 0 {
				t.Fatalf("Lint(%s) found no issues, want at least one %q", tc.file, tc.wantKind)
			}
			found := false
			for _, iss := range issues {
				if iss.Kind == tc.wantKind {
					found = true
				}
			}
			if !found {
				t.Errorf("Lint(%s) = %v, want an issue of kind %q", tc.file, issues, tc.wantKind)
			}
		})
	}
}

// TestLint_OrphanFeature proves the check is mandatory (design doc §5
// step 3: hard-fail for public features) but scoped to catalogs whose
// registry declares feature.in_area — testdata/good/minimal.yaml (covered
// by TestLint_GoodFixturesClean) has a public feature and no in_area
// declaration and must stay clean.
func TestLint_OrphanFeature(t *testing.T) {
	cat, err := LoadCatalog("testdata/lint/orphan-feature.yaml")
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	var found []LintIssue
	for _, iss := range Lint(cat) {
		if iss.Kind == "orphan-feature" {
			found = append(found, iss)
		}
	}
	if len(found) != 1 {
		t.Fatalf("orphan-feature issues = %v, want exactly 1 (feature-orphan only)", found)
	}
	if found[0].Node != "feature-orphan" {
		t.Errorf("orphan-feature issue node = %q, want %q", found[0].Node, "feature-orphan")
	}
	if found[0].Severity != SeverityError {
		t.Errorf("orphan-feature issue severity = %q, want %q", found[0].Severity, SeverityError)
	}
}

// TestLint_InitiativeScope proves initiative-scope fires only for the
// initiative whose declared `targets` disagrees with the derived
// includes -> change.implements -> feature.in_area set, is always a
// warning, and is off unless requested.
func TestLint_InitiativeScope(t *testing.T) {
	cat, err := LoadCatalog("testdata/lint/initiative-scope.yaml")
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	t.Run("off by default", func(t *testing.T) {
		for _, iss := range Lint(cat) {
			if iss.Kind == "initiative-scope" {
				t.Errorf("Lint(cat) without LintOptions reported initiative-scope, want it gated off: %v", iss)
			}
		}
	})

	t.Run("enabled", func(t *testing.T) {
		issues := Lint(cat, LintOptions{InitiativeScope: true})
		var found []LintIssue
		for _, iss := range issues {
			if iss.Kind == "initiative-scope" {
				found = append(found, iss)
			}
		}
		if len(found) != 1 {
			t.Fatalf("initiative-scope issues = %v, want exactly 1 (initiative-mismatch only, initiative-match agrees)", found)
		}
		if found[0].Node != "initiative-mismatch" {
			t.Errorf("initiative-scope issue node = %q, want %q", found[0].Node, "initiative-mismatch")
		}
		if found[0].Severity != SeverityWarning {
			t.Errorf("initiative-scope issue severity = %q, want %q", found[0].Severity, SeverityWarning)
		}
	})
}

func TestLint_CycleReportsBothNodes(t *testing.T) {
	cat, err := LoadCatalog("testdata/lint/cycle.yaml")
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	issues := Lint(cat)
	seen := map[NodeID]bool{}
	for _, iss := range issues {
		if iss.Kind == "cycle" {
			seen[iss.Node] = true
		}
	}
	if !seen["change-a"] || !seen["change-b"] {
		t.Errorf("expected cycle issues for both change-a and change-b, got %v", issues)
	}
}
