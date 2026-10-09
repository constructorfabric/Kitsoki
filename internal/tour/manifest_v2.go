package tour

import "fmt"

// Tour format v2 step kinds. gate is the default for click-through tours: the
// user performs the real action and the tour reacts (Shepherd advanceOn
// semantics). act is the consent-gated agentic tier. Mirrors the TS union in
// tools/runstatus/src/tour/types-v2.ts — keep both in lockstep.
const (
	StepKindHighlight = "highlight"
	StepKindGate      = "gate"
	StepKindAct       = "act"
	StepKindNavigate  = "navigate"
)

// Consent tiers for act steps. Mirrors types-v2.ts ActPolicy.
const (
	PolicyWatch   = "watch"
	PolicyConfirm = "confirm"
	PolicyAuto    = "auto"
)

// Whether the live page beneath the overlay may be clicked during a step.
// Mirrors types-v2.ts Interaction.
const (
	InteractionBlock = "block"
	InteractionAllow = "allow"
)

// AdvanceOn events a gate step reacts to. Mirrors types-v2.ts AdvanceEvent.
const (
	AdvanceEventClick  = "click"
	AdvanceEventInput  = "input"
	AdvanceEventRoute  = "route"
	AdvanceEventSubmit = "submit"
)

// Act kinds an act step performs. Mirrors types-v2.ts ActKind.
const (
	ActClick  = "click"
	ActFill   = "fill"
	ActScroll = "scroll"
	ActPress  = "press"
)

// TargetWaitFor bounds how long anchor resolution waits for the target to
// become resolvable before a step's beforeShowPromise gives up.
type TargetWaitFor struct {
	TimeoutMs int `yaml:"timeoutMs,omitempty" json:"timeoutMs,omitempty"`
}

// TargetBundle is a ranked, multi-anchor bundle for one step's element,
// resolved in order: role+name -> testid -> label/text -> fuzzy css ->
// ancestor-scoped fallback. Never a single selector — see schemas/tour-v2
// .schema.json for the resolution-order contract and [HealEvent] for what
// fires when the primary anchor misses. A nil *TargetBundle is a valid,
// anchorless (centered) step, matching v1's optional Target/TargetText.
type TargetBundle struct {
	Role     string         `yaml:"role,omitempty" json:"role,omitempty"`
	Name     string         `yaml:"name,omitempty" json:"name,omitempty"`
	TestID   string         `yaml:"testid,omitempty" json:"testid,omitempty"`
	Text     string         `yaml:"text,omitempty" json:"text,omitempty"`
	CSS      string         `yaml:"css,omitempty" json:"css,omitempty"`
	Ancestor string         `yaml:"ancestor,omitempty" json:"ancestor,omitempty"`
	Frame    []string       `yaml:"frame,omitempty" json:"frame,omitempty"`
	WaitFor  *TargetWaitFor `yaml:"waitFor,omitempty" json:"waitFor,omitempty"`
}

// IsEmpty reports whether the bundle carries no anchor at all (every field
// unset), which callers treat the same as a nil *TargetBundle.
func (t *TargetBundle) IsEmpty() bool {
	if t == nil {
		return true
	}
	return t.Role == "" && t.Name == "" && t.TestID == "" && t.Text == "" &&
		t.CSS == "" && t.Ancestor == "" && len(t.Frame) == 0
}

// PopoverV2 is the narration surface for a step: title/body copy plus the
// placement relative to the resolved target.
type PopoverV2 struct {
	Title string `yaml:"title,omitempty" json:"title,omitempty"`
	Body  string `yaml:"body,omitempty" json:"body,omitempty"`
	Side  string `yaml:"side,omitempty" json:"side,omitempty"`
	Align string `yaml:"align,omitempty" json:"align,omitempty"`
}

// AdvanceOn declares which real user event advances a gate step.
type AdvanceOn struct {
	Event string `yaml:"event,omitempty" json:"event,omitempty"`
}

// ActSpec is the deterministic action an act step performs against its
// resolved target.
type ActSpec struct {
	Kind  string `yaml:"kind,omitempty" json:"kind,omitempty"`
	Value string `yaml:"value,omitempty" json:"value,omitempty"`
}

// TourStepV2 is one step of a v2 tour. See schemas/tour-v2.schema.json and
// tools/runstatus/src/tour/types-v2.ts for the field-for-field mirrors —
// keep all three in lockstep.
type TourStepV2 struct {
	ID          string                 `yaml:"id" json:"id"`
	Route       string                 `yaml:"route,omitempty" json:"route,omitempty"`
	Target      *TargetBundle          `yaml:"target,omitempty" json:"target,omitempty"`
	Popover     *PopoverV2             `yaml:"popover,omitempty" json:"popover,omitempty"`
	Kind        string                 `yaml:"kind" json:"kind"`
	AdvanceOn   *AdvanceOn             `yaml:"advanceOn,omitempty" json:"advanceOn,omitempty"`
	Act         *ActSpec               `yaml:"act,omitempty" json:"act,omitempty"`
	Policy      string                 `yaml:"policy,omitempty" json:"policy,omitempty"`
	Interaction string                 `yaml:"interaction,omitempty" json:"interaction,omitempty"`
	Viewport    string                 `yaml:"viewport,omitempty" json:"viewport,omitempty"`
	Data        map[string]any         `yaml:"data,omitempty" json:"data,omitempty"`
}

