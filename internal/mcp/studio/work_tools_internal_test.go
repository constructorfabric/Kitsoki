package studio

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/jobs"
	"kitsoki/internal/store"
)

func TestWorkItemsForNotificationsPreservesExternalContext(t *testing.T) {
	sh := &SessionHandle{
		Key:       "github-work",
		SID:       "sid-1",
		StoryPath: "stories/dev-story/app.yaml",
	}
	items := workItemsForNotifications(sh, "inbox", []InboxInspectItem{{
		ID:                 "notif-1",
		Severity:           jobs.SeverityActionRequired,
		Title:              "PR #42 needs review",
		Body:               "Review requested by alice.",
		TeleportState:      "inbox",
		TeleportSlots:      map[string]any{"pr_id": "42", "pr_title": "Add tests"},
		OriginKind:         "external",
		OriginRef:          "github:acme/repo/pr/42",
		OriginURL:          "https://github.com/acme/repo/pull/42",
		CreatedAtUnixMilli: 123,
	}}, false)

	require.Len(t, items, 1)
	got := items[0]
	assert.Equal(t, "Review requested by alice.", got.Body)
	assert.Equal(t, map[string]any{"pr_id": "42", "pr_title": "Add tests"}, got.TeleportSlots)
	assert.Equal(t, "external", got.OriginKind)
	assert.Equal(t, "github:acme/repo/pr/42", got.OriginRef)
	assert.Equal(t, "https://github.com/acme/repo/pull/42", got.OriginURL)
	assert.Equal(t, "session.teleport", got.Reacquire.Tool)
	assert.Equal(t, "notif-1", got.Reacquire.Args["notification_id"])
}

func TestWorkItemsForJobsUseMatchingNotificationTeleport(t *testing.T) {
	sh := &SessionHandle{Key: "async-work", SID: "sid-1"}
	items := workItemsForJobs(sh, "running", []JobInspectItem{
		{
			ID:     "job-awaiting",
			Kind:   "host.agent.task",
			Status: jobs.JobAwaitingInput,
			ClarificationSchema: &jobs.ClarificationSchema{
				Prompt: "Which environment should I use?",
				Fields: map[string]string{"answer": "string"},
			},
		},
		{ID: "job-failed", Kind: "host.agent.task", Status: jobs.JobFailed},
		{ID: "job-running", Kind: "host.agent.task", Status: jobs.JobRunning},
	}, []InboxInspectItem{
		{ID: "notif-awaiting-info", Severity: jobs.SeverityInfo, TeleportJobID: "job-awaiting", OriginKind: "job", OriginRef: "job:job-awaiting"},
		{ID: "notif-awaiting", Severity: jobs.SeverityActionRequired, Body: "Pick staging or prod.", TeleportJobID: "job-awaiting", OriginKind: "job", OriginRef: "job:job-awaiting"},
		{ID: "notif-failed", OriginKind: "job", OriginRef: "job:job-failed"},
	}, false)

	require.Len(t, items, 3)
	byJob := map[string]WorkItem{}
	for _, item := range items {
		byJob[item.JobID] = item
	}
	assert.Equal(t, "session.teleport", byJob["job-awaiting"].Reacquire.Tool)
	assert.Equal(t, "notif-awaiting", byJob["job-awaiting"].Reacquire.Args["notification_id"])
	assert.Equal(t, "Pick staging or prod.", byJob["job-awaiting"].Body)
	assert.Equal(t, "session.teleport", byJob["job-failed"].Reacquire.Tool)
	assert.Equal(t, "notif-failed", byJob["job-failed"].Reacquire.Args["notification_id"])
	assert.Equal(t, "session.inspect", byJob["job-running"].Reacquire.Tool)
	assert.Equal(t, "async-work", byJob["job-running"].Reacquire.Args["handle"])
}

func TestInspectJobsPreservesClarificationSchema(t *testing.T) {
	got := inspectJobs([]jobs.Job{{
		ID:     "job-awaiting",
		Kind:   "host.run",
		Status: jobs.JobAwaitingInput,
		ClarificationSchema: jobs.ClarificationSchema{
			Prompt: "Which environment should I use?",
			Fields: map[string]string{"answer": "string"},
		},
	}})

	require.Len(t, got, 1)
	require.NotNil(t, got[0].ClarificationSchema)
	assert.Equal(t, "Which environment should I use?", got[0].ClarificationSchema.Prompt)
	assert.Equal(t, map[string]string{"answer": "string"}, got[0].ClarificationSchema.Fields)
}

func TestWorkItemNeedsAttentionUsesInterventionSemantics(t *testing.T) {
	assert.True(t, workItemNeedsAttention(WorkItem{
		Kind:     "notification",
		Severity: jobs.SeverityActionRequired,
	}))
	assert.True(t, workItemNeedsAttention(WorkItem{
		Kind:   "job",
		Status: string(jobs.JobAwaitingInput),
	}))
	assert.True(t, workItemNeedsAttention(WorkItem{
		Kind:   "job",
		Status: string(jobs.JobFailed),
	}))

	assert.False(t, workItemNeedsAttention(WorkItem{
		Kind:     "notification",
		Severity: jobs.SeveritySuccess,
	}))
	assert.False(t, workItemNeedsAttention(WorkItem{
		Kind:   "job",
		Status: string(jobs.JobRunning),
	}))
}

