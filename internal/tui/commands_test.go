package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/chats"
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
		"/chat show",
		"/intents",
		"/work [--all]",
		"/world",
		"/meta",
		"/quit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("/help missing %q in output\n---\n%s", want, body)
		}
	}
}

func TestChatScopeDisplayStripsSessionPrefix(t *testing.T) {
	t.Parallel()
	if got := chats.DisplayScopeKey("\x00session=session-1\x00mcp-smoke"); got != "mcp-smoke" {
		t.Fatalf("DisplayScopeKey session scoped = %q, want mcp-smoke", got)
	}
	if got := chats.DisplayScopeKey("plain-scope"); got != "plain-scope" {
		t.Fatalf("DisplayScopeKey plain = %q, want plain-scope", got)
	}
}

// TestActionsAutoToggle exercises /intents auto on|off.
func TestActionsAutoToggle(t *testing.T) {
	t.Parallel()
	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	body, next, _ := ActionsCommand{}.Run(m, []string{"auto", "on"})
	if !next.actionsAuto {
		t.Errorf("actionsAuto should be true after `auto on`")
	}
	if !strings.Contains(body, "intents auto on") {
		t.Errorf("expected confirmation line, got %q", body)
	}

	body, next, _ = ActionsCommand{}.Run(next, []string{"auto", "off"})
	if next.actionsAuto {
		t.Errorf("actionsAuto should be false after `auto off`")
	}
	if !strings.Contains(body, "intents auto off") {
		t.Errorf("expected confirmation line, got %q", body)
	}
}

// TestIdeasCommandAppends verifies /ideas appends a bullet line to the
// configured file, preserves prior content, and reports usage when given
// no text. It writes to a temp file (not the repo's ideas.md) via the
// ideasFilePath override, so it cannot run in parallel.
func TestIdeasCommandAppends(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ideas.md")
	if err := os.WriteFile(path, []byte("## Ideas\n- existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	orig := ideasPathOverride
	ideasPathOverride = path
	defer func() { ideasPathOverride = orig }()

	m := RootModel{}
	m.transcript = newTranscriptModel(80, 24)

	// Empty args → usage, no write.
	body, _, _ := IdeasCommand{}.Run(m, nil)
	if !strings.Contains(body, "usage") {
		t.Errorf("expected usage line for empty /ideas, got %q", body)
	}

	body, _, _ = IdeasCommand{}.Run(m, []string{"build", "a", "widget"})
	if !strings.Contains(body, "jotted to") {
		t.Errorf("expected confirmation line, got %q", body)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "## Ideas\n- existing\n- build a widget\n"; string(got) != want {
		t.Errorf("file contents = %q, want %q", got, want)
	}
}

// TestAppendIdeaLineMissingNewline confirms a bullet is placed on its own
// line even when the existing file lacks a trailing newline.
func TestAppendIdeaLineMissingNewline(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ideas.md")
	if err := os.WriteFile(path, []byte("- existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := appendIdeaLine(path, "new one"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "- existing\n- new one\n"; string(got) != want {
		t.Errorf("file contents = %q, want %q", got, want)
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

func TestInboxNotificationHintIncludesBodyAndOriginURL(t *testing.T) {
	t.Parallel()
	got := inboxNotificationHint(jobs.Notification{
		Body:      "Review requested by alice.\n\nhttps://github.com/acme/repo/pull/42",
		OriginURL: "https://github.com/acme/repo/pull/42",
	})
	if got != "Review requested by alice. - https://github.com/acme/repo/pull/42" {
		t.Fatalf("hint = %q", got)
	}

	got = inboxNotificationHint(jobs.Notification{
		OriginURL: "https://github.com/acme/repo/issues/7",
	})
	if got != "https://github.com/acme/repo/issues/7" {
		t.Fatalf("url-only hint = %q", got)
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
