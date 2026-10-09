package graph

import (
	"os"
	"path/filepath"
	"testing"

	"kitsoki/internal/clock"
)

// copyBundleFixture copies testdata/apply/bundle into a fresh temp dir so
// each test can mutate its own scratch copy without touching the checked-in
// fixture (Apply itself also scratch-copies internally, but the top-level
// fixture must stay pristine across test runs regardless).
func copyBundleFixture(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := "testdata/apply/bundle"
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	return dst
}

// writeChangeset drops a changeset node file into the bundle at root,
// authorized by default (tests override op-by-op as needed).
func writeChangeset(t *testing.T, root, id, status, opsYAML string) {
	t.Helper()
	content := "- schema: graph/changeset/v1\n" +
		"  id: " + id + "\n" +
		"  title: Test changeset\n" +
		"  status: " + status + "\n" +
		"  visibility: internal\n" +
		"  operations:\n" + opsYAML
	if err := os.WriteFile(filepath.Join(root, "nodes", "changeset.yaml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write changeset: %v", err)
	}
}

func TestApply_Added(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-add", ChangesetStatusAuthorized, `    - kind: added
      after:
        schema: graph/requirement/v0
        id: req-two
        title: Requirement two
        status: draft
        visibility: public
`)
	res, err := Apply(root, "change-add", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected apply to succeed, got rejected: %+v", res)
	}
	if !res.Applied {
		t.Fatalf("expected Applied=true, got %+v", res)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cat.Nodes["req-two"]; !ok {
		t.Fatal("req-two was not added to the real catalog")
	}
}

func TestApply_Modified(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-mod", ChangesetStatusAuthorized, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Apply(root, "change-mod", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected apply to succeed, got rejected: %+v", res)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cat.Nodes["req-one"].Status != "satisfied" {
		t.Errorf("req-one status = %q, want satisfied", cat.Nodes["req-one"].Status)
	}

	raw, _ := os.ReadFile(filepath.Join(root, "nodes", "requirements.yaml"))
	if !contains(string(raw), "generic requirement statement") {
		t.Error("expected the file's leading comment/other fields to survive the rewrite")
	}
}

func TestApply_ModifiedStaleGuardRejects(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-mod-stale", ChangesetStatusAuthorized, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: satisfied
          after: closed
`)
	res, err := Apply(root, "change-mod-stale", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected a stale-guard rejection (before=satisfied but catalog has draft)")
	}
	if res.Applied {
		t.Fatal("a rejected changeset must not apply")
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cat.Nodes["req-one"].Status != "draft" {
		t.Errorf("catalog must be untouched after rejection, got status %q", cat.Nodes["req-one"].Status)
	}
}

func TestApply_Removed(t *testing.T) {
	root := copyBundleFixture(t)
	// Remove the edge that points at req-one first (in the same changeset)
	// so removing req-one doesn't leave a dangling-ref lint failure.
	writeChangeset(t, root, "change-rm", ChangesetStatusAuthorized, `    - kind: modified
      node: feature-one
      changes:
        - path: [edges, requirements]
          before: [req-one]
          after: []
    - kind: removed
      node: req-one
      before:
        schema: graph/requirement/v0
        title: Requirement one
`)
	res, err := Apply(root, "change-rm", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected apply to succeed, got rejected: %+v", res)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cat.Nodes["req-one"]; ok {
		t.Error("req-one should have been removed")
	}
}

