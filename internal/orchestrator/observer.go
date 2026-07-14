// Package orchestrator — session observer hook.
//
// SessionObserver is a push-channel from the orchestrator to interested
// consumers (the TUI today; potentially logging, audit, push-notification
// bridges, etc. tomorrow) for events that happen *outside* a foreground
// Turn — most notably terminal background-job completion driven by
// handleJobTerminal.
//
// The foreground Turn path already returns a *TurnOutcome to its caller,
// so the TUI sees those naturally.  The background path commits its
// synthetic turn to the event log but, before this hook, had no way to
// tell the TUI "you should re-render now": the TUI's transcript only
// re-renders on turnOutcomeMsg, which used to be fired exclusively by
// user-keystroke paths.  Result: $inbox badge ticked (polled), main
// transcript stayed frozen until the next keystroke.
//
// The hook is intentionally synchronous-but-async-friendly: the
// orchestrator calls OnBackgroundTurn *after* releasing the per-session
// lock, on the listener goroutine.  Observers must not block on
// orchestrator state — the canonical TUI implementation simply forwards
// the outcome to a *tea.Program via Send (which is itself non-blocking
// when the program has stopped).

package orchestrator

import (
	"kitsoki/internal/app"
)

// SessionObserver receives notifications about session-level events that
// originate outside the foreground Turn path.  Implementations must be
// safe to call from any goroutine and must NOT block on the orchestrator
// — the orchestrator invokes observers after releasing the per-session
// lock but still on the background listener goroutine.
type SessionObserver interface {
	// OnBackgroundTurn fires after handleJobTerminal has applied the
	// on_complete chain, committed the synthetic turn to the event log,
	// and posted the inbox notification.  outcome carries the post-
	// background state path, the rendered view text, the typed view payload
	// when the landed room has one, and the refreshed allowed-intents list so
	// a TUI consumer can re-render its main transcript without re-reading the
	// database.
	//
	// outcome.Mode is ModeTransitioned (or ModeCompleted if the new
	// state is terminal — though terminal-state on_complete is unusual).
	// outcome.Events is nil because the background path's events have
	// already been committed via the event store; replay will pick them
	// up there, not from the observer.
	OnBackgroundTurn(sid app.SessionID, outcome *TurnOutcome)
}

// RegisterObserver adds o to the orchestrator's observer set.  Safe to
// call from any goroutine; duplicate registrations are ignored.
//
// Observers are notified in registration order.  An observer that
// panics during a notification will not stop subsequent observers (the
// orchestrator recovers per-observer) — but the panic IS logged.
func (o *Orchestrator) RegisterObserver(obs SessionObserver) {
	if obs == nil {
		return
	}
	o.obsMu.Lock()
	defer o.obsMu.Unlock()
	for _, existing := range o.observers {
		if existing == obs {
			return
		}
	}
	o.observers = append(o.observers, obs)
}

// UnregisterObserver removes obs from the observer set.  No-op if obs
// was never registered.  Safe from any goroutine.
func (o *Orchestrator) UnregisterObserver(obs SessionObserver) {
	if obs == nil {
		return
	}
	o.obsMu.Lock()
	defer o.obsMu.Unlock()
	for i, existing := range o.observers {
		if existing == obs {
			o.observers = append(o.observers[:i], o.observers[i+1:]...)
			return
		}
	}
}

// notifyBackgroundTurn fans out outcome to every registered observer.
// Called by handleJobTerminal AFTER it releases the per-session lock so
// observer callbacks can re-enter the orchestrator (e.g. read journey
// state) without risking deadlock against the foreground Turn path.
//
// Panics inside an observer are recovered so a buggy listener cannot
// kill the per-session listener goroutine.
func (o *Orchestrator) notifyBackgroundTurn(sid app.SessionID, outcome *TurnOutcome) {
	if outcome == nil {
		return
	}
	o.obsMu.Lock()
	// Copy under the lock so observer callbacks can re-enter
	// RegisterObserver/UnregisterObserver without deadlock.
	obs := make([]SessionObserver, len(o.observers))
	copy(obs, o.observers)
	o.obsMu.Unlock()

	for _, ob := range obs {
		func(ob SessionObserver) {
			defer func() {
				if r := recover(); r != nil {
					o.logger.Error("orchestrator: SessionObserver panicked",
						"session", string(sid),
						"recover", r,
					)
				}
			}()
			ob.OnBackgroundTurn(sid, outcome)
		}(ob)
	}
}
