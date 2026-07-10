package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/chats"
	"kitsoki/internal/host"
	"kitsoki/internal/jobs"
	studio "kitsoki/internal/mcp/studio"
	"kitsoki/internal/store"
)

type studioFakeRunner struct {
	responses map[string]studioFakeResp
}

type studioFakeResp struct {
	stdout string
	stderr string
	code   int
	err    error
}

func (f *studioFakeRunner) run(ctx context.Context, dir, name string, args ...string) (string, string, int, error) {
	key := name + " " + strings.Join(args, " ")
	if r, ok := f.responses[key]; ok {
		return r.stdout, r.stderr, r.code, r.err
	}
	return "", "unexpected command: " + key, 1, nil
}

func TestStudioWorkAggregatesAsyncReacquisitionAcrossHandles(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	appPath := writeBackgroundJobStory(t)

	_, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"key":        "cloak",
		"trace":      t.TempDir() + "/cloak.jsonl",
	})
	require.NoError(t, err)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": appPath,
		"harness":    "replay",
		"key":        "async-work",
		"trace":      t.TempDir() + "/async.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new async: %s", contentText(res))

	res, err = callTool(ctx, cs, "session.submit", map[string]any{
		"handle": "async-work",
		"intent": "enter",
	})
	require.NoError(t, err)
	require.True(t, driveResult(t, res).OK)

	var work studio.WorkResult
	require.Eventually(t, func() bool {
		res, err := callTool(ctx, cs, "studio.work", nil)
		if err != nil || res.IsError {
			return false
		}
		if err := json.Unmarshal([]byte(contentText(res)), &work); err != nil {
			return false
		}
		return work.Summary.NotificationsUnread == 2 && len(work.Items) == 2
	}, 3*time.Second, 25*time.Millisecond)

	assert.True(t, work.OK)
	assert.Equal(t, 2, work.Summary.Sessions)
	assert.Equal(t, 1, work.Summary.JobsTerminal)
	assert.Equal(t, 2, work.Summary.NotificationsUnread)
	assert.Equal(t, 0, work.Summary.NeedsAttention)
	require.Len(t, work.Sessions, 2)
	sessionsByHandle := map[string]studio.WorkSessionSummary{}
	for _, session := range work.Sessions {
		sessionsByHandle[session.Handle] = session
	}
	require.Contains(t, sessionsByHandle, "cloak")
	require.Contains(t, sessionsByHandle, "async-work")
	assert.Equal(t, 1, sessionsByHandle["async-work"].Async.JobsTerminal)

	require.NotEmpty(t, work.Items)
	top := work.Items[0]
	assert.Equal(t, "notification", top.Kind)
	assert.Equal(t, "async-work", top.Handle)
	assert.Equal(t, jobs.SeveritySuccess, top.Severity)
	assert.Equal(t, "session.teleport", top.Reacquire.Tool)
	assert.Equal(t, "async-work", top.Reacquire.Args["handle"])
	assert.Equal(t, top.NotificationID, top.Reacquire.Args["notification_id"])

	res, err = callTool(ctx, cs, "session.teleport", map[string]any{
		"handle":          top.Handle,
		"notification_id": top.NotificationID,
	})
	require.NoError(t, err)
	require.True(t, driveResult(t, res).OK)

	res, err = callTool(ctx, cs, "studio.work", nil)
	require.NoError(t, err)
	require.False(t, res.IsError, "studio.work after teleport: %s", contentText(res))
	var after studio.WorkResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &after))
	assert.Equal(t, 1, after.Summary.NotificationsUnread)
	assert.Len(t, after.Items, 1, "read notification drops out of the active queue")
}

