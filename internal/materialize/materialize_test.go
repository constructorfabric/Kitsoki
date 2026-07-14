package materialize

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/graph"
	"kitsoki/internal/jobs"
)

// copyTestdataFixture copies the whole testdata/ tree (catalog.yaml + the
// fixture story) into a fresh t.TempDir() and returns the copy's root.
// Start()'s write-back (writeback.go) edits the catalog file in place and
// writes an artifact file under RepoRoot/.artifacts/ — a test that drives a
// job to completion must run against a disposable copy of both, not the
// checked-in fixture, or every test run would leave it mutated with the
// previous run's job id/timestamp and a stray .artifacts/ directory.
func copyTestdataFixture(t *testing.T) string {
	t.Helper()
	dstRoot := t.TempDir()
	err := filepath.WalkDir(testdataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(testdataDir, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, raw, 0o644)
	})
	if err != nil {
		t.Fatalf("copy testdata fixture: %v", err)
	}
	return dstRoot
}

func loadTestStoryApp(t *testing.T) *app.AppDef {
	t.Helper()
	def, err := app.Load(testdataDir + "/story/app.yaml")
	if err != nil {
		t.Fatalf("app.Load: %v", err)
	}
	return def
}

const testdataDir = "testdata"
const testCatalogPath = testdataDir + "/catalog.yaml"

func loadTestCatalog(t *testing.T) *graph.Catalog {
	t.Helper()
	cat, err := graph.LoadCatalog(testCatalogPath)
	if err != nil {
		t.Fatalf("graph.LoadCatalog(%s): %v", testCatalogPath, err)
	}
	return cat
}

func TestResolveBinding(t *testing.T) {
	cat := loadTestCatalog(t)

	node := cat.Nodes[graph.NodeID("wi-ready")]
	binding, err := ResolveBinding(cat, node)
	if err != nil {
		t.Fatalf("ResolveBinding: %v", err)
	}
	if binding.Story != "story" {
		t.Errorf("Story = %q, want %q", binding.Story, "story")
	}
	if got, want := binding.Gates, []string{"gate", "owner"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Gates = %v, want %v", got, want)
	}
	if len(binding.Params) != 2 || binding.Params[0].ID != "depth" || binding.Params[1].ID != "audience" {
		t.Errorf("Params = %+v, want depth then audience", binding.Params)
	}

	plain := cat.Nodes[graph.NodeID("plain-one")]
	if _, err := ResolveBinding(cat, plain); err == nil {
		t.Error("ResolveBinding on a type with no materialize: binding should error")
	}
}

func TestUnmetGates(t *testing.T) {
	cat := loadTestCatalog(t)

	ready := cat.Nodes[graph.NodeID("wi-ready")]
	if unmet := UnmetGates(ready, []string{"gate", "owner"}); len(unmet) != 0 {
		t.Errorf("UnmetGates(wi-ready) = %v, want none", unmet)
	}

	notReady := cat.Nodes[graph.NodeID("wi-not-ready")]
	unmet := UnmetGates(notReady, []string{"gate", "owner"})
	if got, want := unmet, []string{"gate", "owner"}; !reflect.DeepEqual(got, want) {
		t.Errorf("UnmetGates(wi-not-ready) = %v, want %v", got, want)
	}
}

func TestMaterializeReadinessResolvesPartialFromGraph(t *testing.T) {
	cat := loadTestCatalog(t)
	node := cat.Nodes[graph.NodeID("wi-ready")]
	binding, err := ResolveBinding(cat, node)
	if err != nil {
		t.Fatalf("ResolveBinding: %v", err)
	}
	binding.Params = []graph.MaterializeParamDecl{
		{ID: "owner", Type: "string", Required: true, SourceField: "owner"},
		{ID: "phases", Type: "list", Required: true, SourceEdge: graph.EdgeField("in_phase")},
		{ID: "audience", Type: "string", Required: true},
	}

	notReady := MaterializeReadiness(cat, node, binding, nil)
	if notReady.Ready {
		t.Fatal("Readiness.Ready = true, want false while audience is absent")
	}
	if got, want := notReady.MissingParams, []string{"audience"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("MissingParams = %v, want %v", got, want)
	}
	if got, want := notReady.Params["phases"], []string{"phase-one"}; !reflect.DeepEqual(got, want) {
		t.Errorf("edge-backed phases = %#v, want %#v", got, want)
	}

	ready := MaterializeReadiness(cat, node, binding, map[string]any{"audience": "internal"})
	if !ready.Ready {
		t.Fatalf("Readiness.Ready = false: %+v", ready)
	}
	emptyEdgeOverride := MaterializeReadiness(cat, node, binding, map[string]any{"audience": "internal", "phases": []string{}})
	if !emptyEdgeOverride.Ready || !reflect.DeepEqual(emptyEdgeOverride.Params["phases"], []string{"phase-one"}) {
		t.Fatalf("empty override must not erase graph-backed required params: %+v", emptyEdgeOverride)
	}
}

