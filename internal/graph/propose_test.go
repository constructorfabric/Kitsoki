package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/clock"
)

// copySingleFileFixture copies a single-file catalog (not a bundle dir) into
// a fresh temp file, so Propose/Authorize/Apply's single-file commit path
// (commitChangedFiles' !info.IsDir() branch) gets exercised the same way
// POG's own pog/catalog.yaml (a single file, not a bundle) hits it — the
// bundle fixture above never exercises that branch at all. Adds a
// "changeset" type_registry entry on top of testdata/good/minimal.yaml
// (which doesn't declare one, and — unlike visibility-leak.yaml — is
// otherwise lint-clean) so Propose's own changeset node passes lint.
func copySingleFileFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/good/minimal.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	content := strings.Replace(string(raw), "type_registry:\n", "type_registry:\n  - id: changeset\n    schema: graph-type/v0\n    extends: core-node\n", 1)
	dst := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dst
}

func TestPropose_SingleFileCatalogCommitsCleanly(t *testing.T) {
	root := copySingleFileFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "Add a requirement",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-new", "title": "New requirement", "status": "draft", "visibility": "internal"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed on a single-file catalog, got: %v", res.RejectReasons)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := cat.Nodes[res.ChangesetID]; !ok {
		t.Fatalf("changeset node %q not found after propose on a single-file catalog", res.ChangesetID)
	}

	authRes, err := Authorize(root, res.ChangesetID, "", clock.Real())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if authRes.Rejected() {
		t.Fatalf("expected authorize to succeed, got rejected: %+v", authRes)
	}

	applyRes, err := Apply(root, res.ChangesetID, false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applyRes.Rejected() {
		t.Fatalf("expected apply to succeed on a single-file catalog, got rejected: %+v", applyRes)
	}

	final, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("final reload: %v", err)
	}
	if _, ok := final.Nodes["req-new"]; !ok {
		t.Error("req-new was not applied to the single-file catalog")
	}
	if final.Nodes[res.ChangesetID].Status != ChangesetStatusNotified {
		t.Errorf("changeset status = %q, want notified", final.Nodes[res.ChangesetID].Status)
	}
}

func TestPropose_AppendsChangesetNode(t *testing.T) {
	root := copyBundleFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "Add requirement two",
		Operations: []map[string]any{
			{
				"kind": "added",
				"after": map[string]any{
					"schema":     "graph/requirement/v0",
					"id":         "req-two",
					"title":      "Requirement two",
					"status":     "draft",
					"visibility": "public",
				},
			},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed, got reject reasons: %v", res.RejectReasons)
	}
	if res.ChangesetID == "" {
		t.Fatal("expected a changeset id")
	}
	if res.Status != ChangesetStatusProposed {
		t.Errorf("status = %q, want %q (no provenance -> always human-reviewed)", res.Status, ChangesetStatusProposed)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node, ok := cat.Nodes[res.ChangesetID]
	if !ok {
		t.Fatalf("changeset node %q not found in catalog", res.ChangesetID)
	}
	if node.TypeID != "changeset" {
		t.Errorf("node type = %q, want changeset", node.TypeID)
	}
	if node.Status != ChangesetStatusProposed {
		t.Errorf("node status = %q, want proposed", node.Status)
	}
	// The proposed operations themselves must NOT have been applied yet —
	// only the changeset node was written.
	if _, ok := cat.Nodes["req-two"]; ok {
		t.Fatal("req-two should not exist yet: propose must not apply its payload")
	}
}

func TestPropose_RejectsImmediateStaleBefore(t *testing.T) {
	root := copyBundleFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "Bad edit",
		Operations: []map[string]any{
			{
				"kind": "modified",
				"node": "req-one",
				"changes": []any{
					map[string]any{"path": []any{"status"}, "before": "satisfied", "after": "closed"},
				},
			},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) == 0 {
		t.Fatal("expected an immediate Before stale-check rejection (catalog has draft, not satisfied)")
	}
	if res.ChangesetID != "" {
		t.Fatal("a rejected propose must not assign/write a changeset id")
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, node := range cat.Nodes {
		if node.TypeID == "changeset" {
			t.Fatal("a rejected propose must not touch the catalog at all")
		}
	}
}

func TestPropose_RejectsAddingExistingNode(t *testing.T) {
	root := copyBundleFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "Duplicate",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-one", "title": "dup", "status": "draft", "visibility": "public"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) == 0 {
		t.Fatal("expected a rejection: req-one already exists")
	}
}

func TestAuthorize_FlipsProposedToAuthorized(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-auth", ChangesetStatusProposed, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Authorize(root, "change-auth", "", clock.Real())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected authorize to succeed, got rejected: %+v", res)
	}
	if !res.Applied {
		t.Fatal("expected Applied=true")
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cat.Nodes["change-auth"].Status != ChangesetStatusAuthorized {
		t.Errorf("status = %q, want authorized", cat.Nodes["change-auth"].Status)
	}
	// The changeset's own operations must still be un-applied — authorize
	// is a lifecycle flip only, never an apply.
	if cat.Nodes["req-one"].Status != "draft" {
		t.Errorf("req-one status = %q, want still draft (authorize must not apply operations)", cat.Nodes["req-one"].Status)
	}
}

