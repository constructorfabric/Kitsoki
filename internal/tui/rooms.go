// rooms.go — per-room transcript buffers and per-room theme swap.
//
// The single-pane-tui proposal defines a "room" as the outermost
// (top-level) state in app.yaml. Two on-path rooms (`foyer` and `bar`)
// each own their own transcript buffer; navigating from one to the
// other saves the outgoing buffer and activates the incoming one.
// Meta mode pulls the same machinery — entering /meta is treated as
// a room switch into a synthetic room (metaRoomKey) with a forced
// transient transcript and a theme swap.
//
// Naming: the app package calls these top-level states "states" with
// no prefix; from the TUI's point of view they are "rooms". The
// helpers below translate between a runtime app.StatePath and the
// room key the transcript map is indexed by.

package tui

import (
	"kitsoki/internal/app"
)

// metaRoomKey is the synthetic room identifier used when a /meta
// overlay owns the pane. It begins with double underscores so it
// can't collide with any real top-level state name (the YAML schema
// permits underscore-prefixed keys, but no shipping story uses one).
const metaRoomKey app.StatePath = "__meta__"

// roomKey returns the top-level segment of a StatePath — the room
// identifier the transcript map and theme lookup are keyed by.
//
// Per the proposal, navigating between two states sharing the same
// top-level segment ("foyer.checking_in" → "foyer.signing_in") is a
// move WITHIN a room. Changing the top-level segment
// ("foyer.checking_in" → "bar.dark") is a room change.
func roomKey(s app.StatePath) app.StatePath {
	return s.TopLevel()
}

// roomDecl returns the top-level State declaration matching room or
// nil when no such state exists (e.g. the synthetic metaRoomKey, or a
// path that was warped to an alias-prefixed state from an import).
func roomDecl(def *app.AppDef, room app.StatePath) *app.State {
	if def == nil || room == "" {
		return nil
	}
	if s, ok := def.States[string(room)]; ok {
		return s
	}
	return nil
}

// transcriptKindForRoom resolves the effective transcript mode for a
// room declaration: explicit "persistent"/"transient" wins; otherwise
// conversational rooms default to transient and everything else to
// persistent. Returns "persistent" for an unknown / nil declaration —
// the safer default for on-path navigation.
func transcriptKindForRoom(s *app.State) string {
	if s != nil {
		switch s.Transcript {
		case "persistent", "transient":
			return s.Transcript
		}
		if s.Mode == "conversational" {
			return "transient"
		}
	}
	return "persistent"
}

// themeNameForRoom resolves the theme name to use for a room. Empty
// declaration falls back to "default"; meta rooms (the synthetic
// metaRoomKey or any room declared with theme: meta-blue / meta-amber)
// keep the author-declared value.
func themeNameForRoom(s *app.State) string {
	if s != nil && s.Theme != "" {
		return s.Theme
	}
	return "default"
}

// currentTheme returns the theme name for the model's active room.
// Falls back to "default" when the path is empty or the room state
// is not in the AppDef (synthetic rooms, alias-prefixed imports).
//
// ModeOffPath shadows everything with the "off-path" theme so the
// pane visibly tints amber while the user is in the escape hatch.
// ModeMeta is wired through the metaRoomKey transcript swap so its
// theme is honoured via the same activeRoom lookup.
//
// Nil-safe: when the orchestrator or AppDef hasn't been wired (test
// fixtures that build a bare RootModel), the helper returns "default"
// rather than crashing.
func (m RootModel) currentTheme() string {
	if m.mode == ModeOffPath {
		return "off-path"
	}
	room := m.activeRoom
	if room == "" {
		room = roomKey(m.currentState)
	}
	if room == metaRoomKey {
		return "meta-blue"
	}
	if m.orch == nil {
		return "default"
	}
	def := m.orch.AppDef()
	if def == nil {
		return "default"
	}
	return themeNameForRoom(roomDecl(def, room))
}

// activateRoom swaps the active transcript buffer to the one keyed
// by `room`. If the room has no saved buffer, a fresh one is created
// with the current viewport dimensions. The outgoing buffer is saved
// into m.transcripts under the room it was associated with.
//
// Returns the (possibly mutated) model so the caller can continue a
// value-receiver chain.
//
// transient controls whether the incoming buffer is scrolled past its
// prior content on activation: when true, the viewport jumps to the
// bottom of the existing content (so the next append lands at the top
// of the visible area). When false, the buffer is presented at its
// previous scroll position (or top, for a fresh buffer).
func (m *RootModel) activateRoom(room app.StatePath, transient bool) {
	if m.transcripts == nil {
		m.transcripts = make(map[app.StatePath]transcriptModel)
	}

	prev := m.activeRoom
	if prev == "" {
		// First activation — there was no prior room key.
		prev = roomKey(m.currentState)
	}

	// Save the outgoing buffer if it has content or was previously
	// active. We always save so the user can scroll back to it when
	// they return.
	if prev != "" {
		m.transcripts[prev] = m.transcript
	}

	// Activate the incoming room buffer — load from the map when
	// present, otherwise spin up a fresh transcript at the current
	// dimensions.
	if t, ok := m.transcripts[room]; ok {
		m.transcript = t
	} else {
		w, h := m.transcript.vp.Width, m.transcript.vp.Height
		if w <= 0 {
			w = m.transcript.width
		}
		if h <= 0 {
			h = m.transcript.height
		}
		if w <= 0 {
			w = 80
		}
		if h <= 0 {
			h = 20
		}
		m.transcript = newTranscriptModel(w, h)
	}

	m.activeRoom = room

	if transient {
		// Scroll past whatever was there on entry so the new content
		// lands at the top of the visible window. Matches the
		// ScrollToLine pattern exitMetaMode already uses for the
		// meta-mode mark; here it's parameterised by the incoming
		// room's transcript declaration.
		mark := m.transcript.ContentHeight()
		m.transcript.ScrollToLine(mark)
	}
}

// maybeSwitchRoomOnState detects a top-level path change between the
// previous and current state and, if one happened, swaps the active
// transcript buffer accordingly. Called from handleTurnOutcome after
// the new state has been resolved.
//
// No-op when prev and curr share a top-level segment (a within-room
// move) or when we're inside an overlay that owns the active room
// (meta mode keeps activeRoom == metaRoomKey for the duration of the
// overlay; the room switch will happen on /onpath via exitMetaMode).
func (m *RootModel) maybeSwitchRoomOnState(prev, curr app.StatePath) {
	// Meta mode owns activeRoom directly; /onpath does the inverse
	// swap. Don't fight it.
	if m.mode == ModeMeta || m.activeRoom == metaRoomKey {
		return
	}
	prevRoom := roomKey(prev)
	currRoom := roomKey(curr)
	if prevRoom == currRoom && m.activeRoom == currRoom {
		return
	}
	// First-call seam: if activeRoom is empty (no prior swap yet)
	// align it to the previous room so the save side picks the
	// right key.
	if m.activeRoom == "" {
		m.activeRoom = prevRoom
	}
	if prevRoom == currRoom {
		// activeRoom is stale (e.g. just exited meta) — sync it
		// without touching the buffer.
		m.activeRoom = currRoom
		return
	}
	transient := transcriptKindForRoom(roomDecl(m.orch.AppDef(), currRoom)) == "transient"
	m.activateRoom(currRoom, transient)
}
