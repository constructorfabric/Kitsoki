// inspect.go — implements the `kitsoki inspect` subcommand: a read-only JSON
// snapshot of a stored session. See docs/guide/development/developer-guide.md §6.2.
//
// Like `tmux attach` but read-only and structured: take an app.yaml + a
// session id, replay its event log, render the current view, and dump
// everything an outside observer needs to know about the session right now.
//
// Does not lock the session; safe to run alongside `kitsoki run`.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/store"
)

// inspectOutput is the JSON shape printed by `kitsoki inspect`.
//
// Field stability: this is a debugging surface, not a programmatic API.
// Field names should be considered stable enough for shell scripts and
// AI consumers, but additions are expected over time.
type inspectOutput struct {
	SessionID      string         `json:"session_id"`
	AppID          string         `json:"app_id"`
	AppVersion     string         `json:"app_version"`
	Status         string         `json:"status"`
	StartedAt      time.Time      `json:"started_at"`
	LastTurn       int64          `json:"last_turn"`
	CurrentState   string         `json:"current_state"`
	World          map[string]any `json:"world"`
	AllowedIntents []string       `json:"allowed_intents"`
	LastViewBytes  int            `json:"last_view_bytes"`
	LastView       string         `json:"last_view"`
	LastTurns      []turnSummary  `json:"last_turns"`
}

// turnSummary collapses one turn's worth of events into a one-line record.
type turnSummary struct {
	Turn      int64    `json:"turn"`
	Input     string   `json:"input,omitempty"`
	Intent    string   `json:"intent,omitempty"`
	FromState string   `json:"from_state,omitempty"`
	ToState   string   `json:"to_state,omitempty"`
	Outcome   string   `json:"outcome,omitempty"`
	ErrorCode string   `json:"error_code,omitempty"`
	HostCalls []string `json:"host_calls,omitempty"`
}

func inspectCmd() *cobra.Command {
	var (
		sessionID          string
		dbPath             string
		lastTurns          int
		routingStats       bool
		unusedSynonyms     bool
		synonymSuggestions bool
		cachePath          string
	)

	cmd := &cobra.Command{
		Use:   "inspect <app.yaml>",
		Short: "Print a read-only JSON snapshot of a stored session or routing diagnostics",
		Long: `Print a JSON snapshot of a kitsoki session: current state, world,
allowed intents, last rendered view, and a tail of turn summaries.

Read-only — does not lock the session, so it is safe to run while
'kitsoki run' is driving the same session.

Routing diagnostics (semantic-routing proposal §7.6 / §7.7) read the
turncache + AppDef for the app and surface:

  --routing-stats        per-intent hit counts across all routing tiers
                         and the hottest cached signatures.
  --unused-synonyms      every declared synonym with zero recorded hits.
  --synonym-suggestions  copy-pasteable YAML for LLM-resolved phrasings
                         that the synonym layer didn't catch.

Examples:
  kitsoki inspect app.yaml --session-id <sid>
  kitsoki inspect app.yaml --session-id <sid> --last-turns 20 | jq .world
  kitsoki inspect app.yaml --routing-stats --cache-db /tmp/cache.sqlite
  kitsoki inspect app.yaml --unused-synonyms --cache-db /tmp/cache.sqlite
  kitsoki inspect app.yaml --synonym-suggestions --cache-db /tmp/cache.sqlite`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appPath := args[0]

			def, err := loadAppWithEnv(appPath)
			if err != nil {
				return err
			}

			// Routing-tier diagnostic surfaces. Each is mutually
			// exclusive with the session-snapshot path because the
			// data sources are disjoint (routing reads the cache; the
			// snapshot reads the session store).
			switch {
			case routingStats:
				return runRoutingStats(cmd, def, cachePath)
			case unusedSynonyms:
				return runUnusedSynonyms(cmd, def, cachePath)
			case synonymSuggestions:
				return runSynonymSuggestions(cmd, def, cachePath)
			}

			if sessionID == "" {
				return fmt.Errorf("--session-id is required (or pass --routing-stats / --unused-synonyms / --synonym-suggestions)")
			}

			if dbPath == "" {
				dbPath = defaultDBPath()
			}
			if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
				return fmt.Errorf("no session database at %s — pass --db <path>", dbPath)
			}

			s, err := store.Open(dbPath)
			if err != nil {
				return fmt.Errorf("open store: %w", err)
			}
			defer func() { _ = s.Close() }()

			out, err := buildInspectOutput(cmd.Context(), def, s, sessionID, lastTurns)
			if err != nil {
				return err
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		},
	}

	cmd.Flags().StringVar(&sessionID, "session-id", "", "session ID to inspect (required for the session snapshot)")
	cmd.Flags().StringVar(&dbPath, "db", "", "path to SQLite session database (default: $XDG_DATA_HOME/kitsoki/sessions.db)")
	cmd.Flags().IntVar(&lastTurns, "last-turns", 5, "number of recent turn summaries to include")
	cmd.Flags().BoolVar(&routingStats, "routing-stats", false, "print per-intent routing-tier statistics from the turncache")
	cmd.Flags().BoolVar(&unusedSynonyms, "unused-synonyms", false, "list every declared synonym with zero recorded hits")
	cmd.Flags().BoolVar(&synonymSuggestions, "synonym-suggestions", false, "print copy-pasteable YAML for cache-resolved phrasings missing a synonym")
	cmd.Flags().StringVar(&cachePath, "cache-db", "", "path to the routing turncache SQLite file (required for --routing-stats / --unused-synonyms / --synonym-suggestions)")

	return cmd
}

