package materialize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/graph"
)

// writebackFixture is a minimal single-node catalog exercising the three
// insertion shapes AppendEvidence/WriteMaterialization must handle: a node
// with no evidence: or materialization: block yet (bare), one that already
// has an evidence: list, and one that already has a materialization: block
// (re-run/status-update case).
const writebackFixture = `schema: project-object-graph/seed-catalog/v0
catalog:
  id: writeback-fixture
type_registry:
  - id: core-node
    schema: graph-type/v0
    extends: null
    required_fields: [id, schema, title, status, visibility]
  - id: phase
    schema: graph-type/v0
    extends: core-node
  - id: changeset
    schema: graph-type/v0
    extends: core-node
  - id: work-item
    schema: graph-type/v0
    extends: core-node
    edge_fields:
      - id: in_phase
        target_type: phase
        cardinality: one
nodes:
  - schema: graph/phase/v0
    id: phase-one
    title: Phase one
    status: active
    visibility: internal
  - schema: graph/work-item/v0
    id: bare-node
    title: "Bare node" # trailing comment must survive
    status: active
    visibility: internal
    gate: "reviewers assigned"
    edges:
      in_phase: [phase-one]
  - schema: graph/work-item/v0
    id: with-evidence
    title: With evidence
    status: active
    visibility: internal
    evidence:
      - kind: doc
        title: Existing doc
        path: docs/existing.md
  - schema: graph/work-item/v0
    id: with-materialization
    title: With materialization
    status: active
    visibility: internal
    materialization:
      job_id: "old-job"
      status: in-progress
      story: story
      stages:
        - { id: gather, status: complete }
        - { id: draft, status: in-progress }
      artifacts: []
`

func writeFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.yaml")
	if err := os.WriteFile(path, []byte(writebackFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func mustLoad(t *testing.T, path string) *graph.Catalog {
	t.Helper()
	cat, err := graph.LoadCatalog(path)
	if err != nil {
		t.Fatalf("graph.LoadCatalog(%s): %v", path, err)
	}
	return cat
}

func TestAppendEvidence_NoExistingBlock(t *testing.T) {
	path := writeFixture(t)
	if err := AppendEvidence(path, "bare-node", EvidenceEntry{Kind: "doc", Title: "Implementation brief", Path: ".artifacts/bare-node/brief.md"}, "job1", "stories/materialize-work-item"); err != nil {
		t.Fatalf("AppendEvidence: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	if !strings.Contains(text, "title: \"Bare node\" # trailing comment must survive") {
		t.Error("write-back disturbed an unrelated field's trailing comment")
	}

	cat := mustLoad(t, path)
	n := cat.Nodes[graph.NodeID("bare-node")]
	if n == nil {
		t.Fatal("bare-node missing after write-back")
	}
	if got := n.Fields["gate"]; got != "reviewers assigned" {
		t.Errorf("gate field corrupted: %v", got)
	}
	if len(n.Edges["in_phase"]) != 1 || n.Edges["in_phase"][0] != graph.NodeID("phase-one") {
		t.Errorf("edges corrupted: %v", n.Edges["in_phase"])
	}
	ev, _ := n.Fields["evidence"].([]any)
	if len(ev) != 1 {
		t.Fatalf("evidence = %v, want 1 entry inserted", ev)
	}
	entry := ev[0].(map[string]any)
	if entry["title"] != "Implementation brief" || entry["path"] != ".artifacts/bare-node/brief.md" || entry["kind"] != "doc" {
		t.Errorf("evidence entry = %v, unexpected shape", entry)
	}

	// The write-back's own audit trail: a system-authored, auto-authorized
	// (then applied, so "notified") changeset node recording this exact
	// field change, per §3.3/§3.4's "system-authored changeset, not a dev
	// splice" mechanism.
	found := false
	for _, node := range cat.Nodes {
		if node.TypeID != "changeset" {
			continue
		}
		found = true
		if node.Status != "notified" {
			t.Errorf("write-back changeset %s status = %q, want notified (proposed -> auto-authorized -> applied)", node.ID, node.Status)
		}
	}
	if !found {
		t.Error("no changeset node found after write-back — AppendEvidence should go through graph.Propose/Apply, not a direct splice")
	}
}

func TestAppendEvidence_AppendsAfterExisting(t *testing.T) {
	path := writeFixture(t)
	if err := AppendEvidence(path, "with-evidence", EvidenceEntry{Kind: "doc", Title: "New brief", Path: ".artifacts/with-evidence/brief.md"}, "job1", "stories/materialize-work-item"); err != nil {
		t.Fatalf("AppendEvidence: %v", err)
	}
	cat := mustLoad(t, path)
	n := cat.Nodes[graph.NodeID("with-evidence")]
	ev, _ := n.Fields["evidence"].([]any)
	if len(ev) != 2 {
		t.Fatalf("evidence = %v, want 2 entries", ev)
	}
	first := ev[0].(map[string]any)
	if first["path"] != "docs/existing.md" {
		t.Errorf("existing evidence entry disturbed: %v", first)
	}
	second := ev[1].(map[string]any)
	if second["path"] != ".artifacts/with-evidence/brief.md" {
		t.Errorf("new evidence entry not appended: %v", second)
	}
}

func TestAppendEvidence_DedupesByPath(t *testing.T) {
	path := writeFixture(t)
	entry := EvidenceEntry{Kind: "doc", Title: "New brief", Path: ".artifacts/with-evidence/brief.md"}
	if err := AppendEvidence(path, "with-evidence", entry, "job1", "stories/materialize-work-item"); err != nil {
		t.Fatalf("AppendEvidence (1st): %v", err)
	}
	if err := AppendEvidence(path, "with-evidence", entry, "job1", "stories/materialize-work-item"); err != nil {
		t.Fatalf("AppendEvidence (2nd): %v", err)
	}
	cat := mustLoad(t, path)
	n := cat.Nodes[graph.NodeID("with-evidence")]
	ev, _ := n.Fields["evidence"].([]any)
	if len(ev) != 2 {
		t.Fatalf("evidence = %v, want 2 entries (no duplicate from re-run)", ev)
	}
}

func TestWriteMaterialization_InsertsNewBlock(t *testing.T) {
	path := writeFixture(t)
	rec := MaterializationRecord{
		JobID:  "01JOB",
		Status: "complete",
		Story:  "stories/materialize-work-item",
		Stages: []Stage{{ID: "gather", Status: "complete"}, {ID: "draft", Status: "complete"}},
		Artifacts: []MaterializationArtifact{
			{Kind: "doc", Title: "Implementation brief", Path: ".artifacts/bare-node/brief.md", ProducedAt: "2026-07-10T00:00:00Z"},
		},
	}
	if err := WriteMaterialization(path, "bare-node", rec); err != nil {
		t.Fatalf("WriteMaterialization: %v", err)
	}
	cat := mustLoad(t, path)
	n := cat.Nodes[graph.NodeID("bare-node")]
	m, ok := n.Fields["materialization"].(map[string]any)
	if !ok {
		t.Fatalf("materialization field missing or wrong shape: %v", n.Fields["materialization"])
	}
	if m["job_id"] != "01JOB" || m["status"] != "complete" || m["story"] != "stories/materialize-work-item" {
		t.Errorf("materialization = %v", m)
	}
	stages, _ := m["stages"].([]any)
	if len(stages) != 2 {
		t.Fatalf("stages = %v", stages)
	}
	// Node still round-trips with its other fields intact.
	if n.Edges["in_phase"] == nil {
		t.Error("in_phase edge lost after inserting materialization: block")
	}
}

func TestWriteMaterialization_ReplacesExistingBlock(t *testing.T) {
	path := writeFixture(t)
	rec := MaterializationRecord{
		JobID:  "01NEWJOB",
		Status: "complete",
		Story:  "story",
		Stages: []Stage{{ID: "gather", Status: "complete"}, {ID: "draft", Status: "complete"}},
		Artifacts: []MaterializationArtifact{
			{Kind: "doc", Title: "Brief", Path: ".artifacts/with-materialization/brief.md", ProducedAt: "2026-07-10T00:00:00Z"},
		},
	}
	if err := WriteMaterialization(path, "with-materialization", rec); err != nil {
		t.Fatalf("WriteMaterialization: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(raw)
	// old-job may still appear inside the changeset audit-trail node's own
	// "before" bookkeeping — the real assertion is that the NODE's own
	// materialization: block (not the changeset's operations list) no
	// longer carries it, checked below via the parsed field.
	_ = text

	cat := mustLoad(t, path)
	n := cat.Nodes[graph.NodeID("with-materialization")]
	m := n.Fields["materialization"].(map[string]any)
	if m["job_id"] != "01NEWJOB" || m["status"] != "complete" {
		t.Errorf("materialization = %v, want updated job_id/status", m)
	}
	if m["job_id"] == "old-job" {
		t.Error("stale materialization: block was not fully replaced")
	}
}

func TestNodeBlockRange_UnknownNode(t *testing.T) {
	path := writeFixture(t)
	if err := AppendEvidence(path, "no-such-node", EvidenceEntry{Kind: "doc", Title: "x", Path: "x.md"}, "job1", "stories/materialize-work-item"); err == nil {
		t.Error("AppendEvidence on an unknown node id should error")
	}
	if err := WriteMaterialization(path, "no-such-node", MaterializationRecord{}); err == nil {
		t.Error("WriteMaterialization on an unknown node id should error")
	}
}
