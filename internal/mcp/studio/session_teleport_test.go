package studio

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/jobs"
)

func TestSessionTeleport_ReacquiresInboxNotification(t *testing.T) {
	ctx := context.Background()
	sess := NewStudioSession(nil)
	srv := NewServer(sess)

	sh, err := sess.OpenDrivingSession(ctx, OpenDrivingSessionParams{
		Key:       "s1",
		Mode:      HarnessReplay,
		StoryPath: "../../../testdata/apps/cloak/app.yaml",
		TracePath: t.TempDir() + "/trace.jsonl",
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.CloseSession(sh.Key) })

	n := &jobs.Notification{
		SessionID:     sh.SID,
		Severity:      jobs.SeverityActionRequired,
		Title:         "Needs cloakroom attention",
		Body:          "Return to the cloakroom and decide what to do.",
		TeleportState: "cloakroom",
		TeleportSlots: map[string]any{"source": "test"},
		OriginKind:    "job",
		OriginRef:     "job:test",
		OriginURL:     "https://github.com/acme/repo/pull/42",
	}
	require.NoError(t, sh.Runtime.jobStore.InsertNotification(ctx, n))

	counts, err := sh.Runtime.jobStore.UnreadCount(ctx, sh.SID)
	require.NoError(t, err)
	assert.Equal(t, 1, counts[jobs.SeverityActionRequired])

	toolErr, out, err := srv.handleSessionTeleport(ctx, nil, SessionTeleportArgs{
		Handle:         sh.Key,
		NotificationID: n.ID,
		Cols:           100,
		Rows:           30,
	})
	require.NoError(t, err)
	require.Nil(t, toolErr)
	teleported, ok := out.(TurnResponse)
	require.True(t, ok)
	require.True(t, teleported.OK)
	assert.Equal(t, "cloakroom", teleported.Outcome.State)
	assert.Equal(t, "cloakroom", teleported.Frame.Metadata.State)
	assert.Contains(t, teleported.Frame.Text, "CLOAKROOM")

	counts, err = sh.Runtime.jobStore.UnreadCount(ctx, sh.SID)
	require.NoError(t, err)
	assert.Equal(t, 0, counts[jobs.SeverityActionRequired])

	inspected, err := sh.Runtime.inspect(ctx, 1, sh.Key)
	require.NoError(t, err)
	assert.Equal(t, "cloakroom", inspected.State)
	assert.Equal(t, 0, inspected.Async.NotificationsActionRequired)
	assert.Equal(t, 0, inspected.Async.NotificationsUnread)
	require.Len(t, inspected.Notifications, 1, "read notifications remain visible until dismissed")
	assert.Equal(t, n.ID, inspected.Notifications[0].ID)
	assert.Equal(t, n.OriginURL, inspected.Notifications[0].OriginURL)
}