// buildInspectOutput is the testable core: reconstruct the journey from
// the store, render the current view, summarise the last N turns.
func buildInspectOutput(ctx context.Context, def *app.AppDef, s store.Store, sessionID string, lastTurns int) (*inspectOutput, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	sid := app.SessionID(sessionID)

	summary, err := lookupSession(ctx, s, def.App.ID, sessionID)
	if err != nil {
		return nil, err
	}

	history, err := s.LoadHistory(sid)
	if err != nil {
		return nil, fmt.Errorf("load history: %w", err)
	}

	// Reconstruct journey using the same path the orchestrator uses.
	initialState := app.StatePath("")
	if root, ok := def.Root.(string); ok {
		initialState = app.StatePath(root)
	}
	initialWorld := machine.WorldFromSchema(def.World)

	snap, hasSnap, err := s.LatestSnapshot(sid)
	if err != nil {
		return nil, fmt.Errorf("latest snapshot: %w", err)
	}
	startState := initialState
	startWorld := initialWorld
	if hasSnap {
		startState = snap.StatePath
		if err := json.Unmarshal(snap.WorldJSON, &startWorld.Vars); err != nil {
			return nil, fmt.Errorf("unmarshal snapshot world: %w", err)
		}
	}

	js, err := store.BuildJourney(def, startState, startWorld, history)
	if err != nil {
		return nil, fmt.Errorf("build journey: %w", err)
	}

	m, err := machine.New(def)
	if err != nil {
		return nil, fmt.Errorf("build machine: %w", err)
	}

	view, err := m.RenderState(js.State, js.World)
	if err != nil {
		// Render failure is not fatal for inspect: we still want to dump
		// state + world so the human can see what's there.
		view = fmt.Sprintf("<render error: %v>", err)
	}

	allowedIntents := m.AllowedIntents(js.State, js.World)
	allowed := make([]string, len(allowedIntents))
	for i, ai := range allowedIntents {
		allowed[i] = ai.Name
	}

	out := &inspectOutput{
		SessionID:      sessionID,
		AppID:          summary.AppID,
		AppVersion:     summary.AppVersion,
		Status:         summary.Status,
		StartedAt:      summary.StartedAt,
		LastTurn:       int64(js.Turn),
		CurrentState:   string(js.State),
		World:          js.World.Vars,
		AllowedIntents: allowed,
		LastViewBytes:  len(view),
		LastView:       view,
		LastTurns:      summariseTurns(history, lastTurns),
	}
	return out, nil
}

// lookupSession finds the SessionSummary for sessionID under appID.
// Returns ErrSessionNotFound if no matching row exists.
func lookupSession(ctx context.Context, s store.Store, appID, sessionID string) (store.SessionSummary, error) {
	sessions, err := s.ListSessions(ctx, appID, 0)
	if err != nil {
		return store.SessionSummary{}, fmt.Errorf("list sessions: %w", err)
	}
	for _, sess := range sessions {
		if string(sess.ID) == sessionID {
			return sess, nil
		}
	}
	return store.SessionSummary{}, fmt.Errorf("session %q not found for app %q", sessionID, appID)
}

// summariseTurns folds an event history into one summary per turn,
// returning the last n entries.
func summariseTurns(history store.History, n int) []turnSummary {
	if n <= 0 {
		return nil
	}
	byTurn := make(map[int64]*turnSummary)
	var order []int64

	for _, ev := range history {
		t := int64(ev.Turn)
		if t == 0 {
			continue
		}
		ts, ok := byTurn[t]
		if !ok {
			ts = &turnSummary{Turn: t}
			byTurn[t] = ts
			order = append(order, t)
		}

		var p map[string]any
		if len(ev.Payload) > 0 {
			_ = json.Unmarshal(ev.Payload, &p)
		}

		switch ev.Kind {
		case store.TurnStarted:
			if v, ok := p["input"].(string); ok {
				ts.Input = v
			}
		case store.TransitionApplied:
			if v, ok := p["intent"].(string); ok && ts.Intent == "" {
				ts.Intent = v
			}
			if v, ok := p["from"].(string); ok {
				ts.FromState = v
			}
			if v, ok := p["to"].(string); ok {
				ts.ToState = v
			}
		case store.TurnEnded:
			if v, ok := p["outcome"].(string); ok {
				ts.Outcome = v
			}
			if v, ok := p["code"].(string); ok {
				ts.ErrorCode = v
			}
		case store.HostInvoked:
			if v, ok := p["namespace"].(string); ok {
				ts.HostCalls = append(ts.HostCalls, v)
			}
		}
	}

	if len(order) <= n {
		out := make([]turnSummary, len(order))
		for i, t := range order {
			out[i] = *byTurn[t]
		}
		return out
	}
	tail := order[len(order)-n:]
	out := make([]turnSummary, len(tail))
	for i, t := range tail {
		out[i] = *byTurn[t]
	}
	return out
}
