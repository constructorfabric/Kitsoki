package tour

// ConvertToV2 maps a legacy v1 [TourManifest] onto the v2 format so existing
// feature-catalog tours keep rendering while internal/tour, the browser MCP,
// and the player migrate to v2. The mapping is lossless: any v1 field with no
// first-class v2 slot (WaitForTarget, DwellMs, Drive, Trace) rides through in
// Data under its v1 key rather than being dropped, so a v2 consumer that
// doesn't understand a v1-only concept can still ignore it safely and a
// kitsoki-aware renderer (internal/tour itself, in v2-render mode) can still
// recover it.
//
// Kind mapping:
//   - v1 "explain"                        -> v2 "highlight"
//   - v1 "action", advance "click-target" -> v2 "gate", advanceOn.event "click"
//   - v1 "action", advance "route-match"  -> v2 "navigate", route AdvanceRoute
//   - any step carrying Drive[] actions   -> v2 "act" with Data["drive"] holding
//     the v1 DriveAction list verbatim (kitsoki-specific self-driving replay
//     data — see the package doc for why Drive exists); ActSpec is left nil
//     because v1's Drive is an ordered list, not ActSpec's single kind/value.
func ConvertToV2(m *TourManifest) *TourManifestV2 {
	out := &TourManifestV2{
		Version:  2,
		ID:       m.Export,
		Steps:    make([]TourStepV2, 0, len(m.Steps)),
		SpecPath: m.SpecPath,
	}
	for _, s := range m.Steps {
		out.Steps = append(out.Steps, convertStepToV2(s))
	}
	return out
}

func convertStepToV2(s TourStep) TourStepV2 {
	step := TourStepV2{
		ID:    s.ID,
		Route: s.Route,
	}

	if s.Target != "" || s.TargetText != "" {
		step.Target = &TargetBundle{TestID: s.Target, Text: s.TargetText}
	}
	if s.WaitForTarget != "" {
		if step.Target == nil {
			step.Target = &TargetBundle{}
		}
		setData(&step, "waitForTarget", s.WaitForTarget)
	}

	if s.Title != "" || s.Body != "" || s.Placement != "" {
		step.Popover = &PopoverV2{Title: s.Title, Body: s.Body, Side: v1PlacementToSide(s.Placement)}
	}

	switch {
	case len(s.Drive) > 0:
		step.Kind = StepKindAct
		setData(&step, "drive", s.Drive)
	case s.Advance == "route-match":
		step.Kind = StepKindNavigate
		if s.AdvanceRoute != "" {
			step.Route = s.AdvanceRoute
		}
	case s.Kind == "action":
		step.Kind = StepKindGate
		step.AdvanceOn = &AdvanceOn{Event: AdvanceEventClick}
	default:
		step.Kind = StepKindHighlight
	}

	if s.DwellMs > 0 {
		setData(&step, "dwellMs", s.DwellMs)
	}

	return step
}

// setData lazily allocates Data and assigns key, keeping the "no first-class
// v2 slot" v1 fields out of the struct while still round-tripping them.
func setData(step *TourStepV2, key string, value any) {
	if step.Data == nil {
		step.Data = map[string]any{}
	}
	step.Data[key] = value
}

// v1PlacementToSide maps v1's Placement vocabulary (top/bottom/left/right/
// center) onto v2 Popover.Side, which shares the same vocabulary — this is
// an identity map kept as a named function so a future divergence between
// the two vocabularies has one place to change.
func v1PlacementToSide(placement string) string {
	return placement
}
