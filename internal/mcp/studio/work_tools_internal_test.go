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
