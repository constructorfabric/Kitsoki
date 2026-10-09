package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/app"
)

func TestSlashCompletionFrameShowsFilteredMenu(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("slash-frame"), "../../testdata/apps/cloak/app.yaml", "")
	m.prompt.SetValue("/mo")

	frame := ComposeFrame(&m, 100, 30)

	if !strings.Contains(frame.Text, "> /mo") {
		t.Fatalf("frame missing prompt value:\n%s", frame.Text)
	}
	if !strings.Contains(frame.Text, "/model [<id|n>]") {
		t.Fatalf("frame missing filtered /model suggestion:\n%s", frame.Text)
	}
	if !strings.Contains(frame.Text, "(Tab)") {
		t.Fatalf("frame missing Tab affordance:\n%s", frame.Text)
	}
	if strings.Contains(frame.Text, "/help") {
		t.Fatalf("filtered frame unexpectedly includes /help:\n%s", frame.Text)
	}
}

func TestSlashCompletionTabAcceptsPrimarySuggestion(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("slash-tab"), "../../testdata/apps/cloak/app.yaml", "")
	m.prompt.SetValue("/wo")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next, ok := updated.(RootModel)
	if !ok {
		t.Fatalf("updated model = %T, want RootModel", updated)
	}
	if got := next.prompt.Value(); got != "/work" {
		t.Fatalf("prompt after tab = %q, want /work", got)
	}
}

func TestSlashCompletionDocsTrainedUserCanDiscoverCompleteAndExecute(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("slash-docs-user"), "../../testdata/apps/cloak/app.yaml", "")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	m = updated.(RootModel)
	frame := ComposeFrame(&m, 100, 30)
	if !strings.Contains(frame.Text, "/help") || !strings.Contains(frame.Text, "(Tab)") {
		t.Fatalf("typing / should show command menu with tab affordance:\n%s", frame.Text)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("he")})
	m = updated.(RootModel)
	frame = ComposeFrame(&m, 100, 30)
	if !strings.Contains(frame.Text, "/help") {
		t.Fatalf("typing /he should filter to /help:\n%s", frame.Text)
	}
	if strings.Contains(frame.Text, "/model") {
		t.Fatalf("typing /he should filter unrelated commands out:\n%s", frame.Text)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = updated.(RootModel)
	if got := m.prompt.Value(); got != "/help" {
		t.Fatalf("prompt after tab = %q, want /help", got)
	}

	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(RootModel)
	if content := m.transcript.AllContent(); !strings.Contains(content, "commands") || !strings.Contains(content, "/help") {
		t.Fatalf("enter after tab should execute /help:\n%s", content)
	}
}

type fixedSlashSuggester struct {
	specs []SlashCommandSpec
}

func (s fixedSlashSuggester) SuggestSlashCommands(RootModel, string) []SlashCommandSpec {
	return s.specs
}

func TestSlashCompletionUsesInjectedSuggester(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("slash-custom"), "../../testdata/apps/cloak/app.yaml", "",
		WithSlashSuggester(fixedSlashSuggester{specs: []SlashCommandSpec{
			{Name: "/zeta", Description: "custom first"},
			{Name: "/alpha", Description: "custom second"},
		}}),
	)
	m.prompt.SetValue("/")

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	next := updated.(RootModel)
	if got := next.prompt.Value(); got != "/zeta" {
		t.Fatalf("prompt after injected tab = %q, want /zeta", got)
	}

	frame := ComposeFrame(&next, 100, 30)
	if !strings.Contains(frame.Text, "/zeta") || !strings.Contains(frame.Text, "custom first") {
		t.Fatalf("frame did not render injected suggestion:\n%s", frame.Text)
	}
}

func TestSlashCompletionDoesNotActivateAfterArguments(t *testing.T) {
	t.Parallel()
	orch := testCloakOrchestrator(t)
	m := NewRootModel(orch, app.SessionID("slash-args"), "../../testdata/apps/cloak/app.yaml", "")
	m.prompt.SetValue("/open README")

	frame := ComposeFrame(&m, 100, 30)
	if strings.Contains(frame.Text, "(Tab)") {
		t.Fatalf("argument-bearing slash command should not show completion:\n%s", frame.Text)
	}
}