func TestGitHubInboxSyncFeedsStudioWork(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"key":        "github-sync",
		"trace":      t.TempDir() + "/github-sync.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))

	restoreAPI := stubStudioGitHubInboxAPI(t, "10")
	defer restoreAPI()

	res, err = callTool(ctx, cs, "inbox.sync_github", map[string]any{
		"handle": "github-sync",
		"repo":   "acme/repo",
		"limit":  10,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "inbox.sync_github: %s", contentText(res))
	var synced studio.GitHubInboxSyncResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &synced))
	assert.Equal(t, 2, synced.Fetched)
	assert.Equal(t, 2, synced.Inserted)
	assert.Equal(t, 0, synced.Skipped)

	res, err = callTool(ctx, cs, "studio.work", nil)
	require.NoError(t, err)
	require.False(t, res.IsError, "studio.work: %s", contentText(res))
	var work studio.WorkResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &work))
	assert.Equal(t, 2, work.Summary.NotificationsUnread)
	assert.Equal(t, 2, work.Summary.NotificationsActionRequired)
	assert.Equal(t, 2, work.Summary.NeedsAttention)
	require.Len(t, work.Items, 2)
	var prItem *studio.WorkItem
	for i := range work.Items {
		if work.Items[i].OriginRef == "github:acme/repo/pr/42" {
			prItem = &work.Items[i]
			break
		}
	}
	require.NotNil(t, prItem, "studio.work should include the review-requested PR")
	assert.Equal(t, "https://github.com/acme/repo/pull/42", prItem.OriginURL)
	assert.Equal(t, map[string]any{"pr_author": "alice", "pr_id": "42", "pr_title": "Review this"}, prItem.TeleportSlots)

	res, err = callTool(ctx, cs, "studio.work", map[string]any{"limit": 1})
	require.NoError(t, err)
	require.False(t, res.IsError, "studio.work limited: %s", contentText(res))
	var limited studio.WorkResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &limited))
	assert.Equal(t, 2, limited.Summary.Items)
	assert.Equal(t, 2, limited.Summary.NeedsAttention)
	assert.Equal(t, 2, limited.Summary.NotificationsUnread)
	assert.Equal(t, 2, limited.Summary.NotificationsActionRequired)
	require.Len(t, limited.Items, 1, "limit only pages returned rows; summary stays global")

	res, err = callTool(ctx, cs, "inbox.sync_github", map[string]any{
		"handle": "github-sync",
		"repo":   "acme/repo",
		"limit":  10,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "second inbox.sync_github: %s", contentText(res))
	var second studio.GitHubInboxSyncResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &second))
	assert.Equal(t, 2, second.Fetched)
	assert.Equal(t, 0, second.Inserted)
	assert.Equal(t, 2, second.Skipped)
	assert.Empty(t, second.Items, "skipped rows are not echoed by default (token diet)")

	// include_skipped opts back into the full echo.
	res, err = callTool(ctx, cs, "inbox.sync_github", map[string]any{
		"handle":          "github-sync",
		"repo":            "acme/repo",
		"limit":           10,
		"include_skipped": true,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	var withSkipped studio.GitHubInboxSyncResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &withSkipped))
	assert.Equal(t, 2, withSkipped.Skipped)
	assert.Len(t, withSkipped.Items, 2, "include_skipped echoes already-present rows")
	for _, it := range withSkipped.Items {
		assert.False(t, it.Inserted)
	}
}

func stubStudioGitHubInboxAPI(t *testing.T, wantLimit string) func() {
	t.Helper()
	t.Setenv("GH_TOKEN", "test-token")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/search/issues", r.URL.Path)
		assert.Equal(t, wantLimit, r.URL.Query().Get("per_page"))
		q := r.URL.Query().Get("q")
		switch {
		case strings.Contains(q, "is:issue"):
			writeStudioJSON(t, w, map[string]any{"items": []map[string]any{{
				"number":    7,
				"title":     "Assigned issue",
				"html_url":  "https://github.com/acme/repo/issues/7",
				"assignees": []map[string]any{{"login": "brad"}},
			}}})
		case strings.Contains(q, "is:pr"):
			writeStudioJSON(t, w, map[string]any{"items": []map[string]any{{
				"number":   42,
				"title":    "Review this",
				"html_url": "https://github.com/acme/repo/pull/42",
				"user":     map[string]any{"login": "alice"},
			}}})
		default:
			t.Fatalf("unexpected search query: %q", q)
		}
	}))
	restore := host.SetGitHubAPIForTest(srv.URL, srv.Client())
	return func() {
		restore()
		srv.Close()
	}
}

func writeStudioJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(w).Encode(v))
}

