package materialize

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/graph"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

func TestResolveChecks_ScriptFieldAndInputsMerge(t *testing.T) {
	node := &graph.Node{
		ID:     "wi",
		TypeID: "checked-work-item",
		Fields: map[string]any{
			"check":        "checks/judge.star",
			"check_inputs": map[string]any{"want": "yes", "extra": 1},
		},
	}
	decls := []graph.MaterializeCheckDecl{{
		ID:          "gate",
		ScriptField: "check",
		Inputs:      map[string]any{"want": "overridden-by-node", "base": true},
		InputsField: "check_inputs",
	}}

	got := ResolveChecks(node, decls)
	if len(got) != 1 {
		t.Fatalf("ResolveChecks returned %d checks, want 1", len(got))
	}
	rc := got[0]
	if rc.Unresolved != "" {
		t.Fatalf("unexpected Unresolved: %q", rc.Unresolved)
	}
	if rc.Script != "checks/judge.star" {
		t.Errorf("Script = %q, want checks/judge.star", rc.Script)
	}
	// Node inputs override declaration literals; declaration-only keys survive.
	if rc.Inputs["want"] != "yes" || rc.Inputs["base"] != true || rc.Inputs["extra"] != 1 {
		t.Errorf("merged Inputs = %+v", rc.Inputs)
	}
}

func TestResolveChecks_MissingScriptFieldIsUnresolved(t *testing.T) {
	node := &graph.Node{ID: "wi", TypeID: "checked-work-item", Fields: map[string]any{}}
	got := ResolveChecks(node, []graph.MaterializeCheckDecl{{ID: "gate", ScriptField: "check"}})
	if len(got) != 1 || got[0].Unresolved == "" {
		t.Fatalf("want one unresolved check, got %+v", got)
	}
	result := RunCheck(context.Background(), t.TempDir(), got[0])
	if result.OK {
		t.Fatal("unresolved check must not pass")
	}
	if !strings.Contains(result.Error, "names no .star assertion script") {
		t.Errorf("Error = %q", result.Error)
	}
}

// TestRunCheck_FsRootPinnedToRepoRoot proves a check's ctx.fs resolves
// against the materialize repo root even when the process-global
// KITSOKI_APP_DIR points at a different repo — the exact race that made the
// web-session materialize path report "no evidence recorded" for evidence
// the CLI path (same catalog, same check) read fine. RunCheck pins the
// sandbox root via the world.workdir override, which outranks AppDirEnv.
func TestRunCheck_FsRootPinnedToRepoRoot(t *testing.T) {
	root := t.TempDir()
	writeFile := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeFile("checks/probe.star", "def main(ctx):\n    return {\"ok\": ctx.fs.exists(\"evidence/e.json\")}\n")
	writeFile("checks/probe.star.yaml", "inputs: {}\noutputs:\n  ok: { type: bool }\n")
	writeFile("evidence/e.json", "{}")

	// A concurrently seeded session on ANOTHER repo won the process-global
	// env var — the check's verdict must not care.
	t.Setenv(host.AppDirEnv, t.TempDir())

	rc := ResolvedCheck{
		ID:           "gate",
		Script:       "checks/probe.star",
		Inputs:       map[string]any{},
		Capabilities: map[string]any{"fs": map[string]any{"read": []any{"evidence/**"}}},
	}
	result := RunCheck(context.Background(), root, rc)
	if result.Error != "" {
		t.Fatalf("RunCheck error: %s", result.Error)
	}
	if !result.OK {
		t.Fatalf("check judged against the wrong root: %+v", result)
	}
}

