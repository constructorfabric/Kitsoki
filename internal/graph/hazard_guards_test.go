package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/clock"
)

// ─── Hazard guard #2: file-level CAS ────────────────────────────────────────

// TestCommitWithCAS_DetectsConcurrentWrite is the core CAS mechanism, tested
// directly and deterministically: load a catalog (capturing its
// ContentDigest), then simulate a concurrent writer mutating the real file
// before commitWithCAS re-checks — the interleaved write must be detected
// and commitWithCAS must refuse to copy scratch changes over it.
func TestCommitWithCAS_DetectsConcurrentWrite(t *testing.T) {
	root := copySingleFileFixture(t)
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	// Simulate a concurrent writer landing between load and copy-back.
	raw, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := os.WriteFile(root, append(raw, []byte("\n# concurrent writer landed here\n")...), 0o644); err != nil {
		t.Fatalf("simulate concurrent write: %v", err)
	}

	err = commitWithCAS(cat, root /* scratchRoot unused on this early-return path */, nil)
	if err == nil {
		t.Fatal("expected commitWithCAS to detect the interleaved write and refuse to commit")
	}
	if !isCASConflict(err) {
		t.Fatalf("expected errCASConflict, got: %v", err)
	}
}

func isCASConflict(err error) bool {
	return err == errCASConflict
}

