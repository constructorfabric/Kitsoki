package tui

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui/blocks"
)

// ActionsCommand implements `/actions`, `/actions <n>`, and
// `/actions auto on|off`. The actions block reuses the current room's
// menu items (already computed by the orchestrator) and renders them
// via blocks.Renderer.Menu so the visual matches Phase 0's preview.
//
// Phase 1 ships the framework default rendering — rooms can later
// declare their own pongo2 template for the actions block (proposal
// §"Rendering is room-provided"); when they do, this command will
// dispatch to the room-supplied template, falling back to the default.
type ActionsCommand struct{}

func (ActionsCommand) Name() string { return "/actions" }

func (ActionsCommand) Run(m RootModel, args []string) (string, RootModel, tea.Cmd) {
	// Sub-command parsing: `/actions auto on|off` toggles the
	// per-session auto-print flag; `/actions <n>` dispatches a row;
	// `/actions` alone prints the block.
	if len(args) >= 2 && strings.EqualFold(args[0], "auto") {
		switch strings.ToLower(args[1]) {
		case "on":
			m.actionsAuto = true
			return blockSlashLine(m, "(actions auto on — actions block will print after each turn)"), m, nil
		case "off":
			m.actionsAuto = false
			return blockSlashLine(m, "(actions auto off)"), m, nil
		default:
			return blockSlashLine(m, "(actions: usage: /actions auto on|off)"), m, nil
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

// dispatchActionByIndex routes /actions <n> to the orchestrator. The
// index is 1-based to match the rendered block. A row whose guard
// fails dispatches anyway and lets the orchestrator surface the
// rejection through its normal channels — same as picking the row from
// the legacy menu UI.
func dispatchActionByIndex(m RootModel, n int) (string, RootModel, tea.Cmd) {
	all := append([]orchestrator.MenuEntry{}, m.menu.items...)
	all = append(all, m.menu.blocked...)
	if n < 1 || n > len(all) {
		return blockSlashLine(m, "(actions: index out of range — try /actions to see the list)"), m, nil
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
// blocks.Renderer so it carries the same styling as /actions itself
// rather than the legacy slashOutputStyle path. Helps keep all phase 1
// chat-block output visually consistent.
func blockSlashLine(m RootModel, text string) string {
	r := blocks.New(m.transcript.width, m.currentTheme())
	return r.SlashOutput(text)
}

// maybeAutoActions is called from handleTurnOutcome after a successful
// turn. When the user has toggled /actions auto on, it returns the
// actions block string so the caller can append it before the prompt
// is shown again. Returns "" when auto-print is off.
func (m RootModel) maybeAutoActions() string {
	if !m.actionsAuto {
		return ""
	}
	return renderActionsBlock(m)
}

