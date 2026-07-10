package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const slashCompletionLimit = 6

// SlashCommandSpec describes one discoverable slash command.
type SlashCommandSpec struct {
	Name        string
	Usage       string
	Description string
	Aliases     []string
}

// SlashSuggester ranks/filter slash commands for the prompt affordance.
type SlashSuggester interface {
	SuggestSlashCommands(m RootModel, query string) []SlashCommandSpec
}

type defaultSlashSuggester struct{}

func (defaultSlashSuggester) SuggestSlashCommands(m RootModel, query string) []SlashCommandSpec {
	query = strings.TrimPrefix(strings.TrimSpace(strings.ToLower(query)), "/")
	specs := slashCommandCatalogue(m)
	if query == "" {
		return specs
	}

	type ranked struct {
		spec  SlashCommandSpec
		score int
		idx   int
	}
	var matches []ranked
	for i, spec := range specs {
		score, ok := slashCommandMatchScore(spec, query)
		if !ok {
			continue
		}
		matches = append(matches, ranked{spec: spec, score: score, idx: i})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].idx < matches[j].idx
	})
	out := make([]SlashCommandSpec, 0, len(matches))
	for _, match := range matches {
		out = append(out, match.spec)
	}
	return out
}

func slashCommandMatchScore(spec SlashCommandSpec, query string) (int, bool) {
	names := append([]string{spec.Name}, spec.Aliases...)
	for _, name := range names {
		trimmed := strings.TrimPrefix(strings.ToLower(name), "/")
		switch {
		case trimmed == query:
			return 0, true
		case strings.HasPrefix(trimmed, query):
			return 1, true
		case strings.Contains(trimmed, query):
			return 2, true
		}
	}
	if strings.Contains(strings.ToLower(spec.Description), query) {
		return 3, true
	}
	return 0, false
}

func slashCommandCatalogue(m RootModel) []SlashCommandSpec {
	enterCmd, exitCmd := offPathTriggers(m.orch.AppDef())
	specs := []SlashCommandSpec{
		{Name: "/help", Description: "show commands"},
		{Name: "/stories", Description: "open the story selector"},
		{Name: "/bug", Usage: "/bug [description]", Description: "file a bug report with TUI evidence"},
		{Name: "/route", Usage: "/route up|down", Description: "rate the last routed turn"},
		{Name: "/ideas", Usage: "/ideas <text>", Description: "capture an idea without interrupting the session"},
		{Name: "/chat", Usage: "/chat show <id>", Description: "show async chat context"},
		{Name: "/intents", Usage: "/intents [<n>]", Description: "show or dispatch available intents"},
		{Name: "/provider", Usage: "/provider [<name|n>]", Description: "list or switch harness provider"},
		{Name: "/model", Usage: "/model [<id|n>]", Description: "list or switch model"},
		{Name: "/effort", Usage: "/effort [<level|n>]", Description: "list or switch reasoning effort"},
		{Name: "/inbox", Usage: "/inbox [<n>]", Description: "show notifications"},
		{Name: "/work", Usage: "/work [--all|drive|artifact|summary]", Description: "show active async work, drive an operation, open its artifact, or show its summary"},
		{Name: "/workflow", Usage: "/workflow <cmd>", Description: "create, validate, run, status, or export workflows"},
		{Name: "/sessions", Usage: "/sessions attach <n>", Description: "attach or inspect meta sessions"},
		{Name: "/trace", Description: "show the last turn routing trace"},
		{Name: "/viz", Description: "export a DOT state diagram"},
		{Name: "/input", Description: "restore a draft cleared by a picker"},
		{Name: "/world", Description: "open the world viewer"},
		{Name: "/meta", Usage: "/meta [name]", Description: "enter or manage meta mode"},
		{Name: "/jump", Usage: "/jump [<n>]", Description: "jump to recent background completion"},
		{Name: "/ide", Usage: "/ide status|connect|disconnect", Description: "manage editor connection"},
		{Name: "/open", Usage: "/open <path>", Description: "open an artifact"},
		{Name: "/warp", Usage: "/warp <state>", Description: "developer teleport"},
		{Name: "/reload", Usage: "/reload [--force]", Description: "reload app.yaml"},
		{Name: "/mine", Usage: "/mine status|pause|resume", Description: "inspect or control ambient mining"},
		{Name: enterCmd, Description: "enter off-path chat"},
		{Name: exitCmd, Description: "return from off-path chat"},
		{Name: "/quit", Description: "exit kitsoki", Aliases: []string{"/q"}},
	}
	return specs
}

func (m RootModel) slashCompletionActive() bool {
	value := m.prompt.Value()
	if !strings.HasPrefix(value, "/") {
		return false
	}
	return !strings.ContainsAny(value, " \t\n")
}

func (m RootModel) slashSuggestions() []SlashCommandSpec {
	if !m.slashCompletionActive() {
		return nil
	}
	suggester := m.slashSuggester
	if suggester == nil {
		suggester = defaultSlashSuggester{}
	}
	suggestions := suggester.SuggestSlashCommands(m, m.prompt.Value())
	if len(suggestions) > slashCompletionLimit {
		suggestions = suggestions[:slashCompletionLimit]
	}
	return suggestions
}

func (m RootModel) primarySlashCompletion() (string, bool) {
	suggestions := m.slashSuggestions()
	if len(suggestions) == 0 {
		return "", false
	}
	return suggestions[0].Name, true
}

func (m RootModel) slashCompletionView(width int) string {
	suggestions := m.slashSuggestions()
	if len(suggestions) == 0 {
		return ""
	}
	style := lipgloss.NewStyle().Foreground(colorMuted)
	primaryStyle := style.Bold(true)
	var lines []string
	for i, spec := range suggestions {
		label := spec.Usage
		if label == "" {
			label = spec.Name
		}
		line := "  " + label
		if i == 0 {
			line += "  " + "(Tab)"
		}
		if spec.Description != "" {
			line += "  " + spec.Description
		}
		if width > 0 {
			line = lipgloss.NewStyle().MaxWidth(width).Render(line)
		}
		if i == 0 {
			lines = append(lines, primaryStyle.Render(line))
			continue
		}
		lines = append(lines, style.Render(line))
	}
	return strings.Join(lines, "\n")
}
