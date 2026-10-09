package orchestrator

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/trace"
)

// normalizeInput trims whitespace and lowercases a string for matching.
func normalizeInput(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// deterministicLookup is a pre-built lookup table from normalized strings to
// MenuEntry indices (primary entries only). An entry of -1 means ambiguous
// (more than one entry maps to that string).
type deterministicLookup struct {
	// byDisplay maps normalized Display → primary-index (or -1 if ambiguous).
	byDisplay map[string]int
	// byExample maps normalized example string → primary-index (or -1 if ambiguous).
	byExample map[string]int
}

// buildDeterministicLookup builds the lookup tables for the given primary menu entries
// in the context of the current state.
// Rules:
//   - Display match: normalized(entry.Display) → entry index.
//   - Example match: normalized examples from the intent definition (via the app def),
//     but only when the example is unique across all primary entries, and only when
//     that intent's primary entry has no MissingSlots (fully determined). If an
//     example appears under multiple entries it is marked ambiguous (-1).
func buildDeterministicLookup(o *Orchestrator, state app.StatePath, primary []MenuEntry) deterministicLookup {
	byDisplay := make(map[string]int, len(primary))
	byExample := make(map[string]int)

	for idx, entry := range primary {
		key := normalizeInput(entry.Display)
		if _, exists := byDisplay[key]; exists {
			byDisplay[key] = -1 // ambiguous display
		} else {
			byDisplay[key] = idx
		}
	}

	// Build example lookup only for entries with no missing slots (fully determined).
	for idx, entry := range primary {
		if len(entry.MissingSlots) > 0 {
			continue // entry needs clarification; skip examples
		}

		intentDef, ok := lookupIntentByPath(o.def, state, entry.Intent)
		if !ok {
			continue
		}

		// Collect intent-level examples.
		for _, ex := range intentDef.Examples {
			normEx := normalizeInput(ex)
			if normEx == "" {
				continue
			}
			if existing, exists := byExample[normEx]; exists {
				if existing != idx {
					byExample[normEx] = -1 // ambiguous: multiple entries share this example
				}
			} else {
				byExample[normEx] = idx
			}
		}

		// Collect slot-level examples (only when the example uniquely identifies
		// the slot value encoded in this entry's PrefilledSlots).
		for slotName, slotDef := range intentDef.Slots {
			val, hasPrefill := entry.PrefilledSlots[slotName]
			if !hasPrefill {
				continue
			}
			for _, ex := range slotDef.Examples {
				normEx := normalizeInput(ex)
				if normEx == "" {
					continue
				}
				// Only treat as a match if the example matches the prefilled value.
				if normalizeInput(fmt.Sprintf("%v", val)) != normEx {
					continue
				}
				if existing, exists := byExample[normEx]; exists {
					if existing != idx {
						byExample[normEx] = -1
					}
				} else {
					byExample[normEx] = idx
				}
			}
		}
	}

	return deterministicLookup{byDisplay: byDisplay, byExample: byExample}
}

// MatchDeterministic checks whether the input matches a primary menu entry
// without dispatching the resulting transition. It is the cheap, side-effect
// free half of TryDeterministic, exported for a caller that wants to inspect
// a match before deciding whether to dispatch it (e.g. to run the dispatch
// on its own background goroutine). No in-repo caller does this today — the
// TUI used to run MatchDeterministic synchronously before Turn as its own
// pre-pass, but that duplicated Turn's internal deterministic tier and could
// in principle disagree with it, so it now calls Turn directly like every
// other surface (see tui.go's dispatchInput). Turn's own TryDeterministic
// call covers the same fast path with no duplication risk.
//
// Returns:
//   - (intent, slots, true, nil) — matched; caller should call SubmitDirect.
//   - ("", nil, false, nil)      — no match; caller should call Turn.
//   - ("", nil, false, err)      — error loading journey.
func (o *Orchestrator) MatchDeterministic(ctx context.Context, sid app.SessionID, input string) (string, map[string]any, bool, error) {
	journey, err := o.loadJourney(sid)
	if err != nil {
		return "", nil, false, fmt.Errorf("orchestrator: MatchDeterministic: load journey: %w", err)
	}

	menu := ComputeMenu(o.def, o.machine, journey.State, journey.World)
	if len(menu.Primary) == 0 {
		return "", nil, false, nil
	}

	lookup := buildDeterministicLookup(o, journey.State, menu.Primary)
	normInput := normalizeInput(input)

	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)

	if idx, ok := lookup.byDisplay[normInput]; ok && idx >= 0 {
		entry := menu.Primary[idx]
		tl.Debug(ctx, trace.EvTurnDeterministicHit,
			slog.String("match_type", "display"),
			slog.String("input", input),
			slog.String("display", entry.Display),
			slog.String("intent", entry.Intent),
		)
		slots := entry.PrefilledSlots
		if slots == nil {
			slots = make(map[string]any)
		}
		return entry.Intent, slots, true, nil
	}

	if idx, ok := lookup.byExample[normInput]; ok && idx >= 0 {
		entry := menu.Primary[idx]
		tl.Debug(ctx, trace.EvTurnDeterministicHit,
			slog.String("match_type", "example"),
			slog.String("input", input),
			slog.String("display", entry.Display),
			slog.String("intent", entry.Intent),
		)
		slots := entry.PrefilledSlots
		if slots == nil {
			slots = make(map[string]any)
		}
		return entry.Intent, slots, true, nil
	}

	tl.Debug(ctx, trace.EvTurnDeterministicMiss,
		slog.String("input", input),
		slog.String("state", string(journey.State)),
	)
	return "", nil, false, nil
}

