package graph

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/clock"
)

// fixedClockTime is the timestamp every stamps test pins the fake clock to
// — an arbitrary, easy-to-eyeball RFC3339 UTC instant.
var fixedClockTime = time.Date(2026, 3, 4, 12, 30, 0, 0, time.UTC)

func TestPropose_StampsCreatedAtAndAuthoredBy(t *testing.T) {
	root := copyBundleFixture(t)
	fake := clock.NewFake(fixedClockTime)
	res, err := Propose(root, ProposeInput{
		Title: "Add requirement",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-stamped", "title": "Stamped", "status": "draft", "visibility": "public"}},
		},
	}, "alice", fake)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed, got: %v", res.RejectReasons)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node, ok := cat.Nodes[res.ChangesetID]
	if !ok {
		t.Fatalf("changeset node %q not found", res.ChangesetID)
	}
	wantTS := "2026-03-04T12:30:00Z"
	if got, _ := node.Fields["created_at"].(string); got != wantTS {
		t.Errorf("created_at = %q, want %q", got, wantTS)
	}
	if got, _ := node.Fields["authored_by"].(string); got != "alice" {
		t.Errorf("authored_by = %q, want %q", got, "alice")
	}
}

func TestPropose_NoActorSkipsAuthoredByStamp(t *testing.T) {
	root := copyBundleFixture(t)
	fake := clock.NewFake(fixedClockTime)
	res, err := Propose(root, ProposeInput{
		Title: "Add requirement",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-stamped2", "title": "Stamped", "status": "draft", "visibility": "public"}},
		},
	}, "", fake)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed, got: %v", res.RejectReasons)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node := cat.Nodes[res.ChangesetID]
	if _, ok := node.Fields["authored_by"]; ok {
		t.Errorf("authored_by should be absent when actor is empty, got %v", node.Fields["authored_by"])
	}
	if got, _ := node.Fields["created_at"].(string); got != "2026-03-04T12:30:00Z" {
		t.Errorf("created_at = %q, want the fixed-clock stamp", got)
	}
}

