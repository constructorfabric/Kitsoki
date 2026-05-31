package history

import (
	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

const (
	// WorldKey is the reserved world-variable name under which the stack
	// is serialised. It is dollar-prefixed so it cannot collide with an
	// author-declared world variable (author keys are bare identifiers).
	WorldKey = "$history"
	// maxDepth caps the stack so a runaway navigation loop cannot grow
	// world state without bound; on overflow the oldest entry is evicted,
	// keeping the most recent navigations recoverable. The value is a
	// pragmatic ceiling on how deep a back chain a user would unwind by
	// hand, not a hard protocol limit.
	maxDepth = 10
)

// Entry is one recorded visit: the room the user landed in plus the
// slots bound on arrival, so a later back restores both the room and the
// bindings the user saw there. The zero Entry (empty State, nil Slots)
// is a valid but unset entry; it never arises from Push, which always
// records a concrete state, and callers should treat an empty State as
// "no history" rather than a navigable target.
type Entry struct {
	// State is the state path of the room the user visited.
	State app.StatePath `json:"state"`
	// Slots are the slot bindings present when the user arrived, snapshotted
	// so back can restore them even if the live bindings have since changed.
	Slots map[string]any `json:"slots,omitempty"`
}

// Stack is the per-turn, in-memory room history. It is NOT
// concurrency-safe: it is owned by a single turn under the
// orchestrator's per-session writer lock, so callers must serialise
// access via that external lock rather than sharing a Stack across
// goroutines. The zero value is not usable — always construct through
// [New] or [FromWorld] so the main-room fallback is set; a Stack with no
// fallback would have nowhere to send an empty-stack back.
type Stack struct {
	entries  []Entry
	mainRoom app.StatePath
}

// New returns an empty Stack whose empty-stack back falls through to
// mainRoom. The fallback is fixed at construction because the canonical
// "home" room does not change within a session, and capturing it here
// lets Pop stay total (it never has to error on an empty stack).
func New(mainRoom app.StatePath) *Stack {
	return &Stack{mainRoom: mainRoom}
}

// Push records a visit to state with the slots bound on arrival. The
// slots are shallow-cloned so later mutation of the caller's map does
// not corrupt stored history. When the push would exceed maxDepth the
// OLDEST entry is evicted, never the newest, keeping recent navigation
// recoverable under a bound.
func (s *Stack) Push(state app.StatePath, slots map[string]any) {
	e := Entry{State: state, Slots: cloneSlots(slots)}
	s.entries = append(s.entries, e)
	if len(s.entries) > maxDepth {
		// Evict oldest (index 0).
		s.entries = s.entries[len(s.entries)-maxDepth:]
	}
}

// Pop removes and returns the top visit. The bool reports whether real
// history was popped: on an empty stack it is false and the returned
// state is the construction-time main room (with nil slots), so back is
// always well-defined and callers need no separate emptiness check.
func (s *Stack) Pop() (app.StatePath, map[string]any, bool) {
	if len(s.entries) == 0 {
		return s.mainRoom, nil, false
	}
	top := s.entries[len(s.entries)-1]
	s.entries = s.entries[:len(s.entries)-1]
	return top.State, top.Slots, true
}

// Peek returns the top entry without consuming it, for rendering a back
// affordance ("← back to <room>") without committing the navigation. The
// bool is false on an empty stack; the returned Entry is then the zero
// value, which callers must not treat as a navigable target.
func (s *Stack) Peek() (Entry, bool) {
	if len(s.entries) == 0 {
		return Entry{}, false
	}
	return s.entries[len(s.entries)-1], true
}

// Clear drops all history. The machine calls it on a transition to the
// main room: the user is back home, so every prior visit becomes
// unreachable and keeping it would let a later back unwind past "home."
func (s *Stack) Clear() {
	s.entries = s.entries[:0]
}

// Len reports the current depth, used to decide whether a back
// affordance is meaningful and never exceeds maxDepth.
func (s *Stack) Len() int {
	return len(s.entries)
}

// Entries returns a defensive copy of the stack, oldest first, so a
// caller inspecting or serialising history cannot mutate the live stack
// through the returned slice. The result is non-nil but may be empty.
func (s *Stack) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// ── World integration helpers ─────────────────────────────────────────────────

// FromWorld rehydrates a Stack from the world snapshot at the start of a
// turn. It never errors: a missing or malformed [WorldKey] value yields
// an empty stack (with the given mainRoom fallback) rather than failing
// the turn, because corrupt history must not be able to wedge
// navigation — the worst case is simply losing back targets. Entries
// with an empty state path are skipped as unnavigable.
func FromWorld(w world.World, mainRoom app.StatePath) *Stack {
	s := New(mainRoom)
	raw, ok := w.Vars[WorldKey]
	if !ok || raw == nil {
		return s
	}
	// The entries are stored as []any (from JSON unmarshal of map[string]any).
	entries, ok := raw.([]any)
	if !ok {
		return s
	}
	for _, item := range entries {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		state, _ := m["state"].(string)
		if state == "" {
			continue
		}
		var slots map[string]any
		if sv, ok := m["slots"]; ok {
			slots, _ = sv.(map[string]any)
		}
		s.entries = append(s.entries, Entry{
			State: app.StatePath(state),
			Slots: slots,
		})
	}
	return s
}

// ToWorld serialises the stack back into a world snapshot at the end of
// a turn, returning a new world (worlds are immutable) with [WorldKey]
// updated. Entries are stored as []any of map[string]any so the value
// round-trips through JSON unmarshalling unchanged, which is exactly the
// shape [FromWorld] expects to read back.
func ToWorld(s *Stack, w world.World) world.World {
	entries := s.Entries()
	// Convert to []any for JSON-safe storage.
	raw := make([]any, len(entries))
	for i, e := range entries {
		m := map[string]any{"state": string(e.State)}
		if len(e.Slots) > 0 {
			m["slots"] = e.Slots
		}
		raw[i] = m
	}
	return w.With(WorldKey, raw)
}

// cloneSlots makes a shallow copy of a slots map.
func cloneSlots(slots map[string]any) map[string]any {
	if slots == nil {
		return nil
	}
	out := make(map[string]any, len(slots))
	for k, v := range slots {
		out[k] = v
	}
	return out
}
