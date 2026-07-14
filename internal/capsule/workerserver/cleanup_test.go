package workerserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kitsoki/internal/capsule/environment"
	"kitsoki/internal/capsule/executor"
)

func TestCleanupRetainsNewestYoungActiveNonterminalAndInvalidRuns(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	worker := cleanupTestServer(t, now)
	source := strings.Repeat("a", 40)
	writeCleanupRun(t, worker, "private-run-a", source, "completed", now.Add(-72*time.Hour))
	writeCleanupRun(t, worker, "private-run-b", source, "failed", now.Add(-96*time.Hour))
	writeCleanupRun(t, worker, "private-run-c", source, "cancelled", now.Add(-time.Hour))
	writeCleanupRun(t, worker, "private-run-d", source, "running", time.Time{})
	writeCleanupRun(t, worker, "private-run-e", source, "completed", now.Add(-120*time.Hour))
	worker.active["private-run-e"] = func() {}
	if err := os.MkdirAll(worker.runDir("private-run-f"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(worker.runRecordPath("private-run-f"), []byte(`{"schema":"wrong"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	unreferenced := strings.Repeat("b", 40)
	writeCleanupSource(t, worker, unreferenced, now.Add(-96*time.Hour))

	policy := DefaultCleanupPolicy()
	policy.Apply = true
	policy.RetainTerminalRuns = 2
	summary, err := worker.Cleanup(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Outcome != "completed" || summary.Runs.Removed != 1 {
		t.Fatalf("summary = %#v", summary)
	}
	for _, id := range []string{"private-run-a", "private-run-c", "private-run-d", "private-run-e", "private-run-f"} {
		if _, err := os.Lstat(worker.runDir(id)); err != nil {
			t.Fatalf("retained run %s: %v", id, err)
		}
	}
	if _, err := os.Lstat(worker.runDir("private-run-b")); !os.IsNotExist(err) {
		t.Fatalf("old run still exists: %v", err)
	}
	if !summary.SourceCleanupBlocked || summary.Sources.DeferredUnsafe != 1 {
		t.Fatalf("source cleanup was not blocked by uncertain/active run state: %#v", summary.Sources)
	}
	if _, err := os.Lstat(worker.sourceDir(unreferenced)); err != nil {
		t.Fatalf("unreferenced source was removed despite uncertain run state: %v", err)
	}
	assertCleanupSummarySafeAndDurable(t, worker, summary, []string{"private-run-a", "private-run-b", "private-run-e", source, unreferenced})
}

func TestCleanupPlanThenRemovesOnlyAgedUnreferencedSources(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	worker := cleanupTestServer(t, now)
	keptSource := strings.Repeat("1", 40)
	deletedRunSource := strings.Repeat("2", 40)
	unreferencedSource := strings.Repeat("3", 40)
	youngSource := strings.Repeat("4", 40)
	writeCleanupRun(t, worker, "private-reference-a", keptSource, "completed", now.Add(-48*time.Hour))
	writeCleanupRun(t, worker, "private-reference-b", deletedRunSource, "completed", now.Add(-72*time.Hour))
	for _, source := range []string{keptSource, deletedRunSource, unreferencedSource} {
		writeCleanupSource(t, worker, source, now.Add(-72*time.Hour))
	}
	writeCleanupSource(t, worker, youngSource, now.Add(-time.Hour))
	if err := os.MkdirAll(filepath.Join(worker.cfg.Root, "sources", "invalid-source"), 0o700); err != nil {
		t.Fatal(err)
	}

	policy := DefaultCleanupPolicy()
	policy.RetainTerminalRuns = 1
	planned, err := worker.Cleanup(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if planned.Outcome != "planned" || planned.Runs.Eligible != 1 || planned.Runs.Removed != 0 || planned.Sources.Eligible != 2 || planned.Sources.Removed != 0 {
		t.Fatalf("plan = %#v", planned)
	}
	for _, source := range []string{keptSource, deletedRunSource, unreferencedSource, youngSource} {
		if _, err := os.Lstat(worker.sourceDir(source)); err != nil {
			t.Fatalf("plan mutated source %s: %v", source, err)
		}
	}

	policy.Apply = true
	applied, err := worker.Cleanup(context.Background(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if applied.Runs.Removed != 1 || applied.Sources.Removed != 2 || applied.Sources.RetainedReferenced != 1 || applied.Sources.RetainedYoung != 1 || applied.Sources.RetainedInvalid != 1 {
		t.Fatalf("apply = %#v", applied)
	}
	for _, source := range []string{keptSource, youngSource} {
		if _, err := os.Lstat(worker.sourceDir(source)); err != nil {
			t.Fatalf("retained source %s: %v", source, err)
		}
	}
	for _, source := range []string{deletedRunSource, unreferencedSource} {
		if _, err := os.Lstat(worker.sourceDir(source)); !os.IsNotExist(err) {
			t.Fatalf("eligible source %s still exists: %v", source, err)
		}
	}
	assertCleanupSummarySafeAndDurable(t, worker, applied, []string{"private-reference-a", "private-reference-b", keptSource, deletedRunSource, unreferencedSource})
}

func cleanupTestServer(t *testing.T, now time.Time) *Server {
	t.Helper()
	worker, err := New(Config{
		Root: t.TempDir(),
		Now:  func() time.Time { return now },
		Runner: func(context.Context, string, executor.Prepared, string) (executor.Result, error) {
			return executor.Result{}, nil
		},
		Environment: EnvironmentVerifierFunc(func(context.Context, string, environment.Lock) error { return nil }),
	})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}

func writeCleanupRun(t *testing.T, worker *Server, id, source, status string, terminalAt time.Time) {
	t.Helper()
	startedAt := terminalAt.Add(-time.Hour)
	updatedAt := terminalAt
	if terminalAt.IsZero() {
		startedAt = worker.cfg.Now().Add(-72 * time.Hour)
		updatedAt = worker.cfg.Now().Add(-time.Hour)
	}
	record := RunRecord{Schema: RunRecordSchema, ExecutionID: id, EnvelopeDigest: "sha256:envelope", SourceDigest: source, StoryDigest: "sha256:story", Status: status, Stage: "fixture", StartedAt: startedAt, UpdatedAt: updatedAt, TerminalAt: terminalAt}
	if err := worker.writeRun(record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worker.runDir(id), "fixture.log"), []byte("bounded fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeCleanupSource(t *testing.T, worker *Server, head string, storedAt time.Time) {
	t.Helper()
	dir := worker.sourceDir(head)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := []byte("source bundle fixture")
	if err := os.WriteFile(worker.sourceBundlePath(head), bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	meta := SourceMeta{Schema: SourceMetaSchema, Head: head, BundleDigest: "sha256:bundle", Size: int64(len(bundle)), StoredAt: storedAt}
	if err := writeJSONFile(worker.sourceMetaPath(head), meta); err != nil {
		t.Fatal(err)
	}
}

func assertCleanupSummarySafeAndDurable(t *testing.T, worker *Server, summary CleanupSummary, forbidden []string) {
	t.Helper()
	if err := ProviderSafeCleanupSummary(summary); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(worker.cfg.Root, "cleanup", "latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var durable CleanupSummary
	if err := json.Unmarshal(raw, &durable); err != nil {
		t.Fatal(err)
	}
	if durable.Schema != CleanupSummarySchema || durable.Outcome != summary.Outcome {
		t.Fatalf("durable summary = %#v", durable)
	}
	text := string(raw)
	for _, value := range append(forbidden, worker.cfg.Root) {
		if strings.Contains(text, value) {
			t.Fatalf("durable summary leaked %q: %s", value, text)
		}
	}
}
