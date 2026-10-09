package tour

import (
	"encoding/json"
	"fmt"
)

// ConvertV2ToV1 downconverts a v2 tour onto the legacy [TourManifest] shape
// so the existing chromedp renderer (runner.go, drive.go, steps.go — none of
// which know about v2) can render it unchanged. This is how "internal/tour
// renders v2" is implemented: convert once at load time, then run the
// mature v1 pipeline.
//
// Field coverage is necessarily lossy in one direction: v1's Target is
// ALWAYS resolved as `[data-testid="<target>"]` (tools/runstatus/src/tour/
// types.ts), so only a v2 TargetBundle's TestID maps onto it — role/text/css
// -only bundles downconvert to an anchorless (centered) v1 step. A v2 "act"
// step converts via its Data["drive"] passthrough when present (the exact
// inverse of ConvertToV2's v1->v2 mapping, so a round-tripped v1 tour
// renders identically); a step authored directly in v2 with an ActSpec but
// no drive passthrough only converts when its Act.Kind is "click" and its
// target carries a testid — every other ActKind has no v1 Drive equivalent
// (v1's DriveTypeAndSend is a chat-composer-specific "fill and send", not a
// generic form fill) and returns an error rather than fabricate behavior.
func ConvertV2ToV1(m *TourManifestV2) (*TourManifest, error) {
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("ConvertV2ToV1: %w", err)
	}
	out := &TourManifest{Export: m.ID, Steps: make([]TourStep, 0, len(m.Steps))}
	for _, s := range m.Steps {
		step, err := convertStepV2ToV1(s)
		if err != nil {
			return nil, fmt.Errorf("ConvertV2ToV1: step %q: %w", s.ID, err)
		}
		out.Steps = append(out.Steps, step)
	}
	return out, nil
}

func convertStepV2ToV1(s TourStepV2) (TourStep, error) {
	step := TourStep{ID: s.ID, Route: s.Route}

	if s.Target != nil {
		step.Target = s.Target.TestID
		step.TargetText = s.Target.Text
	}
	if s.Popover != nil {
		step.Title = s.Popover.Title
		step.Body = s.Popover.Body
		step.Placement = s.Popover.Side
	}

	switch s.Kind {
	case StepKindHighlight:
		step.Kind = "explain"
		step.Advance = "next"
	case StepKindGate:
		step.Kind = "action"
		if s.AdvanceOn != nil && s.AdvanceOn.Event == AdvanceEventRoute {
			step.Advance = "route-match"
			step.AdvanceRoute = s.Route
		} else {
			step.Advance = "click-target"
		}
	case StepKindNavigate:
		step.Kind = "action"
		step.Advance = "route-match"
		step.AdvanceRoute = s.Route
	case StepKindAct:
		step.Kind = "action"
		step.Advance = "click-target"
		drive, err := driveFromStep(s)
		if err != nil {
			return TourStep{}, err
		}
		step.Drive = drive
	default:
		return TourStep{}, fmt.Errorf("unconvertible v2 kind %q", s.Kind)
	}

	if v, ok := s.Data["dwellMs"]; ok {
		if ms, err := asInt(v); err == nil {
			step.DwellMs = ms
		}
	}
	if v, ok := s.Data["waitForTarget"]; ok {
		if wft, ok := v.(string); ok {
			step.WaitForTarget = wft
		}
	}

	return step, nil
}

// driveFromStep recovers the v1 Drive[] list for an "act" step: either the
// exact passthrough ConvertToV2 stashed under Data["drive"] (round-trip
// case — always JSON-roundtripped here since a step loaded from YAML/JSON
// carries Data["drive"] as []interface{}, not a concrete []DriveAction),
// or, for a step authored directly in v2, a single-item synthesis when
// Act.Kind is "click" and the target carries a testid.
func driveFromStep(s TourStepV2) ([]DriveAction, error) {
	if raw, ok := s.Data["drive"]; ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("re-marshal data.drive: %w", err)
		}
		var drive []DriveAction
		if err := json.Unmarshal(data, &drive); err != nil {
			return nil, fmt.Errorf("decode data.drive: %w", err)
		}
		return drive, nil
	}
	if s.Act != nil && s.Act.Kind == ActClick {
		if s.Target == nil || s.Target.TestID == "" {
			return nil, fmt.Errorf("act step with act.kind=click needs target.testid (v1 DriveClickSelector requires a selector)")
		}
		return []DriveAction{{Type: DriveClickSelector, Selector: fmt.Sprintf("[data-testid=%q]", s.Target.TestID)}}, nil
	}
	return nil, fmt.Errorf(
		"act step has no data.drive passthrough and act.kind %q has no v1 Drive equivalent (only click, via a testid target, converts)",
		actKindOrEmpty(s.Act),
	)
}

func actKindOrEmpty(a *ActSpec) string {
	if a == nil {
		return ""
	}
	return a.Kind
}

func asInt(v any) (int, error) {
	switch n := v.(type) {
	case int:
		return n, nil
	case int64:
		return int(n), nil
	case float64:
		return int(n), nil
	default:
		return 0, fmt.Errorf("not a number: %T", v)
	}
}