// Validate checks the step carries the fields its Kind requires, applying
// the same defaults callers (player, tour_replay) must apply: act steps
// default to PolicyConfirm when Policy is unset.
func (s *TourStepV2) Validate() error {
	if s.ID == "" {
		return fmt.Errorf("step has no id")
	}
	switch s.Kind {
	case StepKindHighlight:
		// no required fields beyond popover/target, both optional
	case StepKindGate:
		if s.AdvanceOn == nil || s.AdvanceOn.Event == "" {
			return fmt.Errorf("step %q kind %q requires advanceOn.event", s.ID, s.Kind)
		}
	case StepKindAct:
		// Either a first-class ActSpec, or the kitsoki-specific v1-drive
		// passthrough the v1 converter emits (an ordered DriveAction list
		// doesn't fit ActSpec's single kind/value — see convert_v2.go).
		if (s.Act == nil || s.Act.Kind == "") && s.Data["drive"] == nil {
			return fmt.Errorf("step %q kind %q requires act.kind or data.drive", s.ID, s.Kind)
		}
		switch s.Policy {
		case "", PolicyConfirm, PolicyWatch, PolicyAuto:
		default:
			return fmt.Errorf("step %q has unknown policy %q", s.ID, s.Policy)
		}
	case StepKindNavigate:
		if s.Route == "" {
			return fmt.Errorf("step %q kind %q requires route", s.ID, s.Kind)
		}
	case "":
		return fmt.Errorf("step %q has no kind", s.ID)
	default:
		return fmt.Errorf("step %q has unknown kind %q", s.ID, s.Kind)
	}
	if s.Interaction != "" && s.Interaction != InteractionBlock && s.Interaction != InteractionAllow {
		return fmt.Errorf("step %q has unknown interaction %q", s.ID, s.Interaction)
	}
	return nil
}

// EffectivePolicy returns Policy, defaulting act steps to PolicyConfirm when
// unset (schemas/tour-v2.schema.json's declared default). Non-act steps
// return Policy verbatim (usually empty).
func (s *TourStepV2) EffectivePolicy() string {
	if s.Kind == StepKindAct && s.Policy == "" {
		return PolicyConfirm
	}
	return s.Policy
}

// TourManifestV2 is a v2 tour: an app-agnostic, portable id, an optional
// exact-match origin binding, and its ordered steps.
type TourManifestV2 struct {
	Version  int          `yaml:"version" json:"version"`
	ID       string       `yaml:"id" json:"id"`
	Origin   string       `yaml:"origin,omitempty" json:"origin,omitempty"`
	Steps    []TourStepV2 `yaml:"steps" json:"steps"`
	SpecPath string       `yaml:"-" json:"-"`
}

// Validate checks the manifest version, id, and every step, rejecting
// duplicate step ids the same way v1's TourManifest.validate does.
func (m *TourManifestV2) Validate() error {
	if m.Version != 2 {
		return fmt.Errorf("tour manifest has version %d, want 2", m.Version)
	}
	if m.ID == "" {
		return fmt.Errorf("tour manifest has no id")
	}
	if len(m.Steps) == 0 {
		return fmt.Errorf("tour manifest %q has no steps", m.ID)
	}
	seen := map[string]bool{}
	for i := range m.Steps {
		s := &m.Steps[i]
		if err := s.Validate(); err != nil {
			return fmt.Errorf("tour %q: %w", m.ID, err)
		}
		if seen[s.ID] {
			return fmt.Errorf("tour %q: duplicate step id %q", m.ID, s.ID)
		}
		seen[s.ID] = true
	}
	return nil
}

// HealEvent is emitted by anchor resolution (the player, or tour_replay)
// whenever a step's primary anchor misses and a secondary anchor in its
// [TargetBundle] uniquely resolves. A heal is always audited — never a
// silent rebind — so it always carries which anchor failed, which matched,
// and the resolver's confidence in that match.
type HealEvent struct {
	StepID        string  `yaml:"stepId" json:"stepId"`
	FailedAnchor  string  `yaml:"failedAnchor" json:"failedAnchor"`
	MatchedAnchor string  `yaml:"matchedAnchor" json:"matchedAnchor"`
	Confidence    float64 `yaml:"confidence" json:"confidence"`
}
