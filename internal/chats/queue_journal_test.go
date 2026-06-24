package chats_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/chats"
	"kitsoki/internal/journal"
	"kitsoki/internal/store"
)

// TestDrive_JournalLifecycle verifies the four lifecycle write sites emit the
// matching typed journal entries: Enqueue → submitted, MarkDriveDone →
// completed, MarkDriveFailed → failed, MarkDriveDismissed → dismissed.
//
// This is the continue-mode read side's source of truth — resumed transcripts
// rely on these entries to render drive history that happened before restart.
func TestDrive_JournalLifecycle(t *testing.T) {
	ctx := context.Background()

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jw, err := journal.NewSQLiteWriter(s.DB())
	require.NoError(t, err)
	jr, err := journal.NewSQLiteReader(s.DB())
	require.NoError(t, err)

	chatStore, err := chats.NewStore(s.DB(), chats.WithJournalWriter(jw))
	require.NoError(t, err)

	ch, err := chatStore.Create(ctx, "test-app", "test-room", "scope-1", "title")
	require.NoError(t, err)

	const sid = "session-lifecycle-test"

	// Submitted.
	d, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          ch.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "test",
		Payload:         "hello",
		OriginSessionID: sid,
	})
	require.NoError(t, err)

	// Promote to dispatching so we can flip to done.
	_, err = chatStore.ClaimDrive(ctx, d.DriveID)
	require.NoError(t, err)

	// Done.
	require.NoError(t, chatStore.MarkDriveDone(ctx, d.DriveID, 42))

	// A second drive to exercise the failed path.
	d2, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          ch.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "test",
		Payload:         "fail-me",
		OriginSessionID: sid,
	})
	require.NoError(t, err)
	_, err = chatStore.ClaimDrive(ctx, d2.DriveID)
	require.NoError(t, err)
	require.NoError(t, chatStore.MarkDriveFailed(ctx, d2.DriveID, "boom"))

	// A third to exercise dismissal (stays pending; never claimed).
	d3, err := chatStore.Enqueue(ctx, chats.EnqueueOptions{
		ChatID:          ch.ID,
		Transport:       chats.DriveTransportStateMachine,
		Actor:           "test",
		Payload:         "dismiss-me",
		OriginSessionID: sid,
	})
	require.NoError(t, err)
	require.NoError(t, chatStore.MarkDriveDismissed(ctx, d3.DriveID))

	// Collect the typed entries for this session and assert the four kinds.
	gotKinds := map[string]int{}
	gotByDrive := map[string][]string{} // drive_id → list of kinds
	typedSeq, typedErr := jr.ReplayTyped(app.SessionID(sid))
	for e := range typedSeq {
		switch e.Kind {
		case journal.KindChatDriveSubmitted,
			journal.KindChatDriveCompleted,
			journal.KindChatDriveFailed,
			journal.KindChatDriveDismissed:
			gotKinds[e.Kind]++
			var body struct {
				DriveID string `json:"drive_id"`
			}
			if err := json.Unmarshal(e.Body, &body); err == nil {
				gotByDrive[body.DriveID] = append(gotByDrive[body.DriveID], e.Kind)
			}
		}
	}
	require.NoError(t, typedErr())

	require.Equal(t, 3, gotKinds[journal.KindChatDriveSubmitted],
		"expected 3 submitted entries (one per Enqueue)")
	require.Equal(t, 1, gotKinds[journal.KindChatDriveCompleted],
		"expected 1 completed entry")
	require.Equal(t, 1, gotKinds[journal.KindChatDriveFailed],
		"expected 1 failed entry")
	require.Equal(t, 1, gotKinds[journal.KindChatDriveDismissed],
		"expected 1 dismissed entry")

	// Per-drive sanity: each drive saw exactly its expected kind pair.
	require.Equal(t, []string{
		journal.KindChatDriveSubmitted,
		journal.KindChatDriveCompleted,
	}, gotByDrive[d.DriveID],
		"drive 1: submitted then completed")
	require.Equal(t, []string{
		journal.KindChatDriveSubmitted,
		journal.KindChatDriveFailed,
	}, gotByDrive[d2.DriveID],
		"drive 2: submitted then failed")
	require.Equal(t, []string{
		journal.KindChatDriveSubmitted,
		journal.KindChatDriveDismissed,
	}, gotByDrive[d3.DriveID],
		"drive 3: submitted then dismissed")
}
