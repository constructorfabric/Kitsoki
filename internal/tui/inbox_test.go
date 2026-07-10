package tui_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	tuipkg "kitsoki/internal/tui"

	_ "modernc.org/sqlite"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// openInboxTestDB opens an in-memory SQLite database and applies the jobs schema.
func openInboxTestDB(t *testing.T) (*sql.DB, *jobs.JobStore) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	js, err := jobs.NewJobStore(db)
	require.NoError(t, err)
	return db, js
}

// buildInboxModel creates a standalone inboxModel (via exported test hook)
// fed with a slice of notifications, at a fixed width/height.
func buildInboxModel(t *testing.T, ns []jobs.Notification) tea.Model {
	t.Helper()
	m := tuipkg.NewInboxModelForTest(28, 14, ns)
	return m
}

// ─── TestInboxView_Hidden ─────────────────────────────────────────────────────

// TestInboxView_Hidden asserts that an inbox with zero notifications renders
// the "Inbox: empty" single-line collapsed form.
func TestInboxView_Hidden(t *testing.T) {
	m := buildInboxModel(t, nil)
	view := m.View()
	require.Contains(t, view, "Inbox: empty",
		"zero notifications should render 'Inbox: empty'")
}

// ─── TestInboxView_Compact ────────────────────────────────────────────────────

// TestInboxView_Compact asserts that three notifications of mixed severity are
// all rendered with the correct severity glyph and title.
func TestInboxView_Compact(t *testing.T) {
	sid := app.SessionID("test-session")
	ns := []jobs.Notification{
		{
			ID:        "n1",
			SessionID: sid,
			CreatedAt: time.Now().Add(-5 * time.Minute),
			Severity:  jobs.SeveritySuccess,
			Title:     "build_plan done",
		},
		{
			ID:        "n2",
			SessionID: sid,
			CreatedAt: time.Now().Add(-2 * time.Minute),
			Severity:  jobs.SeverityError,
			Title:     "deploy failed",
		},
		{
			ID:        "n3",
			SessionID: sid,
			CreatedAt: time.Now().Add(-1 * time.Minute),
			Severity:  jobs.SeverityActionRequired,
			Title:     "confirm deploy",
		},
	}
	m := buildInboxModel(t, ns)
	view := m.View()

	// All three titles should appear.
	require.Contains(t, view, "build_plan done", "success notification title")
	require.Contains(t, view, "deploy failed", "error notification title")
	require.Contains(t, view, "confirm deploy", "action_required notification title")

	// Severity glyphs.
	require.Contains(t, view, "✓", "success glyph")
	require.Contains(t, view, "✗", "error glyph")
	require.Contains(t, view, "⋯", "action_required glyph")
}

// ─── TestInboxView_BadgeSummary ───────────────────────────────────────────────

// TestInboxView_BadgeSummary asserts that the title row shows the correct
// unread / total counts.
func TestInboxView_BadgeSummary(t *testing.T) {
	now := time.Now()
	readAt := now.Add(-1 * time.Second)

	sid := app.SessionID("test-session")
	ns := []jobs.Notification{
		{ID: "n1", SessionID: sid, CreatedAt: now, Severity: jobs.SeverityInfo, Title: "a"},
		{ID: "n2", SessionID: sid, CreatedAt: now, Severity: jobs.SeverityInfo, Title: "b"},
		{ID: "n3", SessionID: sid, CreatedAt: now, Severity: jobs.SeveritySuccess, Title: "c", ReadAt: &readAt},
		{ID: "n4", SessionID: sid, CreatedAt: now, Severity: jobs.SeverityWarn, Title: "d", ReadAt: &readAt},
		{ID: "n5", SessionID: sid, CreatedAt: now, Severity: jobs.SeverityError, Title: "e", ReadAt: &readAt},
	}

	m := buildInboxModel(t, ns)
	view := m.View()

	// 2 unread of 5 total → "2 / 5"
	require.Contains(t, view, "2 / 5",
		"badge should show '2 / 5' for 2 unread of 5 total; view:\n%s", view)
}

// ─── TestInboxKey_Selection ───────────────────────────────────────────────────

