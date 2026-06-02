// trace_store.go — helpers for reconstructing a trace from stored events.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/store"
)

// openStoreForTrace opens the SQLite store at path.
func openStoreForTrace(path string) (store.Store, error) {
	return store.Open(path)
}

// printSessionEvents loads and pretty-prints all events for the given session.
func printSessionEvents(s store.Store, sessionID string, w io.Writer) error {
	sid := app.SessionID(sessionID)
	history, err := s.LoadHistory(sid)
	if err != nil {
		return fmt.Errorf("load history for session %q: %w", sessionID, err)
	}

	if len(history) == 0 {
		fmt.Fprintf(w, "No events found for session %q.\n", sessionID)
		return nil
	}

	var currentTurn app.TurnNumber
	for _, ev := range history {
		if ev.Turn != currentTurn {
			if currentTurn > 0 {
				fmt.Fprintln(w)
			}
			currentTurn = ev.Turn
			fmt.Fprintf(w, "[T%d] ── turn %d ────────────────────────────────\n", ev.Turn, ev.Turn)
		}

		// Pretty-print the event.
		var payload map[string]any
		if len(ev.Payload) > 0 {
			_ = json.Unmarshal(ev.Payload, &payload)
		}

		kvParts := make([]string, 0, len(payload))
		for k, v := range payload {
			kvParts = append(kvParts, fmt.Sprintf("%s=%v", k, v))
		}

		kv := strings.Join(kvParts, " ")
		fmt.Fprintf(w, "  %-26s  %s\n", string(ev.Kind), kv)
	}

	fmt.Fprintf(w, "\nTotal events: %d\n", len(history))
	return nil
}