// TryDeterministic attempts to route the input without calling the LLM.
// It recomputes the current menu and tries to match the input against:
//  1. Display strings of primary entries (exact match after normalization).
//  2. Intent-level or slot-level examples that uniquely identify a primary entry.
//
// Returns:
//   - (outcome, true, nil)   — matched; outcome is ready to use.
//   - (nil, false, nil)      — no deterministic match; caller should call Turn.
//   - (nil, false, err)      — error loading journey or running SubmitDirect.
func (o *Orchestrator) TryDeterministic(ctx context.Context, sid app.SessionID, input string) (*TurnOutcome, bool, error) {
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, false, fmt.Errorf("orchestrator: TryDeterministic: load journey: %w", err)
	}

	// Recompute current menu fresh each call.
	menu := ComputeMenu(o.def, o.machine, journey.State, journey.World)

	if len(menu.Primary) == 0 {
		return nil, false, nil
	}

	lookup := buildDeterministicLookup(o, journey.State, menu.Primary)
	normInput := normalizeInput(input)

	// Helper to emit trace events.
	turnNum := journey.Turn + 1
	tl := trace.NewTurnLogger(o.logger, sid, turnNum, journey.State)

	// 1. Try Display match first.
	if idx, ok := lookup.byDisplay[normInput]; ok && idx >= 0 {
		entry := menu.Primary[idx]
		tl.Debug(ctx, trace.EvTurnDeterministicHit,
			slog.String("match_type", "display"),
			slog.String("input", input),
			slog.String("display", entry.Display),
			slog.String("intent", entry.Intent),
		)
		slots := entry.PrefilledSlots
		if slots == nil {
			slots = make(map[string]any)
		}
		// Preserve the user's original text on the audit trail and record
		// the routing provenance — see RouteProvenance.
		outcome, err := o.SubmitDirectRouted(ctx, sid, entry.Intent, slots, input, RouteProvenance{
			Source:    "deterministic",
			MatchType: "display",
		})
		if err != nil {
			return nil, false, err
		}
		return outcome, true, nil
	}

	// 2. Try example match.
	if idx, ok := lookup.byExample[normInput]; ok && idx >= 0 {
		entry := menu.Primary[idx]
		tl.Debug(ctx, trace.EvTurnDeterministicHit,
			slog.String("match_type", "example"),
			slog.String("input", input),
			slog.String("display", entry.Display),
			slog.String("intent", entry.Intent),
		)
		slots := entry.PrefilledSlots
		if slots == nil {
			slots = make(map[string]any)
		}
		outcome, err := o.SubmitDirectRouted(ctx, sid, entry.Intent, slots, input, RouteProvenance{
			Source:    "deterministic",
			MatchType: "example",
		})
		if err != nil {
			return nil, false, err
		}
		return outcome, true, nil
	}

	// No match — fall through to LLM.
	tl.Debug(ctx, trace.EvTurnDeterministicMiss,
		slog.String("input", input),
		slog.String("state", string(journey.State)),
	)
	return nil, false, nil
}
