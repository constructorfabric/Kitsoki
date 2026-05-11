package jobs_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"kitsoki/internal/jobs"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func makeTestJob(id string, status jobs.JobStatus) *jobs.Job {
	now := time.Now()
	return &jobs.Job{
		ID:          id,
		SessionID:   "sess-test",
		Kind:        "host.test",
		Status:      status,
		OriginState: "terminal",
		Payload:     map[string]any{},
		CreatedAt:   now,
		UpdatedAt:   now,
		StartedAt:   &now,
	}
}

func TestJobStore_UpsertAndList(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	now := time.Now()
	j := &jobs.Job{
		ID:          "01J0000000000000000000001A",
		SessionID:   "sess-1",
		Kind:        "host.run",
		Status:      jobs.JobRunning,
		OriginState: "terminal",
		Payload:     map[string]any{"cmd": "echo hi"},
		CreatedAt:   now,
		UpdatedAt:   now,
		StartedAt:   &now,
	}

	if err := js.UpsertJob(context.Background(), j); err != nil {
		t.Fatalf("UpsertJob: %v", err)
	}

	listed, err := js.ListJobsByStatus(context.Background(), "sess-1", jobs.JobRunning)
	if err != nil {
		t.Fatalf("ListJobsByStatus: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("expected 1 job, got %d", len(listed))
	}
	if listed[0].ID != j.ID {
		t.Fatalf("expected id %s, got %s", j.ID, listed[0].ID)
	}
}

func TestJobStore_Notifications(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	n := &jobs.Notification{
		SessionID:     "sess-1",
		CreatedAt:     time.Now(),
		Severity:      jobs.SeveritySuccess,
		Title:         "Job done",
		Body:          "Tests passed.",
		TeleportState: "reviewing",
		OriginKind:    "job",
		OriginRef:     "job:abc123",
	}
	if err := js.InsertNotification(context.Background(), n); err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}

	counts, err := js.UnreadCount(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if counts[jobs.SeveritySuccess] != 1 {
		t.Fatalf("expected 1 unread success notification, got %v", counts)
	}

	notifs, err := js.ListNotifications(context.Background(), "sess-1", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(notifs))
	}
	if notifs[0].Title != "Job done" {
		t.Fatalf("expected title 'Job done', got %q", notifs[0].Title)
	}

	// Mark as read.
	if err := js.MarkNotificationRead(context.Background(), n.ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}

	counts, err = js.UnreadCount(context.Background(), "sess-1")
	if err != nil {
		t.Fatalf("UnreadCount after read: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("expected 0 unread after marking read, got %v", counts)
	}
}
