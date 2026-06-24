package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// menuSystemAction identifies a chosen row in the system menu.
type menuSystemAction int

const (
	menuActionNone menuSystemAction = iota
	menuActionExit
	// menuActionMetaMode opens a specific meta mode. The entry's
	// modeName carries which one; the handler dispatches into
	// startMetaMode(modeName).
	menuActionMetaMode
	// menuActionMetaSessions opens the foyer "meta sessions" panel —
	// an overlay that lists every active meta chat
	// (this app's plus cross-app `self` chats) and lets the user
	// resume one without typing `/meta resume <id>`.
	menuActionMetaSessions
	// menuActionHelp prints the `/help` command list into the
	// transcript — the same block `/help` produces, surfaced for
	// discoverability so a plain app's Esc menu isn't just "Exit".
	menuActionHelp
	// menuActionWorld opens the `/world` viewer for the current session.
	menuActionWorld
)

// menuSystemChoiceMsg is emitted when the user selects a row. modeName
// is non-empty only for menuActionMetaMode rows.
type menuSystemChoiceMsg struct {
	action   menuSystemAction
	modeName string
}

// menuSystemEntry describes one row in the overlay.
type menuSystemEntry struct {
	action   menuSystemAction
	label    string
	hint     string
	modeName string // mode name for menuActionMetaMode entries; "" otherwise
}

// metaHintFromKey renders a `/meta <key>` hint for a meta-mode map key.
// Grouped keys (`story.edit`) become `/meta story edit`; un-namespaced
// keys (`story`) render unchanged.
func metaHintFromKey(key string) string {
	if dot := strings.Index(key, "."); dot > 0 {
		return "/meta " + key[:dot] + " " + key[dot+1:]
	}
	return "/meta " + key
}

// menuSystemModel is the Esc-activated overlay that exposes session-level
// actions (exit, report bug, and one row per declared meta mode). It
// follows the same Open/Close + Update/View shape as disambiguationModel.
type menuSystemModel struct {
	active   bool
	entries  []menuSystemEntry
	selected int
}

// metaMenuEntry is a (mode name, display label) pair passed to
// newMenuSystemModel so the overlay can list every declared meta
// mode. Order is the caller's responsibility — pass sorted names for
// deterministic output. Empty slice → no meta rows.
type metaMenuEntry struct {
	Name  string
	Label string
}

// newMenuSystemModel builds the overlay's entry list. metaEntries
// produces one row per declared meta mode; pass an empty slice when
// the AppDef declares (or has injected) no meta_modes.
func newMenuSystemModel(metaEntries []metaMenuEntry) menuSystemModel {
	entries := []menuSystemEntry{
		{action: menuActionExit, label: "Exit", hint: "quit this session"},
	}
	for _, me := range metaEntries {
		if me.Name == "" {
			continue
		}
		label := me.Label
		if label == "" {
			label = "meta: " + me.Name
		}
		entries = append(entries, menuSystemEntry{
			action:   menuActionMetaMode,
			label:    label,
			hint:     metaHintFromKey(me.Name),
			modeName: me.Name,
		})
	}
	if len(metaEntries) > 0 {
		// Only surface the "Meta sessions" panel when there's at least
		// one mode the user could have a session under.
		entries = append(entries, menuSystemEntry{
			action: menuActionMetaSessions,
			label:  "Meta sessions",
			hint:   "browse + resume active meta chats",
		})
	}
	// Help + World are always available — they're the discoverability
	// floor, so even a plain app (no meta modes) gets more than just
	// "Exit" in the Esc menu. Appended last so any meta-mode hotkey
	// row indices stay stable.
	entries = append(entries,
		menuSystemEntry{action: menuActionHelp, label: "Help", hint: "list the slash commands (/help)"},
		menuSystemEntry{action: menuActionWorld, label: "World", hint: "inspect the world state (/world)"},
	)
	return menuSystemModel{entries: entries}
}

// Open activates the overlay with the selection reset to the first row.
func (m *menuSystemModel) Open() {
	m.active = true
	m.selected = 0
}

// Close deactivates the overlay.
func (m *menuSystemModel) Close() {
	m.active = false
	m.selected = 0
}

// IsActive reports whether the overlay is currently visible.
func (m menuSystemModel) IsActive() bool { return m.active }

func (m menuSystemModel) Init() tea.Cmd { return nil }

func (m menuSystemModel) Update(msg tea.Msg) (menuSystemModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}

	switch keyMsg.String() {
	case "esc", "q":
		m.active = false
		return m, nil

	case "up", "k":
		if m.selected > 0 {
			m.selected--
		}
		return m, nil

	case "down", "j":
		if m.selected < len(m.entries)-1 {
			m.selected++
		}
		return m, nil

	case "enter":
		chosen := m.entries[m.selected]
		m.active = false
		return m, func() tea.Msg {
			return menuSystemChoiceMsg{action: chosen.action, modeName: chosen.modeName}
		}
	}

	// Numeric hotkeys 1..N.
	for i := 1; i <= len(m.entries) && i <= 9; i++ {
		if keyMsg.String() == fmt.Sprintf("%d", i) {
			chosen := m.entries[i-1]
			m.active = false
			m.selected = i - 1
			return m, func() tea.Msg {
				return menuSystemChoiceMsg{action: chosen.action, modeName: chosen.modeName}
			}
		}
	}

	return m, nil
}

// View renders the overlay. Returns an empty string when inactive.
func (m menuSystemModel) View() string {
	if !m.active {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("menu (↑/↓ to move, Enter to pick, Esc to close)\n\n")
	for i, e := range m.entries {
		marker := "  "
		label := menuItemStyle.Render(e.label)
		if i == m.selected {
			marker = "▸ "
			label = menuItemSelectedStyle.Render(e.label)
		}
		sb.WriteString(fmt.Sprintf("%s[%d] %s", marker, i+1, label))
		if e.hint != "" {
			sb.WriteString(" — ")
			sb.WriteString(menuItemBlockedStyle.Render(e.hint))
		}
		sb.WriteString("\n")
	}
	return sb.String()
}