func TestAuthorize_RejectsNonProposedStatus(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-already-auth", ChangesetStatusAuthorized, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Authorize(root, "change-already-auth", "", clock.Real())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected a rejection: changeset is already authorized, not proposed")
	}
}

func TestAuthorize_RejectsUnknownChangeset(t *testing.T) {
	root := copyBundleFixture(t)
	res, err := Authorize(root, "does-not-exist", "", clock.Real())
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected a rejection for an unknown changeset id")
	}
}

func TestApply_MarksNotifiedInSameCommit(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-notify", ChangesetStatusAuthorized, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Apply(root, "change-notify", false, "", clock.Real())
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
	if cat.Nodes["change-notify"].Status != ChangesetStatusNotified {
		t.Errorf("changeset status = %q, want notified (apply must mark notified in the same commit)", cat.Nodes["change-notify"].Status)
	}
	if cat.Nodes["req-one"].Status != "satisfied" {
		t.Errorf("req-one status = %q, want satisfied", cat.Nodes["req-one"].Status)
	}

	// Re-applying a now-"notified" (not "authorized") changeset must be
	// rejected — this is what makes the review queue able to tell applied
	// from pending.
	res2, err := Apply(root, "change-notify", false, "", clock.Real())
	if err != nil {
		t.Fatalf("re-Apply: %v", err)
	}
	if !res2.Rejected() {
		t.Fatal("expected a re-apply of a notified changeset to be rejected")
	}
}

func TestWithdraw_FlipsProposedToWithdrawn(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-withdraw", ChangesetStatusProposed, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Withdraw(root, "change-withdraw", "", clock.Real())
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected withdraw to succeed, got rejected: %+v", res)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cat.Nodes["change-withdraw"].Status != ChangesetStatusWithdrawn {
		t.Errorf("status = %q, want withdrawn", cat.Nodes["change-withdraw"].Status)
	}

	// A withdrawn changeset must not be authorizable or appliable.
	if res2, _ := Authorize(root, "change-withdraw", "", clock.Real()); !res2.Rejected() {
		t.Error("expected authorize on a withdrawn changeset to be rejected")
	}
}

func TestWithdraw_RejectsNotifiedChangeset(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-notified", ChangesetStatusNotified, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Withdraw(root, "change-notified", "", clock.Real())
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected withdraw of an already-notified (applied) changeset to be rejected")
	}
}

func TestPropose_SystemAuthoredAllowlistedFieldAutoAuthorizes(t *testing.T) {
	root := copyBundleFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "materialize write-back",
		Operations: []map[string]any{
			{
				"kind": "modified",
				"node": "feature-one",
				"changes": []any{
					map[string]any{"path": []any{"fields", "materialization"}, "before": nil, "after": map[string]any{"stage": "done"}},
				},
			},
		},
		Provenance: map[string]any{"job_id": "job-1", "story": "materialize-work-item"},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed, got: %v", res.RejectReasons)
	}
	if res.Status != ChangesetStatusAuthorized {
		t.Errorf("status = %q, want authorized (system-authored + allowlisted field -> auto-authorize)", res.Status)
	}
}

