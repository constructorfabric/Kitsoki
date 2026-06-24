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

func TestJobStore_SweepStaleJobs(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	ctx := context.Background()

	// Seed: one running, one awaiting_input, one already-done, one already-failed.
	for _, j := range []*jobs.Job{
		makeTestJob("01J0000000000000000000001R", jobs.JobRunning),
		makeTestJob("01J0000000000000000000001W", jobs.JobAwaitingInput),
		makeTestJob("01J0000000000000000000001D", jobs.JobDone),
		makeTestJob("01J0000000000000000000001F", jobs.JobFailed),
	} {
		if err := js.UpsertJob(ctx, j); err != nil {
			t.Fatalf("UpsertJob: %v", err)
		}
	}

	n, err := js.SweepStaleJobs(ctx)
	if err != nil {
		t.Fatalf("SweepStaleJobs: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 rows swept, got %d", n)
	}

	// Verify the sweep targeted only running/awaiting_input rows.
	for _, tc := range []struct {
		id   string
		want jobs.JobStatus
	}{
		{"01J0000000000000000000001R", jobs.JobFailed},
		{"01J0000000000000000000001W", jobs.JobFailed},
		{"01J0000000000000000000001D", jobs.JobDone},
		{"01J0000000000000000000001F", jobs.JobFailed},
	} {
		got, err := js.GetJob(ctx, tc.id)
		if err != nil {
			t.Fatalf("GetJob(%s): %v", tc.id, err)
		}
		if got.Status != tc.want {
			t.Errorf("%s: status = %q, want %q", tc.id, got.Status, tc.want)
		}
		if tc.want == jobs.JobFailed && tc.id != "01J0000000000000000000001F" {
			// Swept rows should have the ErrProcessDied marker; the pre-existing
			// failed row had no error string set.
			if got.Error != jobs.ErrProcessDied {
				t.Errorf("%s: error = %q, want %q", tc.id, got.Error, jobs.ErrProcessDied)
			}
		}
	}

	// Second sweep is a no-op.
	n2, err := js.SweepStaleJobs(ctx)
	if err != nil {
		t.Fatalf("second SweepStaleJobs: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 rows swept on second pass, got %d", n2)
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

func TestJobStore_ListByStatusAcrossSessions(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	now := time.Now()
	for _, j := range []*jobs.Job{
		{
			ID:        "job-current",
			SessionID: "sess-1",
			Kind:      "host.run",
			Status:    jobs.JobRunning,
			Payload:   map[string]any{"n": 1},
			CreatedAt: now.Add(-2 * time.Minute),
			UpdatedAt: now.Add(-1 * time.Minute),
		},
		{
			ID:        "job-other",
			SessionID: "sess-2",
			Kind:      "host.agent.task",
			Status:    jobs.JobAwaitingInput,
			Payload:   map[string]any{"n": 2},
			CreatedAt: now.Add(-3 * time.Minute),
			UpdatedAt: now,
		},
		{
			ID:        "job-done",
			SessionID: "sess-3",
			Kind:      "host.done",
			Status:    jobs.JobDone,
			Payload:   map[string]any{"n": 3},
			CreatedAt: now.Add(-4 * time.Minute),
			UpdatedAt: now,
		},
	} {
		if err := js.UpsertJob(context.Background(), j); err != nil {
			t.Fatalf("UpsertJob(%s): %v", j.ID, err)
		}
	}

	got, err := js.ListByStatus(context.Background(), []jobs.JobStatus{jobs.JobRunning, jobs.JobAwaitingInput})
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d jobs, want 2: %+v", len(got), got)
	}
	if got[0].ID != "job-other" || got[0].SessionID != "sess-2" {
		t.Fatalf("first job = %+v, want job-other with session", got[0])
	}
	if got[1].ID != "job-current" || got[1].SessionID != "sess-1" {
		t.Fatalf("second job = %+v, want job-current with session", got[1])
	}
}

func TestJobStore_ListNotificationsAll(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	ctx := context.Background()
	now := time.Now()
	current := &jobs.Notification{
		SessionID: "sess-1",
		CreatedAt: now.Add(-time.Minute),
		Severity:  jobs.SeverityInfo,
		Title:     "Current note",
	}
	other := &jobs.Notification{
		SessionID: "sess-2",
		CreatedAt: now,
		Severity:  jobs.SeverityActionRequired,
		Title:     "Other needs input",
	}
	dismissed := &jobs.Notification{
		SessionID: "sess-3",
		CreatedAt: now.Add(time.Minute),
		Severity:  jobs.SeverityWarn,
		Title:     "Dismissed",
	}
	for _, n := range []*jobs.Notification{current, other, dismissed} {
		if err := js.InsertNotification(ctx, n); err != nil {
			t.Fatalf("InsertNotification(%s): %v", n.Title, err)
		}
	}
	if err := js.DismissNotification(ctx, dismissed.ID); err != nil {
		t.Fatalf("DismissNotification: %v", err)
	}

	got, err := js.ListNotificationsAll(ctx, 0)
	if err != nil {
		t.Fatalf("ListNotificationsAll: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d notifications, want 2: %+v", len(got), got)
	}
	if got[0].ID != other.ID || got[0].SessionID != "sess-2" {
		t.Fatalf("first notification = %+v, want other with session", got[0])
	}
	if got[1].ID != current.ID || got[1].SessionID != "sess-1" {
		t.Fatalf("second notification = %+v, want current with session", got[1])
	}
}

func TestJobStore_InsertExternalNotificationOnce(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	ctx := context.Background()

	first := &jobs.Notification{
		SessionID:     "sess-1",
		Severity:      jobs.SeverityActionRequired,
		Title:         "PR #42 needs review",
		Body:          "Review requested by alice.",
		TeleportState: "inbox",
		TeleportSlots: map[string]any{"pr_id": "42", "pr_title": "Add tests"},
		OriginRef:     "github:pr/42",
		OriginURL:     "https://github.com/acme/repo/pull/42",
	}
	inserted, err := js.InsertExternalNotificationOnce(ctx, first)
	if err != nil {
		t.Fatalf("InsertExternalNotificationOnce(first): %v", err)
	}
	if !inserted {
		t.Fatalf("expected first poll result to insert")
	}
	if first.ID == "" {
		t.Fatalf("expected inserted notification ID")
	}

	duplicate := &jobs.Notification{
		SessionID:     "sess-1",
		Severity:      jobs.SeverityActionRequired,
		Title:         "PR #42 needs review again",
		TeleportState: "inbox",
		OriginRef:     "github:pr/42",
		OriginURL:     "https://github.com/acme/repo/pull/42",
	}
	inserted, err = js.InsertExternalNotificationOnce(ctx, duplicate)
	if err != nil {
		t.Fatalf("InsertExternalNotificationOnce(duplicate): %v", err)
	}
	if inserted {
		t.Fatalf("expected duplicate poll result to be ignored")
	}
	if duplicate.ID != first.ID {
		t.Fatalf("expected duplicate to return existing ID %q, got %q", first.ID, duplicate.ID)
	}

	notifs, err := js.ListNotifications(ctx, "sess-1", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected one stored notification, got %d", len(notifs))
	}
	if notifs[0].OriginKind != "external" || notifs[0].OriginURL != first.OriginURL {
		t.Fatalf("expected external origin and URL to round trip, got kind=%q url=%q", notifs[0].OriginKind, notifs[0].OriginURL)
	}
	if got := notifs[0].TeleportSlots["pr_id"]; got != "42" {
		t.Fatalf("expected teleport slot pr_id=42, got %#v", got)
	}

	counts, err := js.UnreadCount(ctx, "sess-1")
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if counts[jobs.SeverityActionRequired] != 1 {
		t.Fatalf("expected one unread action-required notification, got %v", counts)
	}
}

func TestJobStore_InsertExternalNotificationOnce_DedupesReadAndDismissedRows(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	ctx := context.Background()

	n := &jobs.Notification{
		SessionID:     "sess-1",
		Severity:      jobs.SeverityActionRequired,
		Title:         "Issue #7 assigned",
		TeleportState: "inbox",
		OriginRef:     "github:issue/7",
	}
	inserted, err := js.InsertExternalNotificationOnce(ctx, n)
	if err != nil {
		t.Fatalf("InsertExternalNotificationOnce(first): %v", err)
	}
	if !inserted {
		t.Fatalf("expected first poll result to insert")
	}
	if err := js.MarkNotificationRead(ctx, n.ID); err != nil {
		t.Fatalf("MarkNotificationRead: %v", err)
	}
	if err := js.DismissNotification(ctx, n.ID); err != nil {
		t.Fatalf("DismissNotification: %v", err)
	}

	again := &jobs.Notification{
		SessionID:     "sess-1",
		Severity:      jobs.SeverityActionRequired,
		Title:         "Issue #7 assigned",
		TeleportState: "inbox",
		OriginRef:     "github:issue/7",
	}
	inserted, err = js.InsertExternalNotificationOnce(ctx, again)
	if err != nil {
		t.Fatalf("InsertExternalNotificationOnce(after dismiss): %v", err)
	}
	if inserted {
		t.Fatalf("expected dismissed external row to remain deduped")
	}
	if again.ID != n.ID {
		t.Fatalf("expected existing dismissed row ID %q, got %q", n.ID, again.ID)
	}

	notifs, err := js.ListNotifications(ctx, "sess-1", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(notifs) != 0 {
		t.Fatalf("expected dismissed duplicate to stay hidden, got %d rows", len(notifs))
	}
	got, err := js.GetNotification(ctx, n.ID)
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got == nil || got.ReadAt == nil || got.OriginKind != "external" {
		t.Fatalf("expected read external notification to remain resolvable, got %+v", got)
	}
}

// TestJobStore_InsertNotification_DefaultsCreatedAt is a regression test for the
// zero-CreatedAt mis-sort bug. The inbox.PostJobNotification path inserts a
// notification WITHOUT setting CreatedAt (it leaves the zero value). Before the
// fix, InsertNotification persisted that zero time — and time.Time{}.UnixMilli()
// is a large NEGATIVE number, so under "ORDER BY created_at DESC" the row sorted
// as the OLDEST. The later success notification therefore hid behind the earlier
// info one and ListNotifications(limit=1) returned the wrong (older info) row, so
// the success-only toast never fired.
//
// We insert two notifications via the bug's exact path — CreatedAt left ZERO —
// an info first, then a success, with a tiny gap so the fix's time.Now() default
// yields distinct, increasing timestamps. With the fix, the success row is the
// newest and sorts first. If the fix is reverted (zero stored), both rows carry
// the same large-negative UnixMilli and the success row no longer sorts ahead of
// the info row, and the wall-clock-based DESC ordering is lost — failing the
// limit=1 assertion and the "stamped non-zero" assertion below.
func TestJobStore_InsertNotification_DefaultsCreatedAt(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	ctx := context.Background()

	// First: an info notification, CreatedAt deliberately left ZERO (mirrors
	// inbox.PostJobNotification, which does not set it).
	info := &jobs.Notification{
		SessionID:  "sess-1",
		Severity:   jobs.SeverityInfo,
		Title:      "Job started",
		OriginKind: "job",
	}
	if err := js.InsertNotification(ctx, info); err != nil {
		t.Fatalf("InsertNotification(info): %v", err)
	}

	// A small gap so the default time.Now() stamps strictly increasing values;
	// keeps the DESC ordering deterministic without relying on sub-millisecond
	// wall-clock resolution between two back-to-back inserts.
	time.Sleep(2 * time.Millisecond)

	// Second: the success notification, also with CreatedAt left ZERO.
	success := &jobs.Notification{
		SessionID:  "sess-1",
		Severity:   jobs.SeveritySuccess,
		Title:      "Job done",
		OriginKind: "job",
	}
	if err := js.InsertNotification(ctx, success); err != nil {
		t.Fatalf("InsertNotification(success): %v", err)
	}

	// limit=1 must return the most-recently-inserted (success) row. Under the bug
	// (zero CreatedAt persisted) the success row sorts as oldest/ties and this
	// returns the info row instead.
	top, err := js.ListNotifications(ctx, "sess-1", 1)
	if err != nil {
		t.Fatalf("ListNotifications(limit=1): %v", err)
	}
	if len(top) != 1 {
		t.Fatalf("expected 1 notification with limit=1, got %d", len(top))
	}
	if top[0].Title != "Job done" || top[0].Severity != jobs.SeveritySuccess {
		t.Fatalf("limit=1 returned wrong row: got severity=%q title=%q; want the success row (most-recently-inserted)",
			top[0].Severity, top[0].Title)
	}

	// Both rows must be present and ordered success-then-info, with non-zero,
	// strictly-decreasing timestamps. If InsertNotification stops defaulting
	// CreatedAt, the stored times are the zero value (UnixMilli is negative) and
	// this ordering/non-zero check fails.
	all, err := js.ListNotifications(ctx, "sess-1", 2)
	if err != nil {
		t.Fatalf("ListNotifications(limit=2): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 notifications, got %d", len(all))
	}
	if all[0].Title != "Job done" || all[1].Title != "Job started" {
		t.Fatalf("expected order [success, info], got [%q, %q]", all[0].Title, all[1].Title)
	}
	if all[0].CreatedAt.IsZero() || all[1].CreatedAt.IsZero() {
		t.Fatalf("expected non-zero CreatedAt on both rows (defaulted by InsertNotification), got success=%v info=%v",
			all[0].CreatedAt, all[1].CreatedAt)
	}
	if !all[0].CreatedAt.After(all[1].CreatedAt) {
		t.Fatalf("expected success CreatedAt (%v) strictly after info CreatedAt (%v)",
			all[0].CreatedAt, all[1].CreatedAt)
	}
}

// TestJobStore_DismissNotification proves dismiss flips dismissed_at and the
// dismissed row drops out of ListNotifications, while GetNotification still
// resolves it by id (teleport needs the row even after dismiss).
func TestJobStore_DismissNotification(t *testing.T) {
	db := openTestDB(t)
	js, err := jobs.NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}
	ctx := context.Background()

	n := &jobs.Notification{
		SessionID:     "sess-1",
		CreatedAt:     time.Now(),
		Severity:      jobs.SeveritySuccess,
		Title:         "Job done",
		TeleportState: "reviewing",
		OriginKind:    "job",
		OriginRef:     "job:abc123",
	}
	if err := js.InsertNotification(ctx, n); err != nil {
		t.Fatalf("InsertNotification: %v", err)
	}

	// Present before dismiss.
	notifs, err := js.ListNotifications(ctx, "sess-1", 10)
	if err != nil {
		t.Fatalf("ListNotifications: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification before dismiss, got %d", len(notifs))
	}

	if err := js.DismissNotification(ctx, n.ID); err != nil {
		t.Fatalf("DismissNotification: %v", err)
	}

	// Dropped from the list after dismiss.
	notifs, err = js.ListNotifications(ctx, "sess-1", 10)
	if err != nil {
		t.Fatalf("ListNotifications after dismiss: %v", err)
	}
	if len(notifs) != 0 {
		t.Fatalf("expected 0 notifications after dismiss, got %d", len(notifs))
	}

	// And out of the unread counts.
	counts, err := js.UnreadCount(ctx, "sess-1")
	if err != nil {
		t.Fatalf("UnreadCount after dismiss: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("expected 0 unread after dismiss, got %v", counts)
	}

	// GetNotification still resolves it by id (teleport path).
	got, err := js.GetNotification(ctx, n.ID)
	if err != nil {
		t.Fatalf("GetNotification: %v", err)
	}
	if got == nil || got.TeleportState != "reviewing" {
		t.Fatalf("expected dismissed row still resolvable with teleport_state, got %+v", got)
	}

	// Unknown id is (nil, nil), not an error.
	missing, err := js.GetNotification(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("GetNotification(unknown): %v", err)
	}
	if missing != nil {
		t.Fatalf("expected nil for unknown id, got %+v", missing)
	}
}
