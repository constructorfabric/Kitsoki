package inbox_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"kitsoki/internal/app"
	"kitsoki/internal/inbox"
	"kitsoki/internal/jobs"
	"kitsoki/internal/world"

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

func TestRefreshSummary(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	// Insert two notifications: one info, one action_required.
	ctx := context.Background()
	sessionID := app.SessionID("test-session")

	err = js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     sessionID,
		CreatedAt:     time.Now(),
		Severity:      jobs.SeverityInfo,
		Title:         "Info",
		TeleportState: "main",
		OriginKind:    "job",
		OriginRef:     "job:1",
	})
	if err != nil {
		t.Fatalf("insert info: %v", err)
	}

	err = js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     sessionID,
		CreatedAt:     time.Now(),
		Severity:      jobs.SeverityActionRequired,
		Title:         "Needs attention",
		TeleportState: "main",
		OriginKind:    "job",
		OriginRef:     "job:2",
	})
	if err != nil {
		t.Fatalf("insert action_required: %v", err)
	}

	w := world.New()
	w, err = inbox.RefreshSummary(ctx, js, sessionID, w)
	if err != nil {
		t.Fatalf("RefreshSummary: %v", err)
	}

	inboxVal, ok := w.Vars[inbox.WorldKey]
	if !ok {
		t.Fatal("expected $inbox in world vars")
	}

	m, ok := inboxVal.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", inboxVal)
	}
	if m["unread"] != 2 {
		t.Fatalf("expected unread=2, got %v", m["unread"])
	}
	if m["needs_attention"] != 1 {
		t.Fatalf("expected needs_attention=1, got %v", m["needs_attention"])
	}
}

func TestFromNotification(t *testing.T) {
	n := jobs.Notification{
		TeleportState:      "reviewing",
		TeleportSlots:      map[string]any{"cmd": "ls"},
		TeleportProposalID: "prop-abc",
		TeleportJobID:      "job-xyz",
	}
	target := inbox.FromNotification(n)
	if target.State != "reviewing" {
		t.Fatalf("expected reviewing, got %s", target.State)
	}
	if target.ProposalID != "prop-abc" {
		t.Fatalf("expected prop-abc, got %s", target.ProposalID)
	}
	if target.JobID != "job-xyz" {
		t.Fatalf("expected job-xyz, got %s", target.JobID)
	}
	if target.Slots["cmd"] != "ls" {
		t.Fatalf("expected cmd=ls, got %v", target.Slots["cmd"])
	}
}
