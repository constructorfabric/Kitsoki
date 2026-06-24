package tui

import tea "github.com/charmbracelet/bubbletea"

// Phase 1 of the single-pane-tui proposal establishes three command
// flavours so future commands pick the right shape from day one:
//
//   - ChatBlockCommand     — renders a transcript block; the pane stays
//                            on chat view.
//   - DedicatedViewCommand — takes over the pane (today /world; future
//                            /trace, /viz); the chat keeps running
//                            underneath.
//   - RoomSwitchCommand    — swaps the active room (and transcript and
//                            theme); the on-path room keeps processing.
//
// Phase 1 only ships the ChatBlockCommand surface — /help and /intents
// are the first commands wired through it. Existing commands stay on
// the inline switch in handleSlashCommand for now; they migrate as
// later phases convert their overlays into inline blocks.
//
// Why an interface at all in phase 1? It locks the three flavours into
// the codebase, so when a new slash command shows up it picks one
// rather than ad-hoc-extending the switch with a fourth pattern. The
// proposal's "load-bearing distinction" — chat block vs dedicated view
// vs room switch — is encoded here.

// ChatBlockCommand renders a single transcript block and returns. The
// command receives the slash arguments (everything after the command
// name, e.g. `auto on` for `/intents auto on`) and the current model so
// it can read state (room, mode, current menu). It returns the styled
// block body — the caller appends it via transcript.AppendBlock — and
// an updated model plus optional follow-up Cmd.
type ChatBlockCommand interface {
	Name() string
	Run(m RootModel, args []string) (body string, next RootModel, cmd tea.Cmd)
}

// DedicatedViewCommand opens a view that fully replaces the pane until
// dismissed. Phase 1.5 wires /world through this interface.
// Intentionally defined-but-unused in Phase 1 so callers know the
// shape.
type DedicatedViewCommand interface {
	Name() string
	Open(m RootModel, args []string) (next RootModel, cmd tea.Cmd)
}

// RoomSwitchCommand changes which room is active. /jump and /meta are
// the two examples in the proposal. Phase 4+ migrate these as the
// per-room transcript model lands.
type RoomSwitchCommand interface {
	Name() string
	Switch(m RootModel, args []string) (next RootModel, cmd tea.Cmd)
}
