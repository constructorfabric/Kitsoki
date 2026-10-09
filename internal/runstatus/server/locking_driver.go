package server

import (
	"context"
	"fmt"

	"kitsoki/internal/orchestrator"
)

// LockFunc runs fn while holding a session-scoped exclusive lock, returning
// fn's error (or a lock-acquisition error such as store.ErrSessionBusy when the
// lock is held elsewhere — including by another process). It is the injection
// point that keeps this package free of a store import: the registry supplies a
// closure over store.WithWriterLock bound to the session id.
type LockFunc func(ctx context.Context, fn func() error) error

// lockingDriver wraps a [Driver] so state-advancing RPCs (Turn, SubmitDirect,
// ContinueTurn, DriveOperation) run under a session writer lock. This serialises
// a browser turn against a concurrent inbound-bridge turn on the same persisted
// session, and makes a turn that races another process's `kitsoki session
// continue` fail cleanly (the loser gets the lock error) rather than interleave
// two writers on one session.
//
// The read / no-advance methods (View, IntentInfo, AskOffPath, PatchWorld) are
// promoted from the embedded Driver unchanged: View only reads, and the lock is
// reserved for turns that mutate the journey.
type lockingDriver struct {
	Driver // embedded inner driver; promotes View/IntentInfo/AskOffPath/PatchWorld
	lock   LockFunc
}

// NewLockingDriver wraps inner so its advancing RPCs run under lock. Used by the
// web registry for sessions attached to the persisted store, so concurrent
// drivers (browser + inbound bridge, or a separate session-continue process)
// serialise on the session writer lock.
func NewLockingDriver(inner Driver, lock LockFunc) Driver {
	return &lockingDriver{Driver: inner, lock: lock}
}

func (d *lockingDriver) Turn(ctx context.Context, input string) (*orchestrator.TurnOutcome, error) {
	var out *orchestrator.TurnOutcome
	err := d.lock(ctx, func() error {
		var e error
		out, e = d.Driver.Turn(ctx, input)
		return e
	})
	return out, err
}

func (d *lockingDriver) SubmitDirect(ctx context.Context, intent string, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	var out *orchestrator.TurnOutcome
	err := d.lock(ctx, func() error {
		var e error
		out, e = d.Driver.SubmitDirect(ctx, intent, slots)
		return e
	})
	return out, err
}

func (d *lockingDriver) ContinueTurn(ctx context.Context, slots map[string]any) (*orchestrator.TurnOutcome, error) {
	var out *orchestrator.TurnOutcome
	err := d.lock(ctx, func() error {
		var e error
		out, e = d.Driver.ContinueTurn(ctx, slots)
		return e
	})
	return out, err
}

func (d *lockingDriver) DriveOperation(ctx context.Context) (*orchestrator.OperationDriveOutcome, error) {
	od, ok := d.Driver.(OperationDriver)
	if !ok {
		return nil, fmt.Errorf("session.drive_operation: operation driving unavailable")
	}
	var out *orchestrator.OperationDriveOutcome
	err := d.lock(ctx, func() error {
		var e error
		out, e = od.DriveOperation(ctx)
		return e
	})
	return out, err
}

// HarnessProfiles / HarnessSelection / SetHarnessSelection forward to the inner
// driver's HarnessController when it has one. Selection state is in-memory and
// per-session — not the journey log — so these run unlocked (the writer lock is
// reserved for turns that mutate session state). A read-only inner driver
// without the optional interface yields empty profiles and a no-op switch.
func (d *lockingDriver) HarnessProfiles() []orchestrator.ProfileInfo {
	if hc, ok := d.Driver.(HarnessController); ok {
		return hc.HarnessProfiles()
	}
	return nil
}

func (d *lockingDriver) HarnessSelection() orchestrator.ProfileSelection {
	if hc, ok := d.Driver.(HarnessController); ok {
		return hc.HarnessSelection()
	}
	return orchestrator.ProfileSelection{}
}

func (d *lockingDriver) SetHarnessSelection(profile, model, effort string) error {
	if hc, ok := d.Driver.(HarnessController); ok {
		return hc.SetHarnessSelection(profile, model, effort)
	}
	return nil
}

// Teleport drives a turn (it re-renders the destination room and restores the
// job's slots), so it mutates session state and must serialise under the same
// writer lock as the other turn-driving methods. The read-only inbox methods
// (ListNotifications/MarkNotificationRead/DismissNotification) are promoted from
// the embedded Driver unlocked — they touch only the JobStore, not the session.
func (d *lockingDriver) Teleport(ctx context.Context, notificationID string) (*orchestrator.TurnOutcome, error) {
	var out *orchestrator.TurnOutcome
	err := d.lock(ctx, func() error {
		var e error
		out, e = d.Driver.Teleport(ctx, notificationID)
		return e
	})
	return out, err
}

// ListWork is read-only current-state inspection, so it forwards unlocked like
// ListNotifications. Drivers without the optional WorkLister extension
// contribute an empty work set.
func (d *lockingDriver) ListWork(ctx context.Context) (SessionWork, error) {
	if wl, ok := d.Driver.(WorkLister); ok {
		return wl.ListWork(ctx)
	}
	return SessionWork{}, nil
}

// ShowChat is read-only current-state inspection, so it forwards unlocked like
// ListWork. Drivers without the optional ChatShower extension report the same
// no-store shape as a read-only surface.
func (d *lockingDriver) ShowChat(ctx context.Context, chatID string, sinceSeq int) (ChatShowResult, error) {
	if cs, ok := d.Driver.(ChatShower); ok {
		return cs.ShowChat(ctx, chatID, sinceSeq)
	}
	return ChatShowResult{}, fmt.Errorf("chat.show: no chat store configured")
}

// RewindRoute re-baselines the session via a snapshot and re-dispatches a turn,
// so it mutates session state and must serialise under the same writer lock as
// the other turn-driving methods (rather than being promoted unlocked).
func (d *lockingDriver) RewindRoute(ctx context.Context, decisionID string, newClass orchestrator.ContextRouteClass, reason string) (*orchestrator.TurnOutcome, error) {
	var out *orchestrator.TurnOutcome
	err := d.lock(ctx, func() error {
		var e error
		out, e = d.Driver.RewindRoute(ctx, decisionID, newClass, reason)
		return e
	})
	return out, err
}
