package blocks

// Static sample fixtures used by `kitsoki ui preview` and by the
// package's golden tests. Keeping the fixtures in-package means a
// renderer change and its sample re-render land in the same diff.

// ChatFixture is the canonical preview content for the chat view: one
// of each block kind, in a plausible sequence.
type ChatFixture struct {
	Location string
	Room     string

	// SystemNotices printed first (initial-view stand-in).
	Welcome string

	// Turns alternates user turn → routing → agent turn.
	Turns []FixtureTurn

	// Inbox notifications interleaved after the second turn.
	Inbox []InboxNotification

	// Background-complete blocks appended after inbox.
	BackgroundCompletes []FixtureBackgroundComplete

	// Actions block: room's current actions.
	Actions []MenuAction

	// Footer lines.
	FooterLine1 string
	FooterLine2 string

	// Prompt mode for the trailing prompt line.
	PromptMode Mode
}

// FixtureTurn is one round-trip in the fixture: user input, the routing
// resolution that classified it, and the agent body that followed.
type FixtureTurn struct {
	UserInput string
	Resolved  Resolved
	AgentBody string
}

// FixtureBackgroundComplete is one background-completion notice.
type FixtureBackgroundComplete struct {
	Room    string
	Summary string
}

// DefaultChatFixture returns the representative fixture for the chat
// view. Keep this stable across renderer changes — golden tests pin
// its output.
func DefaultChatFixture() ChatFixture {
	return ChatFixture{
		Location: "proposing",
		Room:     "cypilot",
		Welcome:  "session resumed · turn 4 · state proposing",
		Turns: []FixtureTurn{
			{
				UserInput: "back to the proposal",
				Resolved: Resolved{
					Kind:   "nav",
					Intent: "back",
					Source: SourceDeterministic,
				},
				AgentBody: "Returned to the proposal review.",
			},
			{
				UserInput: "use the backup branch instead",
				Resolved: Resolved{
					Kind:       "in-room",
					Intent:     "pick_branch",
					Source:     SourceLLM,
					Confidence: 0.84,
					Detail:     `slots: {branch: "backup"}`,
				},
				AgentBody: "Switched the candidate branch to `backup`. The CI run is queued; I'll print a checkmark when it lands.",
			},
		},
		Inbox: []InboxNotification{
			{
				ID:       "n1",
				Title:    "CI run for PR #4821 finished — 3 failures",
				Severity: "action_required",
				Age:      "32s ago",
			},
		},
		BackgroundCompletes: []FixtureBackgroundComplete{
			{
				Room:    "review_pr",
				Summary: "merged PR #4811 (chore: deps bump)",
			},
		},
		Actions: []MenuAction{
			{Index: 1, Name: "open_review", Label: "Open review", Available: true},
			{Index: 2, Name: "request_changes", Label: "Request changes", Available: true},
			{Index: 3, Name: "approve", Label: "Approve", Available: false, GuardHint: "CI not yet green"},
		},
		FooterLine1: "proposing · cypilot · 2 queued · 1 unread",
		FooterLine2: "PR #4821 · CI: failing (3) · PLTFRM-90014",
		PromptMode:  ModeNormal,
	}
}

// WorldFixture is the dedicated /world view body shown by the preview.
func WorldFixture() []WorldNode {
	return []WorldNode{
		{Key: "session", Expanded: true, HasKids: true, Depth: 0},
		{Key: "id", Value: `"sess_42"`, HasKids: false, Depth: 1},
		{Key: "user", Expanded: true, HasKids: true, Depth: 1},
		{Key: "name", Value: `"brad"`, HasKids: false, Depth: 2, Selected: true},
		{Key: "role", Value: `"dev"`, HasKids: false, Depth: 2},
		{Key: "tickets [3]", Expanded: true, HasKids: true, Depth: 1},
		{Key: "[0]", Value: "PLTFRM-89912", HasKids: false, Depth: 2},
		{Key: "[1]", Value: "PLTFRM-90001", HasKids: false, Depth: 2},
		{Key: "[2]", Value: "PLTFRM-90014", HasKids: false, Depth: 2},
		{Key: "flags", HasKids: true, Depth: 0},
		{Key: "providers", HasKids: true, Depth: 0},
	}
}

// TraceFixture returns the sample routing trace for /trace.
func TraceFixture() []TraceEvent {
	return []TraceEvent{
		{Tier: "deterministic", Result: "miss", Detail: "no exact menu match for `use the backup branch instead`"},
		{Tier: "synonyms", Result: "miss", Detail: "0 synonym entries matched"},
		{Tier: "slot-parser", Result: "miss", Detail: "no parser bound to current state"},
		{Tier: "cache", Result: "miss", Detail: "cache key not present"},
		{Tier: "LLM", Result: "hit", Detail: "intent=pick_branch confidence=0.84 slots={branch:\"backup\"}"},
	}
}

// RoutingPhases is the canonical phase order used by RoutingStatus's
// live-updated preview frames.
func RoutingPhases() []RoutingPhase {
	return []RoutingPhase{
		PhaseDeterministic,
		PhaseSynonyms,
		PhaseSlotParser,
		PhaseCache,
		PhaseLLM,
	}
}