// TestInboxKey_Selection asserts that pressing i2 selects the second item and
// emits an inboxItemSelected message.
func TestInboxKey_Selection(t *testing.T) {
	sid := app.SessionID("test-session")
	ns := []jobs.Notification{
		{ID: "n1", SessionID: sid, CreatedAt: time.Now(), Severity: jobs.SeverityInfo, Title: "first"},
		{ID: "n2", SessionID: sid, CreatedAt: time.Now(), Severity: jobs.SeverityWarn, Title: "second"},
		{ID: "n3", SessionID: sid, CreatedAt: time.Now(), Severity: jobs.SeverityError, Title: "third"},
	}

	m := buildInboxModel(t, ns)

	// Send i2 keypress.
	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("i2")})
	require.NotNil(t, cmd, "i2 should return a non-nil Cmd")

	// Execute the command — it should produce an inboxItemSelected.
	msg := cmd()
	selected, ok := tuipkg.ExtractInboxItemSelected(msg)
	require.True(t, ok, "cmd should produce inboxItemSelected; got %T", msg)
	require.Equal(t, "n2", selected.ID, "selected notification should be the second one")
	require.Equal(t, "second", selected.Title)

	_ = m
}

// ─── TestInboxKey_ActionRequiredBanner ───────────────────────────────────────

// TestInboxKey_ActionRequiredBanner asserts that when at least one unread
// action_required notification exists, View() includes the banner text.
func TestInboxKey_ActionRequiredBanner(t *testing.T) {
	sid := app.SessionID("test-session")
	ns := []jobs.Notification{
		{
			ID:            "n1",
			SessionID:     sid,
			CreatedAt:     time.Now(),
			Severity:      jobs.SeverityActionRequired,
			Title:         "deploy needs confirmation",
			TeleportState: "deploy_confirm",
		},
	}

	// We need to test the banner via the RootModel since the banner is
	// composed there from m.inbox.ActionRequiredBanner(). Instead, test the
	// inbox sub-model's ActionRequiredBanner() via the exported helper.
	banner := tuipkg.InboxActionRequiredBannerForTest(28, 14, ns)
	require.Contains(t, banner, "[enter] open",
		"banner should contain [enter] open")
	require.Contains(t, banner, "[esc] later",
		"banner should contain [esc] later")
	require.Contains(t, banner, "deploy needs confirmation",
		"banner should contain the notification title")
}

// ─── TestInboxPoll_LiveStore ──────────────────────────────────────────────────

// TestInboxPoll_LiveStore exercises the inboxRefreshed message path end-to-end
// using a real in-memory JobStore: insert a notification, feed an
// inboxRefreshed message to an inboxModel, and assert the notification appears
// in View().
func TestInboxPoll_LiveStore(t *testing.T) {
	_, js := openInboxTestDB(t)
	ctx := context.Background()
	sid := app.SessionID("live-test")

	err := js.InsertNotification(ctx, &jobs.Notification{
		SessionID:  sid,
		CreatedAt:  time.Now(),
		Severity:   jobs.SeveritySuccess,
		Title:      "live job done",
		OriginKind: "job",
		OriginRef:  "job:abc",
	})
	require.NoError(t, err)

	ns, err := js.ListNotifications(ctx, sid, 20)
	require.NoError(t, err)
	require.Len(t, ns, 1)

	m := buildInboxModel(t, nil) // start empty
	m, _ = m.Update(tuipkg.InboxRefreshedMsg(ns))

	view := m.View()
	require.Contains(t, view, "live job done", "notification title should appear after inboxRefreshed")
}

func TestInboxSlashSyncGitHubImportsNotifications(t *testing.T) {
	m, sid, js, _, _ := buildWorkTestModel(t)
	ctx := context.Background()
	apiCalls, restoreAPI := stubTUIGitHubInboxAPI(t, "100", true)
	defer restoreAPI()

	m = runTurnBlocking(t, m, "/inbox sync-github acme/repo")
	tx := extractTranscript(t, m)
	require.Contains(t, tx, "github sync: fetched 2, inserted 2, skipped 0")
	require.Contains(t, tx, "Issue #7 assigned: Assigned issue")
	require.Contains(t, tx, "https://github.com/acme/repo/issues/7")
	require.Contains(t, tx, "PR #42 needs review: Review this")
	require.Contains(t, tx, "https://github.com/acme/repo/pull/42")

	ns, err := js.ListNotifications(ctx, sid, 20)
	require.NoError(t, err)
	require.Len(t, ns, 2)
	byRef := map[string]jobs.Notification{}
	for _, n := range ns {
		byRef[n.OriginRef] = n
	}
	require.Equal(t, "https://github.com/acme/repo/issues/7", byRef["github:acme/repo/issue/7"].OriginURL)
	require.Equal(t, "https://github.com/acme/repo/pull/42", byRef["github:acme/repo/pr/42"].OriginURL)
	require.Equal(t, 2, *apiCalls)

	m = runTurnBlocking(t, m, "/inbox sync-github acme/repo")
	tx = extractTranscript(t, m)
	require.Contains(t, tx, "github sync: fetched 2, inserted 0, skipped 2")
	ns, err = js.ListNotifications(ctx, sid, 20)
	require.NoError(t, err)
	require.Len(t, ns, 2)
	require.Equal(t, 4, *apiCalls)
}