// TestChatShowLastNPaginatesTranscript verifies chat.show returns only the last
// N transcript rows by default (token diet) and that last_n/offset page through
// the history, with last_n<=0 returning everything.
func TestChatShowLastNPaginatesTranscript(t *testing.T) {
	ctx := context.Background()
	backing, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backing.Close() })
	chatStore, err := chats.NewStore(backing.DB())
	require.NoError(t, err)
	srv, _ := newReplayServerWithChatStore(t, chatStore)
	cs := connectInProcess(ctx, t, srv)

	chat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-msgs", "with messages")
	require.NoError(t, err)
	for i := 0; i < 12; i++ {
		_, err := chatStore.AppendMessage(ctx, chat.ID, "user", fmt.Sprintf("msg-%02d", i), nil)
		require.NoError(t, err)
	}

	// Default: only the last defaultChatShowLastN (5) rows.
	res, err := callTool(ctx, cs, "chat.show", map[string]any{"chat_id": chat.ID})
	require.NoError(t, err)
	require.False(t, res.IsError, contentText(res))
	var def studio.ChatShowResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &def))
	require.Len(t, def.Messages, 5, "default last_n window")
	assert.Equal(t, "msg-07", def.Messages[0].Content)
	assert.Equal(t, "msg-11", def.Messages[4].Content)

	// last_n=3.
	res, err = callTool(ctx, cs, "chat.show", map[string]any{"chat_id": chat.ID, "last_n": 3})
	require.NoError(t, err)
	var three studio.ChatShowResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &three))
	require.Len(t, three.Messages, 3)
	assert.Equal(t, "msg-11", three.Messages[2].Content)

	// offset=2 with last_n=3 pages backwards from the tail.
	res, err = callTool(ctx, cs, "chat.show", map[string]any{"chat_id": chat.ID, "last_n": 3, "offset": 2})
	require.NoError(t, err)
	var paged studio.ChatShowResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &paged))
	require.Len(t, paged.Messages, 3)
	assert.Equal(t, "msg-09", paged.Messages[2].Content, "offset skips the 2 newest rows")

	// last_n=0 returns the full transcript.
	res, err = callTool(ctx, cs, "chat.show", map[string]any{"chat_id": chat.ID, "last_n": 0})
	require.NoError(t, err)
	var all studio.ChatShowResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &all))
	require.Len(t, all.Messages, 12, "last_n<=0 returns everything")
}

func TestStudioWorkShowsRunningJobs(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	appPath := writeSlowBackgroundJobStory(t)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": appPath,
		"harness":    "replay",
		"key":        "slow-work",
		"trace":      t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))

	res, err = callTool(ctx, cs, "session.submit", map[string]any{
		"handle": "slow-work",
		"intent": "enter",
	})
	require.NoError(t, err)
	require.True(t, driveResult(t, res).OK)

	var work studio.WorkResult
	require.Eventually(t, func() bool {
		res, err := callTool(ctx, cs, "studio.work", nil)
		if err != nil || res.IsError {
			return false
		}
		if err := json.Unmarshal([]byte(contentText(res)), &work); err != nil {
			return false
		}
		if work.Summary.JobsRunning != 1 {
			return false
		}
		for _, item := range work.Items {
			if item.Kind == "job" && item.Status == "running" {
				return true
			}
		}
		return false
	}, time.Second, 10*time.Millisecond)

	assert.Equal(t, 1, work.Summary.Sessions)
	assert.Equal(t, 1, work.Summary.JobsRunning)
	var runningJob studio.WorkItem
	for _, item := range work.Items {
		if item.Kind == "job" && item.Status == "running" {
			runningJob = item
			break
		}
	}
	require.NotEmpty(t, runningJob.JobID)
	assert.Equal(t, "slow-work", runningJob.Handle)
	assert.Equal(t, "session.teleport", runningJob.Reacquire.Tool)
	assert.Equal(t, "slow-work", runningJob.Reacquire.Args["handle"])
	assert.NotEmpty(t, runningJob.Reacquire.Args["notification_id"])
}