// driveCheckedNode runs Start against the disposable fixture copy for nodeID
// and returns the fixture root, terminal job, and collected stage events.
func driveCheckedNode(t *testing.T, nodeID string) (string, *jobs.Job, []StageEvent) {
	t.Helper()
	sched := jobs.NewInMemoryScheduler()
	ctx := context.Background()
	fixtureRoot := copyTestdataFixture(t)

	jobID, _, err := Start(ctx, sched, Request{
		CatalogPath: filepath.Join(fixtureRoot, "catalog.yaml"),
		RepoRoot:    fixtureRoot,
		NodeID:      graph.NodeID(nodeID),
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

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
			t.Fatal("timed out waiting for job to finish")
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
	return fixtureRoot, &job, events
}

func TestStart_GateCheckPassCompletesJob(t *testing.T) {
	fixtureRoot, job, events := driveCheckedNode(t, "wi-check-pass")
	if job.Status != jobs.JobDone {
		t.Fatalf("job status = %v, error = %q, want done", job.Status, job.Error)
	}

	last := events[len(events)-1]
	if last.Stage != "check:gate" || last.Status != "complete" {
		t.Errorf("last stage event = %+v, want check:gate complete", last)
	}

	checks, _ := job.Result.Data["checks"].([]map[string]any)
	if len(checks) != 1 {
		t.Fatalf("result checks = %+v, want one entry", job.Result.Data["checks"])
	}
	if checks[0]["ok"] != true || checks[0]["script"] != "checks/judge.star" {
		t.Errorf("check result = %+v", checks[0])
	}
	if sha, _ := checks[0]["script_sha256"].(string); len(sha) != 64 {
		t.Errorf("script_sha256 = %v, want 64 hex chars", checks[0]["script_sha256"])
	}
	repro, _ := checks[0]["reproduce"].(string)
	if !strings.HasPrefix(repro, "kitsoki starlark run checks/judge.star --inputs ") || !strings.Contains(repro, `"want":"yes"`) {
		t.Errorf("reproduce = %q", repro)
	}

	// The write-back materialization block records the verdict durably.
	cat, err := graph.LoadCatalog(filepath.Join(fixtureRoot, "catalog.yaml"))
	if err != nil {
		t.Fatalf("reload catalog: %v", err)
	}
	mat, _ := cat.Nodes["wi-check-pass"].Fields["materialization"].(map[string]any)
	if mat == nil {
		t.Fatal("no materialization block written back")
	}
	if mat["status"] != "complete" {
		t.Errorf("materialization status = %v", mat["status"])
	}
	recorded, _ := mat["checks"].([]any)
	if len(recorded) != 1 {
		t.Fatalf("materialization checks = %+v", mat["checks"])
	}
	entry, _ := recorded[0].(map[string]any)
	if entry["ok"] != true || entry["id"] != "gate" {
		t.Errorf("recorded check = %+v", entry)
	}
}

func TestStart_GateCheckFailFailsJob(t *testing.T) {
	fixtureRoot, job, events := driveCheckedNode(t, "wi-check-fail")
	if job.Status != jobs.JobFailed {
		t.Fatalf("job status = %v, want failed", job.Status)
	}
	if !strings.Contains(job.Error, `gate check "gate" failed`) || !strings.Contains(job.Error, "want is no, not yes") {
		t.Errorf("job error = %q", job.Error)
	}

	last := events[len(events)-1]
	if last.Stage != "check:gate" || last.Status != "failed" {
		t.Errorf("last stage event = %+v, want check:gate failed", last)
	}

	cat, err := graph.LoadCatalog(filepath.Join(fixtureRoot, "catalog.yaml"))
	if err != nil {
		t.Fatalf("reload catalog: %v", err)
	}
	mat, _ := cat.Nodes["wi-check-fail"].Fields["materialization"].(map[string]any)
	if mat == nil || mat["status"] != "failed" {
		t.Fatalf("materialization = %+v, want status failed", mat)
	}
	recorded, _ := mat["checks"].([]any)
	if len(recorded) != 1 {
		t.Fatalf("materialization checks = %+v", mat["checks"])
	}
	entry, _ := recorded[0].(map[string]any)
	if entry["ok"] != false {
		t.Errorf("recorded check = %+v, want ok false", entry)
	}
}

func TestStart_MissingCheckScriptFailsJob(t *testing.T) {
	_, job, events := driveCheckedNode(t, "wi-check-missing")
	if job.Status != jobs.JobFailed {
		t.Fatalf("job status = %v, want failed", job.Status)
	}
	if !strings.Contains(job.Error, "names no .star assertion script") {
		t.Errorf("job error = %q", job.Error)
	}
	last := events[len(events)-1]
	if last.Stage != "check:gate" || last.Status != "failed" {
		t.Errorf("last stage event = %+v, want check:gate failed", last)
	}
}