func TestWorkPrioritiesKeepPassiveNotificationsBelowActiveWork(t *testing.T) {
	assert.Greater(t, notificationPriority(InboxInspectItem{Severity: jobs.SeverityActionRequired}),
		jobPriority(JobInspectItem{Status: jobs.JobAwaitingInput}))
	assert.Greater(t, jobPriority(JobInspectItem{Status: jobs.JobAwaitingInput}),
		jobPriority(JobInspectItem{Status: jobs.JobFailed}))
	assert.Greater(t, jobPriority(JobInspectItem{Status: jobs.JobRunning}),
		notificationPriority(InboxInspectItem{Severity: jobs.SeveritySuccess}))
	assert.Greater(t, 60,
		notificationPriority(InboxInspectItem{Severity: jobs.SeverityInfo}))
}

func TestPendingMiningProposalsFoldTraceDecisions(t *testing.T) {
	history := store.History{
		miningEvent(t, 1, time.Unix(10, 0), store.MiningProposalRaised, store.MiningProposalRaisedPayload{
			RecipeID:  "recipe-accepted",
			Kind:      "binding",
			Target:    "root-instance",
			Priority:  0.91,
			Rung:      1,
			DraftPath: ".artifacts/mining/recipe-accepted",
		}),
		miningEvent(t, 2, time.Unix(20, 0), store.MiningProposalRaised, store.MiningProposalRaisedPayload{
			RecipeID:  "recipe-pending",
			Kind:      "intent",
			Target:    "dev-story",
			Priority:  0.72,
			Rung:      2,
			DraftPath: ".artifacts/mining/recipe-pending",
		}),
		miningEvent(t, 3, time.Unix(30, 0), store.MiningProposalDecided, store.MiningProposalDecidedPayload{
			RecipeID:   "recipe-accepted",
			Verdict:    store.MiningVerdictAccept,
			By:         store.MiningByHuman,
			FlowsGreen: true,
		}),
	}

	got := pendingMiningProposals("mine-handle", history)
	require.Len(t, got, 1)
	assert.Equal(t, "recipe-pending", got[0].RecipeID)
	assert.Equal(t, "intent", got[0].Kind)
	assert.Equal(t, "dev-story", got[0].Target)
	assert.Equal(t, 0.72, got[0].Priority)
	assert.Equal(t, int64(2), got[0].RaisedTurn)
	assert.Equal(t, int64(20_000_000), got[0].RaisedAtUnixMicro)
	assert.Equal(t, "session.inspect", got[0].Reacquire.Tool)
	assert.Equal(t, "mine-handle", got[0].Reacquire.Args["handle"])
	assert.Equal(t, 10, got[0].Reacquire.Args["last_turns"])
}

func TestWorkItemsForMiningProposals(t *testing.T) {
	sh := &SessionHandle{
		Key:       "mine-handle",
		SID:       "sid-1",
		StoryPath: "stories/dev-story/app.yaml",
	}
	items := workItemsForMiningProposals(sh, "idle", []MiningProposalItem{{
		RecipeID:          "recipe-pending",
		Kind:              "intent",
		Target:            "dev-story",
		Rung:              2,
		DraftPath:         ".artifacts/mining/recipe-pending",
		RaisedAtUnixMicro: 123_000,
		Reacquire: WorkReacquire{
			Tool: "session.inspect",
			Args: map[string]any{"handle": "mine-handle", "last_turns": 10},
		},
	}})

	require.Len(t, items, 1)
	got := items[0]
	assert.Equal(t, "mining_proposal", got.Kind)
	assert.Equal(t, "awaiting_review", got.Status)
	assert.Equal(t, "recipe-pending", got.ProposalID)
	assert.Equal(t, "intent", got.ProposalKind)
	assert.Equal(t, "dev-story", got.ProposalTarget)
	assert.Equal(t, ".artifacts/mining/recipe-pending", got.DraftPath)
	assert.Equal(t, 2, got.Rung)
	assert.Equal(t, int64(123_000), got.UpdatedAtUnixMicro)
	assert.Equal(t, "session.inspect", got.Reacquire.Tool)
	assert.False(t, workItemNeedsAttention(got))
}

func miningEvent(t *testing.T, turn int64, ts time.Time, kind store.EventKind, payload any) store.Event {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	return store.Event{
		Turn:    app.TurnNumber(turn),
		Ts:      ts,
		Kind:    kind,
		Payload: raw,
	}
}

func TestTruncateBodyCapsAndMarks(t *testing.T) {
	// Under the limit: unchanged.
	assert.Equal(t, "hello", truncateBody("hello", 200))
	// Disabled (<=0): unchanged even when long.
	long := strings.Repeat("x", 500)
	assert.Equal(t, long, truncateBody(long, 0))
	assert.Equal(t, long, truncateBody(long, -1))
	// Over the limit: capped, marker appended, prefix preserved.
	got := truncateBody(strings.Repeat("a", 300), 200)
	assert.Contains(t, got, "(truncated)")
	assert.True(t, strings.HasPrefix(got, strings.Repeat("a", 200)))
}

func TestWorkBodyTruncationDefaultsAndOptOut(t *testing.T) {
	items := []WorkItem{{Body: strings.Repeat("b", 400)}}
	for i := range items {
		items[i].Body = truncateBody(items[i].Body, defaultWorkBodyLimit)
	}
	assert.Less(t, len([]rune(items[0].Body)), 400)
	assert.Contains(t, items[0].Body, "(truncated)")
}