func TestStudioWorkChatReacquireCarriesSessionContext(t *testing.T) {
	ctx := context.Background()
	backing, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = backing.Close() })
	chatStore, err := chats.NewStore(backing.DB())
	require.NoError(t, err)
	srv, sess := newReplayServerWithChatStore(t, chatStore)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"key":        "chat-work",
		"trace":      t.TempDir() + "/chat-work.jsonl",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new: %s", contentText(res))
	sh, err := sess.ResolveSession("chat-work")
	require.NoError(t, err)

	queuedChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-queued", "queued review")
	require.NoError(t, err)
	queuedDrive, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          queuedChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "review queued async work",
		OriginSessionID: string(sh.SID),
		OriginState:     "foyer",
	})
	require.NoError(t, err)

	failedChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-failed", "failed review")
	require.NoError(t, err)
	failedDrive, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          failedChat.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "story",
		Payload:         "failed async work",
		OriginSessionID: string(sh.SID),
		OriginState:     "foyer",
	})
	require.NoError(t, err)
	_, err = chatStore.ClaimDrive(ctx, failedDrive.DriveID)
	require.NoError(t, err)
	require.NoError(t, chatStore.MarkDriveFailed(ctx, failedDrive.DriveID, "claude exited 1"))

	backgroundChat, err := chatStore.Create(ctx, "cloak", "agent-room", "scope-bg", "backgrounded review")
	require.NoError(t, err)
	_, err = backing.DB().ExecContext(ctx,
		`UPDATE chats SET session_id = ? WHERE id = ?`,
		string(sh.SID), backgroundChat.ID)
	require.NoError(t, err)
	_, err = chatStore.AttachPTY(ctx, chats.AttachPTYOptions{
		ChatID:      backgroundChat.ID,
		TmuxSession: "kitsoki-chat-work",
	})
	require.NoError(t, err)
	_, err = chatStore.DetachPTY(ctx, backgroundChat.ID)
	require.NoError(t, err)

	res, err = callTool(ctx, cs, "studio.work", nil)
	require.NoError(t, err)
	require.False(t, res.IsError, "studio.work: %s", contentText(res))
	var work studio.WorkResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &work))
	assert.Equal(t, 1, work.Summary.PendingDrives)
	assert.Equal(t, 1, work.Summary.FailedDrives)
	assert.Equal(t, 1, work.Summary.BackgroundedChats)
	assert.Equal(t, 1, work.Summary.NeedsAttention)

	byKind := map[string]studio.WorkItem{}
	for _, item := range work.Items {
		byKind[item.Kind] = item
	}
	pending := byKind["pending_drive"]
	require.NotEmpty(t, pending.ChatID)
	assert.Equal(t, queuedDrive.DriveID, pending.DriveID)
	assert.Equal(t, "chat.show", pending.Reacquire.Tool)
	assert.Equal(t, queuedChat.ID, pending.Reacquire.Args["chat_id"])
	assert.Equal(t, "chat-work", pending.Reacquire.Args["handle"])
	assert.Equal(t, string(sh.SID), pending.Reacquire.Args["session_id"])

	failed := byKind["failed_drive"]
	require.NotEmpty(t, failed.ChatID)
	assert.Equal(t, failedDrive.DriveID, failed.DriveID)
	assert.Equal(t, "claude exited 1", failed.Body)
	assert.Equal(t, "chat.show", failed.Reacquire.Tool)
	assert.Equal(t, failedChat.ID, failed.Reacquire.Args["chat_id"])
	assert.Equal(t, "chat-work", failed.Reacquire.Args["handle"])
	assert.Equal(t, string(sh.SID), failed.Reacquire.Args["session_id"])

	bg := byKind["backgrounded_chat"]
	require.NotEmpty(t, bg.ChatID)
	assert.Equal(t, backgroundChat.ID, bg.ChatID)
	assert.Equal(t, "chat.show", bg.Reacquire.Tool)
	assert.Equal(t, backgroundChat.ID, bg.Reacquire.Args["chat_id"])
	assert.Equal(t, "chat-work", bg.Reacquire.Args["handle"])
	assert.Equal(t, string(sh.SID), bg.Reacquire.Args["session_id"])

	res, err = callTool(ctx, cs, "chat.show", pending.Reacquire.Args)
	require.NoError(t, err)
	require.False(t, res.IsError, "chat.show: %s", contentText(res))
	var shown studio.ChatShowResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &shown))
	require.NotNil(t, shown.Context)
	assert.Equal(t, "chat-work", shown.Context.Handle)
	assert.Equal(t, string(sh.SID), shown.Context.SessionID)
	assert.Equal(t, queuedChat.ID, shown.Chat.ID)
	assert.Equal(t, "scope-queued", shown.Chat.DisplayScopeKey)
}
