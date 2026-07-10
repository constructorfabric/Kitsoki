package artifactjob

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"kitsoki/internal/app"
	_ "modernc.org/sqlite"
)

func TestMemoryStoreRegisterBindListArchive(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	now := time.Date(2026, 7, 8, 1, 2, 3, 0, time.UTC)
	store.SetClock(func() time.Time { return now })

	job, err := store.Register(ctx, RegisterRequest{
		ID:        "job-1",
		AppID:     "dev-story",
		Story:     "stories/dev-story",
		Origin:    Origin{Kind: "dev-story", Ref: "design:artifact-driven-stories"},
		Summary:   "draft design",
		Phase:     "design_brief",
		SessionID: "session-a",
	})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if job.Status != StatusRunning || job.Visibility != VisibilityLocal {
		t.Fatalf("defaults = status %q visibility %q", job.Status, job.Visibility)
	}

	job, err = store.BindRun(ctx, job.ID, "session-b", RunURL("http://127.0.0.1:7331", job.ID), "/tmp/trace.jsonl")
	if err != nil {
		t.Fatalf("BindRun: %v", err)
	}
	if job.SessionID != "session-b" || job.RunURL != "http://127.0.0.1:7331/run/job-1" || job.TracePath == "" {
		t.Fatalf("bound job = %+v", job)
	}

	rows, err := store.List(ctx, ListFilter{AppID: "dev-story"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 || rows[0].ID != job.ID {
		t.Fatalf("List rows = %+v", rows)
	}

	if _, err := store.Archive(ctx, job.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	rows, err = store.List(ctx, ListFilter{AppID: "dev-story"})
	if err != nil {
		t.Fatalf("List archived filtered: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("archived row should be hidden by default: %+v", rows)
	}
}

func TestSQLiteStoreSweepInterrupted(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStoreForTest(t)

	running, err := store.Register(ctx, RegisterRequest{ID: "running", Status: StatusRunning, SessionID: "session-a"})
	if err != nil {
		t.Fatalf("Register running: %v", err)
	}
	if _, err := store.Register(ctx, RegisterRequest{ID: "done", Status: StatusDone, SessionID: "session-a"}); err != nil {
		t.Fatalf("Register done: %v", err)
	}
	n, err := store.SweepInterrupted(ctx, "process restart")
	if err != nil {
		t.Fatalf("SweepInterrupted: %v", err)
	}
	if n != 1 {
		t.Fatalf("swept %d rows, want 1", n)
	}
	got, err := store.Get(ctx, running.ID)
	if err != nil {
		t.Fatalf("Get swept: %v", err)
	}
	if got.Status != StatusInterrupted || got.InterruptedReason != "process restart" {
		t.Fatalf("swept job = %+v", got)
	}
}

func TestSQLiteStoreRunIndex(t *testing.T) {
	ctx := context.Background()
	store := newSQLiteStoreForTest(t)
	job, err := store.Register(ctx, RegisterRequest{ID: "job-idx", Status: StatusDone, SessionID: "session-idx", Story: "stories/dev-story"})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	ended := time.Date(2026, 7, 8, 3, 0, 0, 0, time.UTC)
	if err := store.UpsertRun(ctx, Run{JobID: job.ID, SessionID: job.SessionID, Story: job.Story, Status: StatusDone, StartedAt: ended.Add(-time.Minute), EndedAt: &ended, LastTurn: 7, TracePath: "/tmp/run.jsonl"}); err != nil {
		t.Fatalf("UpsertRun: %v", err)
	}
	artifact := Artifact{Handle: "video#1", JobID: job.ID, Kind: "video", MIME: "video/mp4", Label: "demo", Path: "/tmp/demo.mp4", SizeBytes: 42, CreatedAt: ended}
	if err := store.UpsertArtifact(ctx, artifact); err != nil {
		t.Fatalf("UpsertArtifact: %v", err)
	}
	got, err := store.ResolveArtifact(ctx, job.ID, artifact.Handle)
	if err != nil {
		t.Fatalf("ResolveArtifact: %v", err)
	}
	if got.Path != artifact.Path || got.MIME != artifact.MIME {
		t.Fatalf("artifact = %+v", got)
	}
	if _, err := store.ResolveArtifact(ctx, job.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing artifact err = %v", err)
	}
}

func newSQLiteStoreForTest(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewSQLiteStore(db)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	store.SetClock(func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) })
	return store
}

var _ Store = (*MemoryStore)(nil)
var _ RunIndex = (*MemoryStore)(nil)
var _ Store = (*SQLiteStore)(nil)
var _ RunIndex = (*SQLiteStore)(nil)
var _ = app.SessionID("")
