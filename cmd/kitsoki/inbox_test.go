package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

func TestInboxSyncGitHub_InsertsAndDedupes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")

	stdout, err := runKitsoki(t, "session", "create",
		"--app", cloakAppFlag(),
		"--db", dbPath,
		"--key", "github:inbox",
	)
	require.NoError(t, err)
	var created map[string]any
	require.NoError(t, json.Unmarshal([]byte(stdout), &created))
	sid := app.SessionID(created["session_id"].(string))

	t.Setenv("GH_TOKEN", "test-token")
	t.Setenv("PATH", t.TempDir())
	var seenIssues, seenPRs bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/search/issues", r.URL.Path)
		require.Equal(t, "10", r.URL.Query().Get("per_page"))
		q := r.URL.Query().Get("q")
		switch {
		case strings.Contains(q, "is:issue"):
			seenIssues = true
			for _, want := range []string{"repo:acme/repo", "is:open", "assignee:@me"} {
				require.Contains(t, q, want)
			}
			writeInboxJSON(t, w, map[string]any{"items": []map[string]any{{
				"number":    7,
				"title":     "Assigned issue",
				"html_url":  "https://github.com/acme/repo/issues/7",
				"assignees": []map[string]any{{"login": "brad"}},
			}}})
		case strings.Contains(q, "is:pr"):
			seenPRs = true
			for _, want := range []string{"repo:acme/repo", "is:open", "review-requested:@me"} {
				require.Contains(t, q, want)
			}
			writeInboxJSON(t, w, map[string]any{"items": []map[string]any{{
				"number":   42,
				"title":    "Review this",
				"html_url": "https://github.com/acme/repo/pull/42",
				"user":     map[string]any{"login": "alice"},
			}}})
		default:
			t.Fatalf("unexpected search query: %q", q)
		}
	}))
	defer srv.Close()
	restoreAPI := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	defer restoreAPI()

	firstOut, err := runKitsoki(t, "inbox", "sync-github",
		"--db", dbPath,
		"--key", "github:inbox",
		"--repo", "acme/repo",
		"--limit", "10",
	)
	require.NoError(t, err)
	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(firstOut), &first))
	require.Equal(t, float64(2), first["fetched"])
	require.Equal(t, float64(2), first["inserted"])
	require.Equal(t, float64(0), first["skipped"])

	secondOut, err := runKitsoki(t, "inbox", "sync-github",
		"--db", dbPath,
		"--key", "github:inbox",
		"--repo", "acme/repo",
		"--limit", "10",
	)
	require.NoError(t, err)
	var second map[string]any
	require.NoError(t, json.Unmarshal([]byte(secondOut), &second))
	require.Equal(t, float64(2), second["fetched"])
	require.Equal(t, float64(0), second["inserted"])
	require.Equal(t, float64(2), second["skipped"])

	s, err := openSessionStore(dbPath)
	require.NoError(t, err)
	defer func() { _ = s.Close() }()
	js, err := jobs.NewJobStore(s.DB())
	require.NoError(t, err)
	notifs, err := js.ListNotifications(context.Background(), sid, 10)
	require.NoError(t, err)
	require.Len(t, notifs, 2)

	byRef := map[string]jobs.Notification{}
	for _, n := range notifs {
		byRef[n.OriginRef] = n
	}
	pr := byRef["github:acme/repo/pr/42"]
	require.Equal(t, jobs.SeverityActionRequired, pr.Severity)
	require.Equal(t, "inbox", pr.TeleportState)
	require.Equal(t, "42", pr.TeleportSlots["pr_id"])
	require.Equal(t, "alice", pr.TeleportSlots["pr_author"])
	require.Equal(t, "https://github.com/acme/repo/pull/42", pr.OriginURL)

	issue := byRef["github:acme/repo/issue/7"]
	require.Equal(t, "7", issue.TeleportSlots["ticket_id"])
	require.Equal(t, "https://github.com/acme/repo/issues/7", issue.OriginURL)
	require.True(t, seenIssues, "inbox sync should use native issue search")
	require.True(t, seenPRs, "inbox sync should use native PR search")
}

func writeInboxJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

func TestInboxSyncGitHub_RequiresTargetSession(t *testing.T) {
	_, err := runKitsoki(t, "inbox", "sync-github")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one of --key or --id")
}