func TestPropose_SystemAuthoredNonAllowlistedFieldStaysProposed(t *testing.T) {
	root := copyBundleFixture(t)
	res, err := Propose(root, ProposeInput{
		Title: "system-authored content edit",
		Operations: []map[string]any{
			{
				"kind": "modified",
				"node": "feature-one",
				"changes": []any{
					map[string]any{"path": []any{"title"}, "before": "Feature one", "after": "Renamed"},
				},
			},
		},
		Provenance: map[string]any{"job_id": "job-1", "story": "materialize-work-item"},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if res.Status != ChangesetStatusProposed {
		t.Errorf("status = %q, want proposed (title is not an allowlisted auto-authorize field)", res.Status)
	}
}

func TestRebase_RefreshesStaleBeforeAndSucceeds(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-rebase", ChangesetStatusProposed, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)

	// Simulate someone else advancing req-one's status while this changeset
	// sat in the review queue — the changeset's own Before ("draft") is now
	// stale.
	reqPath := filepath.Join(root, "nodes", "requirements.yaml")
	raw, err := os.ReadFile(reqPath)
	if err != nil {
		t.Fatalf("read requirements.yaml: %v", err)
	}
	drifted := strings.Replace(string(raw), "status: draft", "status: in-review", 1)
	if drifted == string(raw) {
		t.Fatal("fixture setup: expected to find 'status: draft' in requirements.yaml")
	}
	if err := os.WriteFile(reqPath, []byte(drifted), 0o644); err != nil {
		t.Fatalf("write drifted requirements.yaml: %v", err)
	}

	// Before the rebase, authorize+apply would still nominally succeed
	// (Authorize/Apply don't re-run ValidateChangeset's stale-guard against
	// the live catalog the way Propose does) — but a fresh Propose replay of
	// the same operations would now be rejected as stale, which is exactly
	// the review-queue scenario Rebase exists to fix.
	res, err := Rebase(root, "change-rebase", "", clock.Real())
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if res.Rejected() {
		t.Fatalf("expected rebase to succeed (non-conflicting refresh), got rejected: %+v", res)
	}
	if !res.Applied {
		t.Fatal("expected Applied=true")
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	cs, err := ParseChangeset(cat.Nodes["change-rebase"])
	if err != nil {
		t.Fatalf("ParseChangeset: %v", err)
	}
	if len(cs.Operations) != 1 || len(cs.Operations[0].Changes) != 1 {
		t.Fatalf("unexpected operations shape after rebase: %+v", cs.Operations)
	}
	if got := cs.Operations[0].Changes[0].Before; got != "in-review" {
		t.Errorf("Before = %v, want refreshed to %q", got, "in-review")
	}
	if got := cs.Operations[0].Changes[0].After; got != "satisfied" {
		t.Errorf("After = %v, want unchanged %q", got, "satisfied")
	}
	// The changeset's own status must be untouched by rebase (still proposed).
	if cat.Nodes["change-rebase"].Status != ChangesetStatusProposed {
		t.Errorf("status = %q, want still proposed (rebase must not authorize)", cat.Nodes["change-rebase"].Status)
	}

	// A re-validate-on-open (what the review queue would do next) now
	// passes, since Before matches the live catalog.
	if reasons := ValidateChangeset(cs, cat); len(reasons) > 0 {
		t.Errorf("expected no stale-Before reasons after rebase, got: %v", reasons)
	}

	// The refreshed changeset still authorizes and applies cleanly, and its
	// own After ("satisfied") is what lands, not the drifted "in-review".
	if authRes, _ := Authorize(root, "change-rebase", "", clock.Real()); authRes.Rejected() {
		t.Fatalf("expected authorize to succeed after rebase, got rejected: %+v", authRes)
	}
	applyRes, err := Apply(root, "change-rebase", false, "", clock.Real())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applyRes.Rejected() {
		t.Fatalf("expected apply to succeed after rebase, got rejected: %+v", applyRes)
	}
	final, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("final reload: %v", err)
	}
	if final.Nodes["req-one"].Status != "satisfied" {
		t.Errorf("req-one status = %q, want satisfied", final.Nodes["req-one"].Status)
	}
}

func TestRebase_RejectsWhenNothingStale(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-fresh", ChangesetStatusProposed, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	res, err := Rebase(root, "change-fresh", "", clock.Real())
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected rebase to report nothing-to-refresh when Before already matches the live catalog")
	}
}

func TestRebase_RejectsNonProposedStatus(t *testing.T) {
	root := copyBundleFixture(t)
	writeChangeset(t, root, "change-authed", ChangesetStatusAuthorized, `    - kind: modified
      node: req-one
      changes:
        - path: [status]
          before: draft
          after: satisfied
`)
	reqPath := filepath.Join(root, "nodes", "requirements.yaml")
	raw, _ := os.ReadFile(reqPath)
	drifted := strings.Replace(string(raw), "status: draft", "status: in-review", 1)
	os.WriteFile(reqPath, []byte(drifted), 0o644)

	res, err := Rebase(root, "change-authed", "", clock.Real())
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if !res.Rejected() {
		t.Fatal("expected rebase to reject an already-authorized changeset (rebase is proposed-only)")
	}
}
