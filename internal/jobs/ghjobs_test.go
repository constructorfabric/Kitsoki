package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestGHStore(t *testing.T) *GHJobStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := NewGHJobStore(db)
	if err != nil {
		t.Fatalf("NewGHJobStore: %v", err)
	}
	return s
}

// TestClaim_WinThenAttach proves Claim is idempotent on origin_ref: the first
// mention wins a fresh queued->claimed CAS, a re-mention attaches to the SAME
// row (won=false), and exactly one row exists.
func TestClaim_WinThenAttach(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	m := GHMention{OriginRef: "github:o/r/issue/42", Repo: "o/r", ObjectKind: "issue", ObjectNumber: "42"}

	job1, won1, err := s.Claim(ctx, m, "w1")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if !won1 {
		t.Fatalf("first claim should win")
	}
	if job1.State != GHClaimed {
		t.Errorf("state = %q, want claimed", job1.State)
	}
	if job1.WorkerID != "w1" {
		t.Errorf("worker = %q, want w1", job1.WorkerID)
	}

	job2, won2, err := s.Claim(ctx, m, "w2")
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if won2 {
		t.Errorf("re-mention should NOT win the claim")
	}
	if job2.JobID != job1.JobID {
		t.Errorf("re-mention minted a new job: %q vs %q", job2.JobID, job1.JobID)
	}
	if job2.WorkerID != "w1" {
		t.Errorf("re-mention stole the claim: worker = %q, want w1", job2.WorkerID)
	}

	// Exactly one row.
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gh_jobs`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("gh_jobs row count = %d, want 1", count)
	}
}

func TestGHJobEventsAndStuckListing(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	job, won, err := s.Claim(ctx, GHMention{
		OriginRef:    "github:o/r/issue/9",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "9",
	}, "w1")
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if !won {
		t.Fatal("first claim did not win")
	}
	if err := s.SetStory(ctx, job.JobID, "stories/bugfix"); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(ctx, job.JobID, GHRunning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE gh_jobs SET updated_at=? WHERE job_id=?`, time.Now().Add(-time.Hour).UnixMilli(), job.JobID); err != nil {
		t.Fatalf("age job: %v", err)
	}
	stuck, err := s.ListStuck(ctx, time.Now().Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("ListStuck: %v", err)
	}
	if len(stuck) != 1 || stuck[0].JobID != job.JobID {
		t.Fatalf("stuck=%+v, want job %s", stuck, job.JobID)
	}
	if _, err := s.BumpAttempt(ctx, job.JobID); err != nil {
		t.Fatalf("BumpAttempt: %v", err)
	}
	if err := s.SetIncidentURL(ctx, job.JobID, "https://github.com/o/r/issues/123"); err != nil {
		t.Fatalf("SetIncidentURL: %v", err)
	}
	events, err := s.Events(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var sawRunning, sawAttempt, sawIncident bool
	for _, ev := range events {
		switch ev.State {
		case GHRunning:
			sawRunning = true
		case "attempt":
			sawAttempt = true
		case "incident":
			sawIncident = true
		}
	}
	if !sawRunning || !sawAttempt || !sawIncident {
		t.Fatalf("events missing expected lifecycle states: %+v", events)
	}
	got, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.AttemptCount != 1 || got.IncidentURL == "" {
		t.Fatalf("attempt/incident not persisted: %+v", got)
	}
}

func TestListQueuedReturnsOnlyQueuedJobs(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	queuedJob, _, err := s.Claim(ctx, GHMention{
		OriginRef:    "github:o/r/issue/1",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "1",
	}, "w1")
	if err != nil {
		t.Fatalf("Claim queued: %v", err)
	}
	if err := s.Advance(ctx, queuedJob.JobID, GHQueued, "retry"); err != nil {
		t.Fatalf("requeue: %v", err)
	}
	runningJob, _, err := s.Claim(ctx, GHMention{
		OriginRef:    "github:o/r/pr/2",
		Repo:         "o/r",
		ObjectKind:   "pr",
		ObjectNumber: "2",
	}, "w1")
	if err != nil {
		t.Fatalf("Claim running: %v", err)
	}
	if err := s.Advance(ctx, runningJob.JobID, GHRunning, ""); err != nil {
		t.Fatalf("mark running: %v", err)
	}

	queued, err := s.ListQueued(ctx, 10)
	if err != nil {
		t.Fatalf("ListQueued: %v", err)
	}
	if len(queued) != 1 || queued[0].JobID != queuedJob.JobID {
		t.Fatalf("queued=%+v, want only queued job %s", queued, queuedJob.JobID)
	}
}