func stubTUIGitHubInboxAPI(t *testing.T, wantLimit string, includePR bool) (*int, func()) {
	t.Helper()
	t.Setenv("GH_TOKEN", "test-token")
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/search/issues", r.URL.Path)
		require.Equal(t, wantLimit, r.URL.Query().Get("per_page"))
		q := r.URL.Query().Get("q")
		switch {
		case strings.Contains(q, "is:issue"):
			writeTUIJSON(t, w, map[string]any{"items": []map[string]any{{
				"number":    7,
				"title":     "Assigned issue",
				"html_url":  "https://github.com/acme/repo/issues/7",
				"assignees": []map[string]any{{"login": "brad"}},
			}}})
		case strings.Contains(q, "is:pr"):
			items := []map[string]any{}
			if includePR {
				items = append(items, map[string]any{
					"number":   42,
					"title":    "Review this",
					"html_url": "https://github.com/acme/repo/pull/42",
					"user":     map[string]any{"login": "alice"},
				})
			}
			writeTUIJSON(t, w, map[string]any{"items": items})
		default:
			t.Fatalf("unexpected search query: %q", q)
		}
	}))
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	return &calls, func() {
		restore()
		srv.Close()
	}
}

func writeTUIJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

func TestInboxSlashIndexTeleportsToUnreadNotification(t *testing.T) {
	m, sid, js, _, _ := buildWorkTestModel(t)
	ctx := context.Background()
	readAt := time.Now().Add(-time.Minute)
	require.NoError(t, js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     sid,
		CreatedAt:     time.Now().Add(-2 * time.Minute),
		Severity:      jobs.SeverityInfo,
		Title:         "already handled",
		TeleportState: "foyer",
		ReadAt:        &readAt,
	}))
	require.NoError(t, js.InsertNotification(ctx, &jobs.Notification{
		SessionID:     sid,
		CreatedAt:     time.Now().Add(-time.Minute),
		Severity:      jobs.SeverityActionRequired,
		Title:         "check cloakroom",
		TeleportState: "cloakroom",
	}))
	ns, err := js.ListNotifications(ctx, sid, 20)
	require.NoError(t, err)
	m, _ = m.Update(tuipkg.InboxRefreshedMsg(ns))

	m = runTurnBlocking(t, m, "/inbox 1")
	tx := extractTranscript(t, m)
	require.Contains(t, tx, "CLOAKROOM")

	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	require.Equal(t, app.StatePath("cloakroom"), rm.CurrentStateForTest())

	after, err := js.ListNotifications(ctx, sid, 20)
	require.NoError(t, err)
	var opened jobs.Notification
	for _, n := range after {
		if n.Title == "check cloakroom" {
			opened = n
			break
		}
	}
	require.NotEmpty(t, opened.ID)
	require.NotNil(t, opened.ReadAt)
}

// ─── TestHumanizeDuration ─────────────────────────────────────────────────────

// TestHumanizeDuration validates the relative-time helper across several buckets.
func TestHumanizeDuration(t *testing.T) {
	type tc struct {
		d    time.Duration
		want string
	}
	cases := []tc{
		{30 * time.Second, "just now"},
		{90 * time.Second, "1 m ago"},
		{65 * time.Minute, "1 h ago"},
		{25 * time.Hour, "1 d ago"},
	}
	for _, c := range cases {
		got := tuipkg.HumanizeDurationForTest(c.d)
		require.Equal(t, c.want, got, "humanizeDuration(%v)", c.d)
	}
}

// ─── TestInboxBadge_NoUnread ──────────────────────────────────────────────────

// TestInboxBadge_NoUnread asserts that the badge string is empty when there are
// no unread notifications.
func TestInboxBadge_NoUnread(t *testing.T) {
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)

	// No notifications — badge should be absent from hints row.
	view := m.View()
	require.NotContains(t, view, "inbox:", "no badge when there are no notifications")

	_ = sid
}
