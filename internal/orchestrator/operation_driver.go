package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"kitsoki/internal/app"
	"kitsoki/internal/machine"
	"kitsoki/internal/world"
)

const OperationDriveMaxTurns = 16

type OperationDriveOutcome struct {
	Turns      int
	Final      *TurnOutcome
	StopReason string
	LastIntent string
}

func (o *Orchestrator) DriveOperation(ctx context.Context, sid app.SessionID) (*OperationDriveOutcome, error) {
	if o == nil {
		return nil, fmt.Errorf("orchestrator: DriveOperation: nil orchestrator")
	}
	outcome := &OperationDriveOutcome{}
	for outcome.Turns < OperationDriveMaxTurns {
		journey, err := o.loadJourney(sid)
		if err != nil {
			return nil, fmt.Errorf("orchestrator: DriveOperation: load journey: %w", err)
		}
		handle, ok := operationDriverRunHandle(journey.World)
		if !ok {
			outcome.StopReason = "no-operation"
			return outcome, nil
		}
		status := operationDriverString(handle, "status")
		if status == "" {
			status = "running"
		}
		if status != "running" {
			outcome.StopReason = "operation-" + status
			return outcome, nil
		}
		if mode := operationDriverString(handle, "mode"); !operationDriverModeDrivable(mode) {
			outcome.StopReason = "operation-not-autonomous"
			return outcome, nil
		}
		if st := lookupStateByPath(o.def, journey.State); st != nil && st.Terminal {
			outcome.StopReason = "terminal"
			return outcome, nil
		}

		intentName, slots, match, ok := o.nextOperationDriverIntent(journey.State, journey.World)
		if !ok {
			outcome.StopReason = "no-driver-intent"
			return outcome, nil
		}
		turn, err := o.SubmitDirectRouted(ctx, sid, intentName, slots, "", RouteProvenance{
			Source:    "operation_driver",
			MatchType: match,
		})
		if err != nil {
			return nil, fmt.Errorf("orchestrator: DriveOperation: submit %q: %w", intentName, err)
		}
		outcome.Turns++
		outcome.Final = turn
		outcome.LastIntent = intentName
		switch turn.Mode {
		case ModeClarify:
			outcome.StopReason = "clarify"
			return outcome, nil
		case ModeRejected:
			outcome.StopReason = "rejected"
			return outcome, nil
		case ModeOffPath:
			outcome.StopReason = "offpath"
			return outcome, nil
		case ModeCancelled:
			outcome.StopReason = "cancelled"
			return outcome, nil
		}
	}
	outcome.StopReason = "max-turns"
	return outcome, nil
}

// DriveBackgroundOperation drives the active operation only when the running
// handle explicitly opted into background execution. Callers use this after a
// normal operator turn so foreground/manual operations remain operator-driven.
func (o *Orchestrator) DriveBackgroundOperation(ctx context.Context, sid app.SessionID) (*OperationDriveOutcome, error) {
	if o == nil {
		return nil, fmt.Errorf("orchestrator: DriveBackgroundOperation: nil orchestrator")
	}
	journey, err := o.loadJourney(sid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: DriveBackgroundOperation: load journey: %w", err)
	}
	handle, ok := operationDriverRunHandle(journey.World)
	if !ok {
		return nil, nil
	}
	status := operationDriverString(handle, "status")
	if status == "" {
		status = "running"
	}
	if status != "running" || !operationDriverBool(handle, "run_in_background") {
		return nil, nil
	}
	return o.DriveOperation(ctx, sid)
}

func (o *Orchestrator) nextOperationDriverIntent(state app.StatePath, w world.World) (string, map[string]any, string, bool) {
	if o == nil || o.machine == nil {
		return "", nil, "", false
	}
	menu := o.machine.Menu(state, w)
	candidates := make([]machine.MenuEntry, 0, len(menu.Primary))
	for _, entry := range menu.Primary {
		if len(entry.MissingSlots) > 0 || operationDriverUnsafeIntent(entry.Intent) {
			continue
		}
		if entry.DestinationHint == "" || entry.DestinationHint == string(state) {
			continue
		}
		candidates = append(candidates, entry)
	}
	if len(candidates) == 0 {
		return "", nil, "", false
	}
	for _, preferred := range []string{"accept", "continue", "proceed", "next", "done"} {
		for _, entry := range candidates {
			if operationDriverIntentBase(entry.Intent) == preferred {
				return entry.Intent, cloneSlots(entry.PrefilledSlots), "preferred:" + preferred, true
			}
		}
	}
	if len(candidates) == 1 {
		entry := candidates[0]
		return entry.Intent, cloneSlots(entry.PrefilledSlots), "single", true
	}
	return "", nil, "", false
}

func operationDriverRunHandle(w world.World) (map[string]any, bool) {
	if w.Vars == nil {
		return nil, false
	}
	handle, ok := w.Vars[app.OperationRunWorldKey].(map[string]any)
	if !ok || len(handle) == 0 {
		return nil, false
	}
	return handle, true
}

func operationDriverString(handle map[string]any, key string) string {
	v, ok := handle[key]
	if !ok || v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

func operationDriverBool(handle map[string]any, key string) bool {
	v, ok := handle[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return strings.EqualFold(strings.TrimSpace(t), "true")
	default:
		return strings.EqualFold(strings.TrimSpace(fmt.Sprint(t)), "true")
	}
}

func operationDriverModeDrivable(mode string) bool {
	switch mode {
	case "", "autonomous", "supervised":
		return true
	default:
		return false
	}
}

func operationDriverIntentBase(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "__"); i >= 0 {
		name = name[i+2:]
	}
	if i := strings.LastIndexAny(name, "./"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(strings.ReplaceAll(name, "-", "_"))
}

func operationDriverUnsafeIntent(name string) bool {
	base := operationDriverIntentBase(name)
	switch base {
	case "quit", "cancel", "abort", "stop", "refine", "look":
		return true
	default:
		return strings.HasPrefix(base, "restart") || strings.HasPrefix(base, "jump")
	}
}

func cloneSlots(slots map[string]any) map[string]any {
	if len(slots) == 0 {
		return nil
	}
	out := make(map[string]any, len(slots))
	for k, v := range slots {
		out[k] = v
	}
	return out
}