func TestContextClosure(t *testing.T) {
	cat := loadTestCatalog(t)

	closure := ContextClosure(cat, graph.NodeID("wi-ready"), []graph.EdgeField{"in_phase"})
	got := make([]string, len(closure))
	for i, id := range closure {
		got[i] = string(id)
	}
	sort.Strings(got)
	if want := []string{"phase-one"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ContextClosure = %v, want %v", got, want)
	}

	// Filtering out the only declared edge kind yields an empty closure.
	if empty := ContextClosure(cat, graph.NodeID("wi-ready"), nil); len(empty) != 0 {
		t.Errorf("ContextClosure with no edge kinds = %v, want empty", empty)
	}
}

func TestStart_GateValidation_RejectsWithUnmetList(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()
	ctx := context.Background()

	_, _, err := Start(ctx, sched, Request{
		CatalogPath: testCatalogPath,
		RepoRoot:    testdataDir,
		NodeID:      graph.NodeID("wi-not-ready"),
	})
	if err == nil {
		t.Fatal("Start on a node with unmet gates should error")
	}
	var gateErr *GateError
	if !errors.As(err, &gateErr) {
		t.Fatalf("error = %v, want *GateError", err)
	}
	if got, want := gateErr.Unmet, []string{"gate", "owner"}; !reflect.DeepEqual(got, want) {
		t.Errorf("GateError.Unmet = %v, want %v", got, want)
	}
}

