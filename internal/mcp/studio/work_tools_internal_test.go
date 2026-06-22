package studio

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/jobs"
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
