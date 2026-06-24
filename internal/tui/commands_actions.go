package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// ActionsCommand implements `/intents`, `/intents <n>`, and
// `/intents auto on|off`. The intents block reuses the current room's
// menu items (already computed by the orchestrator) and renders them
// via blocks.Renderer.Menu so the visual matches Phase 0's preview.
//
// Phase 1 ships the framework default rendering — rooms can later
// declare their own pongo2 template for the intents block (proposal
// §"Rendering is room-provided"); when they do, this command will
// dispatch to the room-supplied template, falling back to the default.
type ActionsCommand struct{}

func (ActionsCommand) Name() string { return "/intents" }

func (ActionsCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	// Sub-command parsing: `/intents auto on|off` toggles the
	// per-session auto-print flag; `/intents <n>` dispatches a row;
	// `/intents` alone prints the block.
	if len(args) >= 2 && strings.EqualFold(args[0], "auto") {
		switch strings.ToLower(args[1]) {
		case "on":
			m.actionsAuto = true
			return blockSlashLine(m, "(intents auto on — intents block will print after each turn)"), m, nil
		case "off":
			m.actionsAuto = false
			return blockSlashLine(m, "(intents auto off)"), m, nil
		default:
			return blockSlashLine(m, "(intents: usage: /intents auto on|off)"), m, nil
		}
	}
	if len(args) == 1 {
		if n, err := strconv.Atoi(args[0]); err == nil {
			return dispatchActionByIndex(m, n)
		}
	}
	return renderActionsBlock(m), m, nil
}

// renderActionsBlock builds the actions transcript block from the
// current menu state. The block is built fresh each time — the menu
// is cheap to read from m.menu, and stale snapshots would be confusing.
func renderActionsBlock(m RootModel) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	rows := make([]blocks.MenuAction, 0, len(m.menu.items)+len(m.menu.blocked))
	idx := 1
	for _, e := range m.menu.items {
		rows = append(rows, blocks.MenuAction{
			Index:     idx,
			Name:      e.Intent,
			Label:     e.Display,
			Available: true,
		})
		idx++
	}
	for _, e := range m.menu.blocked {
		rows = append(rows, blocks.MenuAction{
			Index:     idx,
			Name:      e.Intent,
			Label:     e.Display,
			Available: false,
			GuardHint: e.Reason,
		})
		idx++
	}
	return r.Menu(rows)
}

// dispatchActionByIndex routes /intents <n> to the orchestrator. The
// index is 1-based to match the rendered block. A row whose guard
// fails dispatches anyway and lets the orchestrator surface the
// rejection through its normal channels — same as picking the row from
// the legacy menu UI.
func dispatchActionByIndex(m RootModel, n int) (string, RootModel, tea.Cmd) {
	all := append([]orchestrator.MenuEntry{}, m.menu.items...)
	all = append(all, m.menu.blocked...)
	if n < 1 || n > len(all) {
		return blockSlashLine(m, "(intents: index out of range — try /intents to see the list)"), m, nil
	}
	entry := all[n-1]
	// Use the existing dispatch path so the rest of the system (history
	// echo, journal write, view re-render) behaves identically to a
	// number-key press today.
	updated, cmd := m.dispatchMenuEntry(&entry)
	if rm, ok := updated.(RootModel); ok {
		return "", rm, cmd
	}
	return "", m, cmd
}

// blockSlashLine renders a "(...)" slash-feedback line through the
// blocks.Renderer so it carries the same styling as /intents itself
// rather than the legacy slashOutputStyle path. Helps keep all phase 1
// chat-block output visually consistent.
func blockSlashLine(m RootModel, text string) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	return r.SlashOutput(text)
}

// maybeAutoActions is called from handleTurnOutcome after a successful
// turn. When the user has toggled /intents auto on, it returns the
// intents block string so the caller can append it before the prompt
// is shown again. Returns "" when auto-print is off.
func (m RootModel) maybeAutoActions() string {
	if !m.actionsAuto {
		return ""
	}
	return renderActionsBlock(m)
}