func TestStart_DrivesStoryAndHeartbeats(t *testing.T) {
	sched := jobs.NewInMemoryScheduler()
	ctx := context.Background()
	fixtureRoot := copyTestdataFixture(t)
	catalogPath := filepath.Join(fixtureRoot, "catalog.yaml")

	jobID, stages, err := Start(ctx, sched, Request{
		CatalogPath: catalogPath,
		RepoRoot:    fixtureRoot,
		NodeID:      graph.NodeID("wi-ready"),
		Params:      map[string]any{"depth": 3, "audience": "public"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if jobID == "" {
		t.Fatal("Start returned empty job id")
	}

	wantStages := []Stage{{ID: "gather", Status: "waiting"}, {ID: "draft", Status: "waiting"}, {ID: "done", Status: "waiting"}}
	if !reflect.DeepEqual(stages, wantStages) {
		t.Errorf("initial stages = %+v, want %+v", stages, wantStages)
	}

	// Collect every stage heartbeat fanned out for this job, in order.
	ch, unsub := sched.Subscribe(jobID)
	defer unsub()

	var events []StageEvent
	timeout := time.After(5 * time.Second)
collect:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break collect
			}
			if se, ok := ev.Progress.(StageEvent); ok {
				events = append(events, se)
			}
			if ev.Status == jobs.JobDone || ev.Status == jobs.JobFailed {
				break collect
			}
		case <-timeout:
			t.Fatal("timed out waiting for job to complete")
		}
	}

	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := sched.WaitIdle(waitCtx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}

	job, ok := sched.Get(jobID)
	if !ok {
		t.Fatal("Get: job not found")
	}
	if job.Status != jobs.JobDone {
		t.Fatalf("job status = %v, error = %q, want done", job.Status, job.Error)
	}

	wantEvents := []StageEvent{
		{Stage: "gather", Status: "in-progress"},
		{Stage: "gather", Status: "complete"},
		{Stage: "draft", Status: "in-progress"},
		{Stage: "draft", Status: "complete"},
		{Stage: "done", Status: "in-progress"},
		{Stage: "done", Status: "complete"},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Errorf("stage events = %+v, want %+v", events, wantEvents)
	}

	if job.Result == nil {
		t.Fatal("job.Result is nil")
	}
	if got := job.Result.Data["artifact_path"]; got != ".artifacts/wi-ready/brief.md" {
		t.Errorf("artifact_path = %v, want %q", got, ".artifacts/wi-ready/brief.md")
	}

	// Write-back (slice 6): the artifact's content actually landed on disk
	// under RepoRoot, and the catalog's node block gained an evidence entry
	// plus a materialization: block reflecting the completed job.
	artifactBytes, err := os.ReadFile(filepath.Join(fixtureRoot, ".artifacts", "wi-ready", "brief.md"))
	if err != nil {
		t.Fatalf("read written artifact: %v", err)
	}
	if len(artifactBytes) == 0 {
		t.Error("written artifact is empty")
	}
	if got, want := string(artifactBytes), "# Script-produced fixture\n\nExact producer bytes."; got != want {
		t.Errorf("written artifact = %q, want script-produced bytes %q", got, want)
	}

	// The write-back's own audit trail (§3.3/§3.4: system-authored
	// changesets, not a splice) — every mutation went through
	// graph.Propose/Apply, so both writes land as their own notified
	// changeset node, in addition to the target node's fields below.
	catalogAfterWriteback, err := graph.LoadCatalog(catalogPath)
	if err != nil {
		t.Fatalf("write-back produced unparseable catalog: %v", err)
	}
	n := catalogAfterWriteback.Nodes[graph.NodeID("wi-ready")]
	if n == nil {
		t.Fatal("wi-ready missing from write-back catalog")
	}

	ev, _ := n.Fields["evidence"].([]any)
	if len(ev) != 1 {
		t.Fatalf("evidence = %v, want 1 entry", ev)
	}
	if entry := ev[0].(map[string]any); entry["kind"] != "doc" || entry["path"] != ".artifacts/wi-ready/brief.md" {
		t.Errorf("evidence entry = %v, unexpected shape", entry)
	}

	m, ok := n.Fields["materialization"].(map[string]any)
	if !ok {
		t.Fatalf("materialization field missing or wrong shape: %v", n.Fields["materialization"])
	}
	if m["job_id"] != string(jobID) || m["status"] != "complete" || m["story"] != "story" {
		t.Errorf("materialization = %v", m)
	}
	wantStageStatuses := []map[string]any{
		{"id": "gather", "status": "complete"},
		{"id": "draft", "status": "complete"},
		{"id": "done", "status": "complete"},
	}
	gotStages, _ := m["stages"].([]any)
	if len(gotStages) != len(wantStageStatuses) {
		t.Fatalf("materialization stages = %v, want %v", gotStages, wantStageStatuses)
	}
	for i, want := range wantStageStatuses {
		got, _ := gotStages[i].(map[string]any)
		if got["id"] != want["id"] || got["status"] != want["status"] {
			t.Errorf("materialization stage %d = %v, want %v", i, got, want)
		}
	}

	changesetCount := 0
	for _, node := range catalogAfterWriteback.Nodes {
		if node.TypeID == "changeset" {
			changesetCount++
			if node.Status != "notified" {
				t.Errorf("write-back changeset %s status = %q, want notified", node.ID, node.Status)
			}
		}
	}
	if changesetCount != 2 {
		t.Errorf("changeset count = %d, want 2 (one evidence append, one materialization write)", changesetCount)
	}

	// Round-trips as valid YAML with the node's other fields (id/gate/owner/
	// edges) intact — the surgical field replace must not have corrupted
	// the surrounding block.
	if n.Fields["owner"] != "brad" {
		t.Errorf("owner field corrupted by write-back: %v", n.Fields["owner"])
	}
	if len(n.Edges["in_phase"]) != 1 {
		t.Errorf("in_phase edge corrupted by write-back: %v", n.Edges["in_phase"])
	}
}

func TestRoomSequence(t *testing.T) {
	def := loadTestStoryApp(t)
	seq, err := RoomSequence(def)
	if err != nil {
		t.Fatalf("RoomSequence: %v", err)
	}
	if want := []string{"gather", "draft", "done"}; !reflect.DeepEqual(seq, want) {
		t.Errorf("RoomSequence = %v, want %v", seq, want)
	}
}

// TestStart_PrivateRigRunsHostVerbs proves the private drive rig wires the
// builtin host registry: hoststory's produce room writes a file via
// host.run, which only happens if the invoke actually dispatches (a rig
// without a registry treats it as a silent no-op — the bug that made
// `kitsoki graph materialize` run producer rooms without producing).
func TestStart_PrivateRigRunsHostVerbs(t *testing.T) {
	outPath := filepath.Join(t.TempDir(), "produced.txt")
	t.Setenv("MATERIALIZE_HOST_TEST_OUT", outPath)

	fixtureRoot, job, _ := driveCheckedNode(t, "wi-host-run")
	_ = fixtureRoot
	if job.Status != jobs.JobDone {
		t.Fatalf("job status = %v, error = %q, want done", job.Status, job.Error)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("host.run never produced %s: %v", outPath, err)
	}
	if string(data) != "produced" {
		t.Fatalf("produced file content = %q, want %q", string(data), "produced")
	}
}
