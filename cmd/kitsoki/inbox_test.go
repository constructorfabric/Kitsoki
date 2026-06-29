package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
)

type inboxFakeRunner struct {
	responses map[string]inboxFakeResp
}

type inboxFakeResp struct {
	stdout string
	stderr string
	code   int
	err    error
}

func (f *inboxFakeRunner) run(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
	key := name + " " + strings.Join(args, " ")
	if r, ok := f.responses[key]; ok {
		return r.stdout, r.stderr, r.code, r.err
	}
	return "", "unexpected command: " + key, 1, nil
}

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

	fr := &inboxFakeRunner{responses: map[string]inboxFakeResp{
		"gh --version": {stdout: "gh version 2.x\n"},
		"gh issue list --repo acme/repo --state open --assignee @me --limit 10 --json number,title,assignees,url": {
			stdout: `[{"number":7,"title":"Assigned issue","url":"https://github.com/acme/repo/issues/7","assignees":[{"login":"brad"}]}]`,
		},
		"gh pr list --repo acme/repo --state open --search review-requested:@me --limit 10 --json number,title,author,url": {
			stdout: `[{"number":42,"title":"Review this","url":"https://github.com/acme/repo/pull/42","author":{"login":"alice"}}]`,
		},
	}}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

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
}

func TestInboxSyncGitHub_RequiresTargetSession(t *testing.T) {
	_, err := runKitsoki(t, "inbox", "sync-github")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exactly one of --key or --id")
}