func TestEnqueueCreatesQueuedJobIdempotently(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	m := GHMention{
		OriginRef:    "github:o/r/issue/105",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "105",
	}

	job1, created1, err := s.Enqueue(ctx, m, "stories/bugfix")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !created1 {
		t.Fatal("first enqueue should create a job")
	}
	if job1.State != GHQueued || job1.Story != "stories/bugfix" {
		t.Fatalf("job after enqueue = %+v, want queued stories/bugfix", job1)
	}

	job2, created2, err := s.Enqueue(ctx, m, "stories/dev-story")
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if created2 {
		t.Fatal("second enqueue should attach to existing job")
	}
	if job2.JobID != job1.JobID {
		t.Fatalf("second enqueue minted new job %s, want %s", job2.JobID, job1.JobID)
	}
	if job2.Story != "stories/bugfix" {
		t.Fatalf("second enqueue overwrote story: %q", job2.Story)
	}

	queued, err := s.ListQueued(ctx, 10)
	if err != nil {
		t.Fatalf("ListQueued: %v", err)
	}
	if len(queued) != 1 || queued[0].JobID != job1.JobID {
		t.Fatalf("queued=%+v, want only %s", queued, job1.JobID)
	}
}

func TestMergeMetadataPersistsAndOverlaysKeys(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	job, _, err := s.Enqueue(ctx, GHMention{
		OriginRef:    "github:o/r/issue/105",
		Repo:         "o/r",
		ObjectKind:   "issue",
		ObjectNumber: "105",
	}, "stories/bugfix")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if err := s.MergeMetadata(ctx, job.JobID, map[string]string{
		"ticket_title": "Still-live UI bug",
		"ticket_body":  "Steps and evidence",
		"blank":        " ",
	}); err != nil {
		t.Fatalf("MergeMetadata first: %v", err)
	}
	if err := s.MergeMetadata(ctx, job.JobID, map[string]string{
		"ticket_body":        "Updated evidence",
		"ticket_source_mode": "remote",
	}); err != nil {
		t.Fatalf("MergeMetadata second: %v", err)
	}
	got, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Metadata["ticket_title"] != "Still-live UI bug" {
		t.Fatalf("ticket_title = %q", got.Metadata["ticket_title"])
	}
	if got.Metadata["ticket_body"] != "Updated evidence" {
		t.Fatalf("ticket_body = %q", got.Metadata["ticket_body"])
	}
	if got.Metadata["ticket_source_mode"] != "remote" {
		t.Fatalf("ticket_source_mode = %q", got.Metadata["ticket_source_mode"])
	}
	if _, ok := got.Metadata["blank"]; ok {
		t.Fatalf("blank metadata key should have been discarded: %+v", got.Metadata)
	}

	events, err := s.Events(ctx, job.JobID)
	if err != nil {
		t.Fatalf("Events: %v", err)
	}
	var metadataEvents int
	for _, ev := range events {
		if ev.State == "metadata" {
			metadataEvents++
		}
	}
	if metadataEvents != 2 {
		t.Fatalf("metadata events = %d, want 2", metadataEvents)
	}
}

func TestAdvanceAndSetters(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	m := GHMention{OriginRef: "github:o/r/issue/7", Repo: "o/r", ObjectKind: "issue", ObjectNumber: "7"}
	job, _, err := s.Claim(ctx, m, "w1")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := s.SetStory(ctx, job.JobID, "stories/bugfix"); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(ctx, job.JobID, GHRunning, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.SetComment(ctx, job.JobID, "c-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunURL(ctx, job.JobID, "run-1", "kitsoki://run/run-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(ctx, job.JobID, GHDone, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(ctx, job.JobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Story != "stories/bugfix" || got.State != GHDone || got.CommentID != "c-1" || got.RunURL != "kitsoki://run/run-1" {
		t.Errorf("unexpected job after lifecycle: %+v", got)
	}
}

func TestListRecentOrdersByUpdatedAt(t *testing.T) {
	ctx := context.Background()
	s := newTestGHStore(t)
	older, _, err := s.Claim(ctx, GHMention{OriginRef: "github:o/r/issue/1", Repo: "o/r", ObjectKind: "issue", ObjectNumber: "1"}, "w1")
	if err != nil {
		t.Fatalf("claim older: %v", err)
	}
	newer, _, err := s.Claim(ctx, GHMention{OriginRef: "github:o/r/pr/2", Repo: "o/r", ObjectKind: "pr", ObjectNumber: "2"}, "w1")
	if err != nil {
		t.Fatalf("claim newer: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE gh_jobs SET updated_at=? WHERE job_id=?`, time.Now().Add(-time.Hour).UnixMilli(), older.JobID); err != nil {
		t.Fatalf("age older: %v", err)
	}
	recent, err := s.ListRecent(ctx, 1)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("recent len=%d, want 1", len(recent))
	}
	if recent[0].JobID != newer.JobID {
		t.Fatalf("recent[0]=%s, want newer %s", recent[0].JobID, newer.JobID)
	}
}