// TestPropose_CASConflictRetriesAndSucceeds simulates exactly one
// interleaved write landing during Propose's first attempt (via
// casTestHook, deterministic — no goroutine racing): Propose must detect
// it, retry the whole operation against a freshly reloaded catalog, and
// succeed on the second attempt instead of clobbering the concurrent
// change or surfacing it as a hard error.
func TestPropose_CASConflictRetriesAndSucceeds(t *testing.T) {
	root := copySingleFileFixture(t)

	calls := 0
	casTestHook = func() {
		calls++
		if calls == 1 {
			// First commit attempt: simulate a concurrent writer landing in
			// the window, by touching the file's content out from under
			// the in-flight attempt's captured digest.
			raw, err := os.ReadFile(root)
			if err != nil {
				t.Fatalf("read during hook: %v", err)
			}
			if err := os.WriteFile(root, append(raw, []byte("\n# concurrent writer\n")...), 0o644); err != nil {
				t.Fatalf("write during hook: %v", err)
			}
		}
	}
	t.Cleanup(func() { casTestHook = nil })

	res, err := Propose(root, ProposeInput{
		Title: "Add a requirement under a concurrent writer",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-cas", "title": "New requirement", "status": "draft", "visibility": "internal"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected Propose to retry past the transient CAS conflict and succeed, got reject reasons: %v", res.RejectReasons)
	}
	if calls < 2 {
		t.Fatalf("expected commitWithCAS to be called at least twice (one conflict + one successful retry), got %d", calls)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cat.Nodes[res.ChangesetID]; !ok {
		t.Fatal("changeset node missing after a CAS-conflict retry recovered")
	}
}

// TestPropose_CASConflictExhaustsRetries: a concurrent writer that NEVER
// stops landing must eventually surface a CONFLICT reject reason (bounded
// retries), rather than looping forever or silently clobbering.
func TestPropose_CASConflictExhaustsRetries(t *testing.T) {
	root := copySingleFileFixture(t)

	casTestHook = func() {
		raw, err := os.ReadFile(root)
		if err != nil {
			t.Fatalf("read during hook: %v", err)
		}
		if err := os.WriteFile(root, append(raw, []byte("\n# concurrent writer, every attempt\n")...), 0o644); err != nil {
			t.Fatalf("write during hook: %v", err)
		}
	}
	t.Cleanup(func() { casTestHook = nil })

	res, err := Propose(root, ProposeInput{
		Title: "Add a requirement under a persistent concurrent writer",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-cas2", "title": "New requirement", "status": "draft", "visibility": "internal"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) == 0 {
		t.Fatal("expected Propose to exhaust its retry budget and reject with CONFLICT")
	}
	found := false
	for _, r := range res.RejectReasons {
		if strings.Contains(r, "CONFLICT") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a CONFLICT reject reason, got: %v", res.RejectReasons)
	}
}

// ─── Hazard guard #3: lint-diff gate ────────────────────────────────────────

// dirtyBundleFixture returns a bundle catalog that is already lint-dirty
// (a dangling edge reference pre-existing before any changeset) so tests
// can assert that dirt alone never blocks a clean changeset, but a NEW
// issue introduced by the changeset itself does.
func dirtyBundleFixture(t *testing.T) string {
	t.Helper()
	root := copyBundleFixture(t)
	// Add a dangling edge on the existing feature-one node: requirements
	// now also points at a nonexistent node, a pre-existing lint error.
	featuresPath := filepath.Join(root, "nodes", "features.yaml")
	raw, err := os.ReadFile(featuresPath)
	if err != nil {
		t.Fatalf("read features.yaml: %v", err)
	}
	dirty := strings.Replace(string(raw), "requirements:\n      - req-one\n", "requirements:\n      - req-one\n      - req-does-not-exist\n", 1)
	if dirty == string(raw) {
		t.Fatal("fixture replace had no effect — features.yaml layout changed")
	}
	if err := os.WriteFile(featuresPath, []byte(dirty), 0o644); err != nil {
		t.Fatalf("write dirty fixture: %v", err)
	}
	return root
}

// TestLintDiffGate_PreExistingDirtDoesNotBlockCleanChangeset: a catalog
// that already has an error-severity lint issue before any write must not
// deadlock an authorized changeset that doesn't touch that issue at all
// (hazard guard #3 — pre-existing dirt no longer deadlocks writes). Apply
// is where the lint-diff gate actually bites (it's the carrier that applies
// a changeset's operations to a candidate and lints THAT — Propose only
// ever lints the changeset-node-append itself, per its own doc comment).
func TestLintDiffGate_PreExistingDirtDoesNotBlockCleanChangeset(t *testing.T) {
	root := dirtyBundleFixture(t)

	// Sanity: the fixture really is dirty before we touch it.
	dirtyCat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if issues := ErrorIssues(Lint(dirtyCat)); len(issues) == 0 {
		t.Fatal("test fixture setup bug: expected pre-existing dangling-ref lint error")
	}

	writeChangeset(t, root, "cs-clean", ChangesetStatusAuthorized, `    - kind: added
      after:
        schema: graph/requirement/v0
        id: req-clean
        title: Clean requirement
        status: draft
        visibility: internal
`)
	res, err := Apply(root, "cs-clean", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected apply to succeed despite pre-existing dirt, got rejected: %+v", res)
	}
}

// TestLintDiffGate_NewIssueStillBlocks: a changeset that introduces a BRAND
// NEW error-severity issue must still be blocked, even against an already-
// dirty baseline — only pre-existing dirt is exempt.
func TestLintDiffGate_NewIssueStillBlocks(t *testing.T) {
	root := dirtyBundleFixture(t)

	writeChangeset(t, root, "cs-newdirt", ChangesetStatusAuthorized, `    - kind: added
      after:
        schema: graph/requirement/v0
        id: req-new-dirt
        title: Introduces a new dangling ref
        status: draft
        visibility: public
        edges:
          required_by:
            - feature-does-not-exist
`)
	res, err := Apply(root, "cs-newdirt", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected apply to be rejected by the NEW dangling-ref issue")
	}
	if len(res.LintIssues) == 0 {
		t.Fatal("expected LintIssues to carry the new dangling-ref issue")
	}
	for _, iss := range res.LintIssues {
		if iss.Node == "feature-one" {
			t.Errorf("the pre-existing dirty-fixture issue on feature-one leaked into NEW issues: %+v", iss)
		}
	}
	found := false
	for _, iss := range res.LintIssues {
		if iss.Node == "req-new-dirt" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an issue on the newly added req-new-dirt node, got: %+v", res.LintIssues)
	}
}

// ─── Hazard guard #4: canonicality pre-check ────────────────────────────────

// blockScalarFixture is a single-file catalog whose changeset type's
// requirement node carries a hand-wrapped literal block scalar — yaml.v3
// would reflow this on any whole-document re-marshal (the failure mode
// checkCanonical exists to catch before a write, not after).
const blockScalarFixture = `schema: project-object-graph/seed-catalog/v0
catalog:
  id: canon-fixture
type_registry:
  - id: core-node
    schema: graph-type/v0
    required_fields: [id, schema, title, status, visibility]
  - id: requirement
    schema: graph-type/v0
    extends: core-node
  - id: changeset
    schema: graph-type/v0
    extends: core-node
nodes:
  - schema: graph/requirement/v0
    id: req-block
    title: Requirement with a block scalar
    status: draft
    visibility: internal
    statement: >-
      This is a folded block scalar that has been hand-wrapped at a
      narrow width by a human editor, spanning several short lines
      instead of one long line, to keep diffs readable in review —
      yaml.v3's re-marshal collapses this onto one line, the exact
      reflow hazard this guard exists to catch.
`

func writeBlockScalarFixture(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(dst, []byte(blockScalarFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dst
}

func TestCheckCanonical_FlagsHandWrappedBlockScalarDivergence(t *testing.T) {
	root := writeBlockScalarFixture(t)
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	reasons := checkCanonical(cat)
	if len(reasons) == 0 {
		t.Fatal("expected checkCanonical to flag the hand-wrapped folded block scalar as non-canonical")
	}
	found := false
	for _, r := range reasons {
		if strings.Contains(r, "NEEDS_CANONICALIZATION") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a NEEDS_CANONICALIZATION reason, got: %v", reasons)
	}

	// And Propose must refuse to write at all — not even attempt the
	// scratch build — when the catalog needs canonicalization.
	res, err := Propose(root, ProposeInput{
		Title: "Should be blocked by canonicality pre-check",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-blocked", "title": "Should not land", "status": "draft", "visibility": "internal"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) == 0 {
		t.Fatal("expected Propose to reject with NEEDS_CANONICALIZATION")
	}
	reload, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reload.Nodes["req-blocked"]; ok {
		t.Error("req-blocked must not have been written when the catalog needed canonicalization")
	}
}

// TestCheckCanonical_NoBlockScalarsNeverBlocks: a catalog with no block
// scalars at all is exempt from the byte-compare even when it isn't
// byte-identical to yaml.v3's own serialization conventions (flow-mapping
// padding, quoting) — checkCanonical only cares about the block-scalar
// reflow hazard, not incidental cosmetic differences.
func TestCheckCanonical_NoBlockScalarsNeverBlocks(t *testing.T) {
	root := copySingleFileFixture(t)
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if reasons := checkCanonical(cat); len(reasons) > 0 {
		t.Errorf("expected no canonicalization block for a block-scalar-free fixture, got: %v", reasons)
	}
}

// ─── Hazard guard #5: guard-fill + validate_only ────────────────────────────

func TestFillGuards_ModifiedFillsOmittedBeforeFromLiveNode(t *testing.T) {
	root := copySingleFileFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "Modify without an explicit before",
		Operations: []map[string]any{
			{
				"kind": "modified",
				"node": "req-one",
				"changes": []any{
					map[string]any{"path": []any{"title"}, "after": "Requirement one, renamed"},
				},
			},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed with a server-filled guard, got: %v", res.RejectReasons)
	}
	if len(res.GuardFills) != 1 {
		t.Fatalf("expected exactly one GuardFill echo, got %d: %+v", len(res.GuardFills), res.GuardFills)
	}
	gf := res.GuardFills[0]
	if gf.Node != "req-one" || gf.Value != "Requirement one" {
		t.Errorf("GuardFill = %+v, want Node=req-one Value=%q (the live title before the edit)", gf, "Requirement one")
	}

	// The persisted changeset must carry the filled Before, not an absent
	// one, so a later Authorize/Apply's stale-guard actually checks it.
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	cs, err := ParseChangeset(cat.Nodes[res.ChangesetID])
	if err != nil {
		t.Fatalf("ParseChangeset: %v", err)
	}
	if len(cs.Operations) != 1 || len(cs.Operations[0].Changes) != 1 {
		t.Fatalf("unexpected persisted operations: %+v", cs.Operations)
	}
	if cs.Operations[0].Changes[0].Before != "Requirement one" {
		t.Errorf("persisted Before = %v, want the filled live value %q", cs.Operations[0].Changes[0].Before, "Requirement one")
	}
}

func TestFillGuards_RemovedFillsFullNodeAsDigest(t *testing.T) {
	root := copySingleFileFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "Remove without an explicit before",
		Operations: []map[string]any{
			{"kind": "removed", "node": "req-one"},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed with a server-filled removed guard, got: %v", res.RejectReasons)
	}
	if len(res.GuardFills) != 1 {
		t.Fatalf("expected exactly one GuardFill echo, got %d: %+v", len(res.GuardFills), res.GuardFills)
	}
	gf := res.GuardFills[0]
	if gf.Node != "req-one" {
		t.Errorf("GuardFill.Node = %q, want req-one", gf.Node)
	}
	if gf.SHA == "" || len(gf.Fields) == 0 {
		t.Errorf("expected a removed-op guard fill to echo a {sha, fields} digest, got %+v", gf)
	}
	if gf.Value != nil || gf.Path != nil {
		t.Errorf("a removed-op guard fill must echo a digest, not full content: %+v", gf)
	}

	// A stale removal (mismatched before) must still be rejected —
	// confirming the guard-fill actually feeds a real stale-guard, not a
	// no-op check.
	root2 := copySingleFileFixture(t)
	staleRes, err := Propose(root2, ProposeInput{
		Title: "Stale removal",
		Operations: []map[string]any{
			{"kind": "removed", "node": "req-one", "before": map[string]any{"title": "Not the real title"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(staleRes.RejectReasons) == 0 {
		t.Fatal("expected a stale removed-op Before to be rejected")
	}
}

// TestFillGuards_RetypedFillsFullNodeAsDigest mirrors
// TestFillGuards_RemovedFillsFullNodeAsDigest for "retyped" ops (hazard
// guard #5's red-team amendment extending guard-fill to retyped/renamed
// preconditions): clause-one (type iso9001-clause, minimal.yaml fixture)
// retypes down to requirement — a legal retype (requirement has no
// required fields clause-one lacks, and clause-one's inherited required_by
// edge is still declared on the target type) — with no "before" supplied.
func TestFillGuards_RetypedFillsFullNodeAsDigest(t *testing.T) {
	root := copySingleFileFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "Retype without an explicit before",
		Operations: []map[string]any{
			{"kind": "retyped", "node": "clause-one", "from_type": "iso9001-clause", "to_type": "requirement"},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed with a server-filled retyped guard, got: %v", res.RejectReasons)
	}
	if len(res.GuardFills) != 1 {
		t.Fatalf("expected exactly one GuardFill echo, got %d: %+v", len(res.GuardFills), res.GuardFills)
	}
	gf := res.GuardFills[0]
	if gf.Node != "clause-one" {
		t.Errorf("GuardFill.Node = %q, want clause-one", gf.Node)
	}
	if gf.SHA == "" || len(gf.Fields) == 0 {
		t.Errorf("expected a retyped-op guard fill to echo a {sha, fields} digest, got %+v", gf)
	}
	if gf.Value != nil || gf.Path != nil {
		t.Errorf("a retyped-op guard fill must echo a digest, not full content: %+v", gf)
	}

	// The persisted changeset must carry the filled Before, not an absent
	// one, so a later Authorize/Apply's stale-guard actually checks it.
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	cs, err := ParseChangeset(cat.Nodes[res.ChangesetID])
	if err != nil {
		t.Fatalf("ParseChangeset: %v", err)
	}
	if len(cs.Operations) != 1 || len(cs.Operations[0].Before) == 0 {
		t.Fatalf("expected persisted retyped op to carry a filled Before, got: %+v", cs.Operations)
	}
}

// TestFillGuards_RetypedStaleBeforeRejected: a retyped op whose (caller-
// supplied) Before no longer matches the live node must be rejected as
// stale — mirroring OpRemoved's nodeMatchesBefore stale-guard.
func TestFillGuards_RetypedStaleBeforeRejected(t *testing.T) {
	root := copySingleFileFixture(t)
	staleRes, err := Propose(root, ProposeInput{
		Title: "Stale retype",
		Operations: []map[string]any{
			{
				"kind":      "retyped",
				"node":      "clause-one",
				"from_type": "iso9001-clause",
				"to_type":   "requirement",
				"before":    map[string]any{"title": "Not the real title"},
			},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(staleRes.RejectReasons) == 0 {
		t.Fatal("expected a stale retyped-op Before to be rejected")
	}
	found := false
	for _, r := range staleRes.RejectReasons {
		if strings.Contains(r, "stale") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a stale-guard reject reason, got: %v", staleRes.RejectReasons)
	}
}

func TestFillGuards_EdgeFieldReadThroughEdgeTargetsNotRawEdgesMap(t *testing.T) {
	// testdata/lint/cycle.yaml declares change.depends_on as storage:
	// top_level — the exact edge shape the hard constraint requires
	// readNodePath/nodeToMap to resolve via Node.EdgeTargets, never the raw
	// Edges map (which is always empty for a top_level edge).
	root := filepath.Join(t.TempDir(), "catalog.yaml")
	raw, err := os.ReadFile("testdata/lint/cycle.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	if err := os.WriteFile(root, raw, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	node := cat.Nodes["change-a"]
	if node == nil {
		t.Fatal("change-a not found")
	}
	got := readNodePath(cat, node, []string{"edges", "depends_on"})
	targets, ok := got.([]NodeID)
	if !ok || len(targets) != 1 || targets[0] != "change-b" {
		t.Fatalf("readNodePath(edges,depends_on) on a top_level-storage edge = %#v, want [change-b] via EdgeTargets", got)
	}

	m := nodeToMap(cat, node)
	edges, ok := m["edges"].(map[string]any)
	if !ok {
		t.Fatalf("nodeToMap didn't surface an edges block for a top_level edge field: %+v", m)
	}
	depIface, ok := edges["depends_on"].([]any)
	if !ok || len(depIface) != 1 || depIface[0] != "change-b" {
		t.Fatalf("nodeToMap edges.depends_on = %#v, want [change-b]", edges["depends_on"])
	}
}

func TestPropose_ValidateOnlyWritesNothing(t *testing.T) {
	root := copySingleFileFixture(t)
	before, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	res, err := Propose(root, ProposeInput{
		Title:        "Validate only, should not write",
		ValidateOnly: true,
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-validate-only", "title": "Should not land", "status": "draft", "visibility": "internal"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected validate_only to pass validation, got reject reasons: %v", res.RejectReasons)
	}
	if !res.ValidatedOnly {
		t.Error("expected ValidatedOnly=true")
	}
	if res.ChangesetID == "" {
		t.Error("expected a would-be changeset id to be reported")
	}

	after, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Error("validate_only must write nothing — the real catalog file changed")
	}
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cat.Nodes[res.ChangesetID]; ok {
		t.Error("validate_only must not persist the changeset node itself")
	}
	if _, ok := cat.Nodes["req-validate-only"]; ok {
		t.Error("validate_only must not apply the proposed operations")
	}
}

// TestPropose_ValidateOnlyStillRunsFullValidation: validate_only must still
// run ValidateChangeset (stale-guards etc.) and reject a genuinely invalid
// changeset, not just rubber-stamp everything because nothing gets written.
func TestPropose_ValidateOnlyStillRunsFullValidation(t *testing.T) {
	root := copySingleFileFixture(t)
	res, err := Propose(root, ProposeInput{
		Title:        "Validate only, but the precondition is stale",
		ValidateOnly: true,
		Operations: []map[string]any{
			{
				"kind": "modified",
				"node": "req-one",
				"changes": []any{
					map[string]any{"path": []any{"title"}, "before": "Not the real title", "after": "New title"},
				},
			},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) == 0 {
		t.Fatal("expected validate_only to still reject a stale precondition via full validation")
	}

	after, err := os.ReadFile(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(after), "Requirement one") {
		t.Error("catalog must be unchanged after a rejected validate_only propose")
	}
}