func TestAuthorize_StampsAuthorizedAtAndBy(t *testing.T) {
	root := copyBundleFixture(t)
	proposeRes, err := Propose(root, ProposeInput{
		Title: "Add requirement",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-auth-stamp", "title": "Stamped", "status": "draft", "visibility": "public"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}

	fake := clock.NewFake(fixedClockTime)
	authRes, err := Authorize(root, proposeRes.ChangesetID, "bob", fake)
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if authRes.Rejected() {
		t.Fatalf("expected authorize to succeed, got rejected: %+v", authRes)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node := cat.Nodes[proposeRes.ChangesetID]
	wantTS := "2026-03-04T12:30:00Z"
	if got, _ := node.Fields["authorized_at"].(string); got != wantTS {
		t.Errorf("authorized_at = %q, want %q", got, wantTS)
	}
	if got, _ := node.Fields["authorized_by"].(string); got != "bob" {
		t.Errorf("authorized_by = %q, want %q", got, "bob")
	}
}

func TestApply_StampsAppliedAt(t *testing.T) {
	root := copyBundleFixture(t)
	proposeRes, err := Propose(root, ProposeInput{
		Title: "Add requirement",
		Operations: []map[string]any{
			{"kind": "added", "after": map[string]any{"schema": "graph/requirement/v0", "id": "req-apply-stamp", "title": "Stamped", "status": "draft", "visibility": "public"}},
		},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if _, err := Authorize(root, proposeRes.ChangesetID, "", clock.Real()); err != nil {
		t.Fatalf("Authorize: %v", err)
	}

	fake := clock.NewFake(fixedClockTime)
	applyRes, err := Apply(root, proposeRes.ChangesetID, false, "", fake)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if applyRes.Rejected() {
		t.Fatalf("expected apply to succeed, got rejected: %+v", applyRes)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node := cat.Nodes[proposeRes.ChangesetID]
	wantTS := "2026-03-04T12:30:00Z"
	if got, _ := node.Fields["applied_at"].(string); got != wantTS {
		t.Errorf("applied_at = %q, want %q", got, wantTS)
	}
}

// writePolicySingleFileFixture is copySingleFileFixture (propose_test.go)
// plus a `write_policy:` block declaring a custom auto-authorize allowlist
// root ("custom_field") that is NOT in the hardcoded autoAuthorizeFieldRoots
// fallback — proving Propose consults the catalog's declared policy instead
// of (not merely in addition to) the Go constant.
func writePolicySingleFileFixture(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/good/minimal.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	content := strings.Replace(string(raw),
		"type_registry:\n",
		"type_registry:\n  - id: changeset\n    schema: graph-type/v0\n    extends: core-node\n",
		1)
	content = strings.Replace(content,
		"type_registry:\n",
		"write_policy:\n  auto_authorize_field_roots: [\"custom_field\"]\ntype_registry:\n",
		1)
	dst := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dst
}

func TestPropose_WritePolicyOverridesDefaultAllowlist(t *testing.T) {
	root := writePolicySingleFileFixture(t)

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cat.WritePolicy == nil {
		t.Fatal("expected write_policy to be parsed")
	}
	if got := cat.WritePolicy.AutoAuthorizeFieldRoots; len(got) != 1 || got[0] != "custom_field" {
		t.Fatalf("write_policy.auto_authorize_field_roots = %v, want [custom_field]", got)
	}

	// "custom_field" is not in the hardcoded default allowlist
	// (materialization/evidence only) — this only auto-authorizes because
	// the catalog's own write_policy block declared it.
	res, err := Propose(root, ProposeInput{
		Title: "system write",
		Operations: []map[string]any{
			{
				"kind": "modified",
				"node": "feature-one",
				"changes": []any{
					map[string]any{"path": []any{"fields", "custom_field"}, "before": nil, "after": "value"},
				},
			},
		},
		Provenance: map[string]any{"job_id": "job-1", "story": "custom-write"},
	}, "", clock.Real())
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(res.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed, got: %v", res.RejectReasons)
	}
	if res.Status != ChangesetStatusAuthorized {
		t.Errorf("status = %q, want authorized (write_policy declares custom_field auto-authorizable)", res.Status)
	}
}

func TestPropose_DefaultAllowlistAppliesWhenNoWritePolicyDeclared(t *testing.T) {
	root := copyBundleFixture(t)
	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cat.WritePolicy != nil {
		t.Fatal("expected no write_policy block on the bundle fixture")
	}
	got := cat.WritePolicyOrDefault().AutoAuthorizeFieldRoots
	want := map[string]bool{"materialization": true, "evidence": true}
	if len(got) != len(want) {
		t.Fatalf("default write policy roots = %v, want keys of %v", got, want)
	}
	for _, r := range got {
		if !want[r] {
			t.Errorf("unexpected default root %q", r)
		}
	}
}

// TestRebase_PreservesCreatedAtAndAuthoredByStamps guards the merge-time
// integration of fad888bf7's Rebase into the P1 stamps seam: Rebase rewrites
// only the changeset node's "operations" field (a single FieldChange at
// ["fields", "operations"]) — created_at/authored_by, stamped by the
// originating Propose call, live under different Fields keys entirely and
// must survive byte-for-byte.
func TestRebase_PreservesCreatedAtAndAuthoredByStamps(t *testing.T) {
	root := copyBundleFixture(t)
	fake := clock.NewFake(fixedClockTime)
	proposeRes, err := Propose(root, ProposeInput{
		Title: "Flip req-one to satisfied",
		Operations: []map[string]any{
			{
				"kind": "modified",
				"node": "req-one",
				"changes": []any{
					map[string]any{"path": []any{"status"}, "before": "draft", "after": "satisfied"},
				},
			},
		},
	}, "alice", fake)
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if len(proposeRes.RejectReasons) > 0 {
		t.Fatalf("expected propose to succeed, got: %v", proposeRes.RejectReasons)
	}

	// Someone else advances req-one's status while the changeset sits in the
	// review queue, staling out the changeset's own Before — the same
	// fixture drift propose_test.go's Rebase tests use.
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

	rebaseRes, err := Rebase(root, proposeRes.ChangesetID, "carol", clock.Real())
	if err != nil {
		t.Fatalf("Rebase: %v", err)
	}
	if rebaseRes.Rejected() {
		t.Fatalf("expected rebase to succeed (non-conflicting refresh), got rejected: %+v", rebaseRes)
	}

	cat, err := LoadCatalog(root)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	node, ok := cat.Nodes[proposeRes.ChangesetID]
	if !ok {
		t.Fatalf("changeset node %q not found after rebase", proposeRes.ChangesetID)
	}
	wantTS := "2026-03-04T12:30:00Z"
	if got, _ := node.Fields["created_at"].(string); got != wantTS {
		t.Errorf("created_at = %q, want %q (rebase must not touch it)", got, wantTS)
	}
	if got, _ := node.Fields["authored_by"].(string); got != "alice" {
		t.Errorf("authored_by = %q, want %q (rebase actor %q must not overwrite the original author)", got, "alice", "carol")
	}
	// Sanity: the rebase itself actually happened (Before refreshed).
	cs, err := ParseChangeset(node)
	if err != nil {
		t.Fatalf("ParseChangeset: %v", err)
	}
	if len(cs.Operations) != 1 || len(cs.Operations[0].Changes) != 1 {
		t.Fatalf("unexpected operations shape after rebase: %+v", cs.Operations)
	}
	if got := cs.Operations[0].Changes[0].Before; got != "in-review" {
		t.Errorf("Before = %v, want refreshed to %q", got, "in-review")
	}
}

func TestLoadCatalog_ParsesFeedbackRouting(t *testing.T) {
	raw, err := os.ReadFile("testdata/good/minimal.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	content := strings.Replace(string(raw),
		"type_registry:\n",
		"feedback_routing:\n  type: evidence\n  fields: [\"notes\"]\n  edges: [\"related_to\"]\ntype_registry:\n",
		1)
	dst := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cat, err := LoadCatalog(dst)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cat.FeedbackRouting == nil {
		t.Fatal("expected feedback_routing to be parsed")
	}
	if cat.FeedbackRouting.Type != "evidence" {
		t.Errorf("feedback_routing.type = %q, want evidence", cat.FeedbackRouting.Type)
	}
	if len(cat.FeedbackRouting.Fields) != 1 || cat.FeedbackRouting.Fields[0] != "notes" {
		t.Errorf("feedback_routing.fields = %v, want [notes]", cat.FeedbackRouting.Fields)
	}
	if len(cat.FeedbackRouting.Edges) != 1 || cat.FeedbackRouting.Edges[0] != "related_to" {
		t.Errorf("feedback_routing.edges = %v, want [related_to]", cat.FeedbackRouting.Edges)
	}
}
