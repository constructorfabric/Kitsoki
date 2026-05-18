package tui

import (
	"strings"
	"testing"

	"kitsoki/internal/jobs"
)

// TestHelpCommandLists confirms /help renders all four sections and
// includes a representative command from each. The exact list will
// drift as phases land more commands — these checks are about
// structural presence, not exact wording.
func TestHelpCommandLists(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)
	body, _, _ := HelpCommand{}.Run(m, nil)
	for _, want := range []string{
		"chat blocks",
		"dedicated views",
		"room switches",
		"system",
		"/help",
		"/actions",
		"/world",
		"/meta",
		"/quit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/help missing %q in output\n---\n%s", want, body)
		}
	}
}

// TestActionsAutoToggle exercises /actions auto on|off.
func TestActionsAutoToggle(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	body, next, _ := ActionsCommand{}.Run(m, []string{"auto", "on"})
	if !next.actionsAuto {
		t.Errorf("actionsAuto should be true after `auto on`")
	}
	if !strings.Contains(body, "actions auto on") {
		t.Errorf("expected confirmation line, got %q", body)
	}

	body, next, _ = ActionsCommand{}.Run(next, []string{"auto", "off"})
	if next.actionsAuto {
		t.Errorf("actionsAuto should be false after `auto off`")
	}
	if !strings.Contains(body, "actions auto off") {
		t.Errorf("expected confirmation line, got %q", body)
	}
}

// TestNewInboxNotificationsDetectsAdded verifies the differ used by
// the inbox-arrival transcript line picks up only genuinely new items.
func TestNewInboxNotificationsDetectsAdded(t *testing.T) {
	t.Parallel()
	prior := []jobs.Notification{
		{ID: "a", Title: "alpha"},
		{ID: "b", Title: "bravo"},
	}
	fresh := []jobs.Notification{
		{ID: "a", Title: "alpha"},
		{ID: "b", Title: "bravo"},
		{ID: "c", Title: "charlie"},
		{ID: "d", Title: "delta"},
	}
	got := newInboxNotifications(prior, fresh)
	if len(got) != 2 {
		t.Fatalf("expected 2 new notifications, got %d (%v)", len(got), got)
	}
	if got[0].ID != "c" || got[1].ID != "d" {
		t.Errorf("expected [c,d], got %v", []string{got[0].ID, got[1].ID})
	}
}

// TestRenderActionsBlockFromMenu sanity-checks that menu items
// populated on the model produce a non-empty actions block.
func TestRenderActionsBlockFromMenu(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)
	m.menu = newMenuModel(40, 24)
	out := renderActionsBlock(m)
	// Empty menu — should still emit a friendly empty notice.
	if !strings.Contains(out, "no actions") {
		t.Errorf("empty menu should render an empty notice, got:\n%s", out)
	}
}
