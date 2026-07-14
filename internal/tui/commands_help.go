package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/tui/blocks"
)

// HelpCommand implements `/help` — a chat block listing every slash
// command grouped by category. Mirrors Claude Code's /help shape: short
// one-line description per command.
type HelpCommand struct{}

func (HelpCommand) Name() string { return "/help" }

func (HelpCommand) Run(m RootModel, _ []string) (string, RootModel, tea.Cmd) {
	r := blocks.New(m.transcript.width, m.currentTheme())
	r = r.WithNoColor(false)

	type row struct{ cmd, desc string }
	type section struct {
		title string
		rows  []row
	}
	sections := []section{
		{"chat blocks", []row{
			{"/help", "this list"},
			{"/stories", "open the story selector and launch another story"},
			{"/bug [description]", "file a bug report with scrubbed TUI transcript evidence"},
			{"/route up|down", "record a thumbs up/down verdict on the last routed turn"},
			{"/route retry <intent|help|workbench|meta>", "reroute the last routed turn to a different class"},
			{"/ideas <text>", "jot an idea to ideas.md without interrupting the conversation"},
			{"/chat show <id>", "show focused async chat context without attaching"},
			{"/intents [<n>]", "print available intents; <n> dispatches by index"},
			{"/intents auto on|off", "auto-print intents after each turn"},
			{"/provider [<name|n>]", "list harness profiles; <name|n> switches the backend/provider (next turn)"},
			{"/model [<id|n>]", "list the active profile's models; <id|n> switches the model (next turn)"},
			{"/effort [<level|n>]", "list/switch the active profile's reasoning effort (where the model supports it)"},
			{"/inbox [<n>]", "list recent notifications; <n> opens one"},
			{"/inbox sync-github [repo]", "refresh assigned GitHub issues and requested PR reviews"},
			{"/work [--all|drive|artifact|summary]", "list active async work, drive the current operation, open its artifact, or show its summary"},
			{"/workflow <cmd>", "create, validate, run, status, or export a workflow draft"},
			{"/sessions attach <n> --dry-run", "show the cached chat target without attaching"},
			{"/trace", "print the last turn's routing trace"},
			{"/viz", "export the state diagram to a DOT file"},
			{"/input", "restore your prior chat draft (cleared when a choice widget opens)"},
		}},
		{"dedicated views", []row{
			{"/world", "open the world viewer (q or Esc to close)"},
		}},
		{"room switches", []row{
			{"/meta [name]", "enter a meta-mode room (parallel transcript)"},
			{"/meta list", "list meta sessions"},
			{"/meta done", "leave meta mode"},
			{"/jump [<n>]", "jump to a recent background-completion (0-indexed, newest first)"},
		}},
		{"system", []row{
			{"/ide", "connect to the editor (or show status if connected)"},
			{"/ide connect [<n>]", "discover + connect; <n> picks among matching lock files"},
			{"/ide disconnect", "close the editor link; stop ambient selection capture"},
			{"/ide status", "show connection: editor, workspace, port"},
			{"/open <path>", "open an artifact in $EDITOR or the OS default handler"},
			{"/warp <state>", "developer teleport to a state"},
			{"/reload [--force]", "reload app.yaml; --force bypasses once: cache for this rerun"},
			{"/quit, /q", "exit kitsoki"},
		}},
	}

	var sb strings.Builder
	sb.WriteString(r.SlashOutput("commands"))
	sb.WriteString("\n")
	for i, s := range sections {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(r.SlashOutput("  " + s.title))
		sb.WriteString("\n")
		for _, row := range s.rows {
			// Pad command column to 22 chars so descriptions line up.
			cmd := row.cmd
			if len(cmd) < 22 {
				cmd += strings.Repeat(" ", 22-len(cmd))
			}
			line := "    " + cmd + row.desc
			sb.WriteString(line)
			sb.WriteString("\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n"), m, nil
}
