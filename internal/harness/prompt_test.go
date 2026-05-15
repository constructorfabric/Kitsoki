package harness_test

import (
	"strings"
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/harness"
	"kitsoki/internal/world"
)

// TestBuildDynamicSuffix_IncludesStateViewAndDescription locks in that the
// current state's description and user-facing view text reach the prompt.
// Without these, the router only sees generic intent descriptions and
// misroutes state-specific phrasing — see the "start a new task" → jira regression.
func TestBuildDynamicSuffix_IncludesStateViewAndDescription(t *testing.T) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "x", Title: "X"},
		States: map[string]*app.State{
			"main": {
				Description: "Main Room — your daily HQ.",
				View: app.LegacyView(strings.Join([]string{
					"Welcome to dev-story.",
					"",
					"  - Start a new task          (jira search)",
					"  - Continue existing task    (workspace manager)",
				}, "\n")),
			},
		},
	}

	in := harness.TurnInput{
		StatePath:      "main",
		AllowedIntents: []string{"go_jira", "go_workspace_manager"},
		World:          world.World{Vars: map[string]any{}},
	}

	out := harness.BuildDynamicSuffixForTest(def, in)

	if !strings.Contains(out, "Main Room — your daily HQ.") {
		t.Errorf("expected state description in prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "Start a new task") || !strings.Contains(out, "(jira search)") {
		t.Errorf("expected menu labels from the view in prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "Continue existing task") {
		t.Errorf("expected second menu label in prompt, got:\n%s", out)
	}
}

// TestBuildDynamicSuffix_RendersWorldTemplates verifies that world templates
// inside the view (e.g. {{ world.inbox_unread }}) are expanded so the LLM
// sees the same concrete labels the user sees, not raw template syntax.
func TestBuildDynamicSuffix_RendersWorldTemplates(t *testing.T) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "x"},
		States: map[string]*app.State{
			"main": {
				Description: "Main",
				View:        app.LegacyView("Inbox: {{ world.inbox_unread }} unread."),
			},
		},
	}

	in := harness.TurnInput{
		StatePath: "main",
		World:     world.World{Vars: map[string]any{"inbox_unread": 3}},
	}

	out := harness.BuildDynamicSuffixForTest(def, in)

	if !strings.Contains(out, "Inbox: 3 unread.") {
		t.Errorf("expected rendered world template in prompt, got:\n%s", out)
	}
	if strings.Contains(out, "{{") {
		t.Errorf("expected template syntax to be expanded, got:\n%s", out)
	}
}

// TestBuildDynamicSuffix_RendersRecentTurns asserts the RecentTurns slice
// shows up in the system prompt as a "Recent conversation" block — the
// surface the LLM should consult to resolve back-references like "what I
// just said". Both success and rejection rows are exercised.
func TestBuildDynamicSuffix_RendersRecentTurns(t *testing.T) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "x"},
		States: map[string]*app.State{
			"main": {Description: "Main", View: app.LegacyView("ok")},
		},
	}

	in := harness.TurnInput{
		StatePath: "main",
		World:     world.World{Vars: map[string]any{}},
		RecentTurns: []harness.TurnSummary{
			{Turn: 3, UserText: "go south", Intent: "go", State: "bar.dark"},
			{Turn: 4, UserText: "drink", Intent: "drink", State: "bar.dark", Rejected: true},
		},
	}

	out := harness.BuildDynamicSuffixForTest(def, in)

	if !strings.Contains(out, "Recent conversation") {
		t.Fatalf("expected 'Recent conversation' header in prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "turn 3") || !strings.Contains(out, "go south") {
		t.Errorf("expected turn 3 user text in prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "REJECTED") {
		t.Errorf("expected rejection marker in prompt, got:\n%s", out)
	}
	if !strings.Contains(out, "bar.dark") {
		t.Errorf("expected post-turn state in prompt, got:\n%s", out)
	}
}

// TestBuildDynamicSuffix_EmptyRecentTurnsOmitsBlock asserts the recent-
// conversation block is omitted entirely on turn 1 (RecentTurns empty)
// — we should not waste tokens on an empty heading.
func TestBuildDynamicSuffix_EmptyRecentTurnsOmitsBlock(t *testing.T) {
	def := &app.AppDef{
		App: app.AppMeta{ID: "x"},
		States: map[string]*app.State{
			"main": {Description: "Main", View: app.LegacyView("ok")},
		},
	}
	in := harness.TurnInput{
		StatePath: "main",
		World:     world.World{Vars: map[string]any{}},
	}
	out := harness.BuildDynamicSuffixForTest(def, in)
	if strings.Contains(out, "Recent conversation") {
		t.Errorf("expected no 'Recent conversation' block when RecentTurns is empty, got:\n%s", out)
	}
}

// TestBuildDynamicSuffix_UnknownStateIsSafe asserts the function does not
// panic or error when the state path is unknown — it just omits the
// description/view section.
func TestBuildDynamicSuffix_UnknownStateIsSafe(t *testing.T) {
	def := &app.AppDef{
		App:    app.AppMeta{ID: "x"},
		States: map[string]*app.State{},
	}
	in := harness.TurnInput{StatePath: "nonexistent"}
	out := harness.BuildDynamicSuffixForTest(def, in)
	if !strings.Contains(out, "Current state") {
		t.Errorf("expected 'Current state' header even for unknown state, got:\n%s", out)
	}
	if strings.Contains(out, "State description") {
		t.Errorf("should not emit State description for unknown state, got:\n%s", out)
	}
}
