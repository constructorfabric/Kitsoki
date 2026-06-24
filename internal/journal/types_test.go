package journal_test

import (
	"testing"

	"kitsoki/internal/journal"
)

func TestKindConstants_RoundTrip(t *testing.T) {
	t.Parallel()

	patchKinds := []string{
		journal.KindWorldPatch,
		journal.KindStateTransition,
		journal.KindChatsAppend,
		journal.KindJobsUpdate,
	}
	for _, k := range patchKinds {
		if !journal.IsPatchKind(k) {
			t.Errorf("IsPatchKind(%q) = false, want true", k)
		}
		if journal.IsCheckpointKind(k) {
			t.Errorf("IsCheckpointKind(%q) = true, want false", k)
		}
		if journal.IsTypedKind(k) {
			t.Errorf("IsTypedKind(%q) = true, want false", k)
		}
	}

	checkpointKinds := []string{
		journal.KindWorldCheckpoint,
		journal.KindStateCheckpoint,
		journal.KindChatsCheckpoint,
		journal.KindJobsCheckpoint,
	}
	for _, k := range checkpointKinds {
		if journal.IsPatchKind(k) {
			t.Errorf("IsPatchKind(%q) = true, want false", k)
		}
		if !journal.IsCheckpointKind(k) {
			t.Errorf("IsCheckpointKind(%q) = false, want true", k)
		}
		if journal.IsTypedKind(k) {
			t.Errorf("IsTypedKind(%q) = true, want false", k)
		}
	}

	typedKinds := []string{
		journal.KindHostInvoked,
		journal.KindHostDispatched,
		journal.KindHostReturned,
		journal.KindClarifyRequested,
		journal.KindClarifyAnswered,
		journal.KindOffPathQuestion,
		journal.KindOffPathAnswer,
		journal.KindOffPathChatResolved,
		journal.KindTimeoutArmed,
		journal.KindTimeoutCancelled,
		journal.KindTimeoutFired,
		journal.KindInboxItemOpened,
		journal.KindInboxItemDismissed,
		journal.KindValidationRejected,
		journal.KindGuardRejected,
		journal.KindIDEContextCaptured,
	}
	for _, k := range typedKinds {
		if journal.IsPatchKind(k) {
			t.Errorf("IsPatchKind(%q) = true, want false", k)
		}
		if journal.IsCheckpointKind(k) {
			t.Errorf("IsCheckpointKind(%q) = true, want false", k)
		}
		if !journal.IsTypedKind(k) {
			t.Errorf("IsTypedKind(%q) = false, want true", k)
		}
	}
}

func TestPredicates_Mutual_Exclusion(t *testing.T) {
	t.Parallel()

	all := []string{
		journal.KindWorldPatch, journal.KindStateTransition,
		journal.KindChatsAppend, journal.KindJobsUpdate,
		journal.KindWorldCheckpoint, journal.KindStateCheckpoint,
		journal.KindChatsCheckpoint, journal.KindJobsCheckpoint,
		journal.KindHostInvoked, journal.KindClarifyRequested,
	}
	for _, k := range all {
		count := 0
		if journal.IsPatchKind(k) {
			count++
		}
		if journal.IsCheckpointKind(k) {
			count++
		}
		if journal.IsTypedKind(k) {
			count++
		}
		if count != 1 {
			t.Errorf("kind %q matches %d predicates, want exactly 1", k, count)
		}
	}
}