func TestApply_Renamed(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-rename", ChangesetStatusAuthorized, `    - kind: renamed
      from: req-one
      to: req-one-renamed
`)
	res, err := Apply(root, "change-rename", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected apply to succeed, got rejected: %+v", res)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cat.Nodes["req-one"]; ok {
		t.Error("req-one should no longer exist under its old id")
	}
	renamed, ok := cat.Nodes["req-one-renamed"]
	if !ok {
		t.Fatal("req-one-renamed not found")
	}
	// The rename must coordinate-rewrite feature-one's edge reference too.
	feature := cat.Nodes["feature-one"]
	found := false
	for _, target := range feature.Edges["requirements"] {
		if target == "req-one-renamed" {
			found = true
		}
	}
	if !found {
		t.Errorf("feature-one.requirements = %v, want it to reference req-one-renamed", feature.Edges["requirements"])
	}
	if len(renamed.Edges["required_by"]) != 1 || renamed.Edges["required_by"][0] != "feature-one" {
		t.Errorf("req-one-renamed.required_by = %v, want [feature-one]", renamed.Edges["required_by"])
	}
}

func TestApply_RejectsUnauthorizedStatus(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-proposed", ChangesetStatusProposed, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Apply(root, "change-proposed", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected a proposed (not authorized) changeset to be rejected")
	}

	// Dry-run should still preview it despite the status.
	res, err = Apply(root, "change-proposed", true, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply dry-run: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("dry-run of a valid-but-unauthorized changeset should preview clean, got %+v", res)
	}
	if res.Applied {
		t.Fatal("dry-run must never set Applied")
	}
}

func TestApply_RejectsOnPostApplyLintFailure(t *testing.T) {
	root := copyBundleFixture(t)
	// Adding a node whose edge points at a nonexistent target is valid
	// changeset shape (pre-apply validation passes) but must fail the
	// post-apply catalog lint (dangling-ref) and leave the catalog untouched.
	writeChangeset(t, root, "change-bad-edge", ChangesetStatusAuthorized, `    - kind: added
      after:
        schema: graph/requirement/v0
        id: req-danling
        title: Requirement with a dangling edge
        status: draft
        visibility: public
        edges:
          required_by: [feature-does-not-exist]
`)
	res, err := Apply(root, "change-bad-edge", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.LintIssues) == 0 {
		t.Fatal("expected a post-apply lint rejection (dangling-ref)")
	}
	if res.Applied {
		t.Fatal("a lint-rejected changeset must not apply")
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cat.Nodes["req-danling"]; ok {
		t.Error("catalog must be untouched: req-danling should not exist")
	}
}

func TestApply_RejectsAddingExistingNode(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-dup", ChangesetStatusAuthorized, `    - kind: added
      after:
        schema: graph/requirement/v0
        id: req-one
        title: Duplicate
        status: draft
        visibility: public
`)
	res, err := Apply(root, "change-dup", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.RejectReasons) == 0 {
		t.Fatal("expected a pre-apply rejection (added node already exists)")
	}
}


func TestApply_Retyped(t *testing.T) {
	root := copyBundleFixture(t)
	// Retype req-one from requirement to feature. This should fail because req-one has
	// required_by edge which feature does not declare.
	writeChangeset(t, root, "change-retype-fail", ChangesetStatusAuthorized, `    - kind: retyped
      node: req-one
      from_type: requirement
      to_type: feature
`)
	res, err := Apply(root, "change-retype-fail", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected retype to fail due to undeclared edge field")
	}

	// Add a new type program that has core-node fields and requirements edges, then retype req-one to program.
	// Wait, req-one carries required_by. So first we add type program with required_by edge too.
	writeChangeset(t, root, "change-retype-success", ChangesetStatusAuthorized, `    - kind: registry_type_added
      after:
        id: program
        schema: graph-type/v1
        extends: requirement
        edge_fields:
          - id: required_by
            target_type: feature
            cardinality: many
    - kind: retyped
      node: req-one
      from_type: requirement
      to_type: program
`)
	res, err = Apply(root, "change-retype-success", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected apply to succeed, got rejected: %+v", res)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node, ok := cat.Nodes["req-one"]
	if !ok {
		t.Fatal("req-one not found")
	}
	if node.TypeID != "program" {
		t.Errorf("expected node type program, got %q", node.TypeID)
	}
	if node.Schema != "graph/program/v0" {
		t.Errorf("expected schema graph/program/v0, got %q", node.Schema)
	}
}

func TestApply_RegistryTypeModified(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-reg-mod", ChangesetStatusAuthorized, `    - kind: registry_type_modified
      node: requirement
      changes:
        - path: [summary]
          before: null
          after: "Updated summary"
`)
	res, err := Apply(root, "change-reg-mod", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected apply to succeed, got rejected: %+v", res)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	def, exists := cat.Registry.TypeDef("requirement")
	if !exists {
		t.Fatal("requirement type not found")
	}
	if def.Summary != "Updated summary" {
		t.Errorf("expected summary 'Updated summary', got %q", def.Summary)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
