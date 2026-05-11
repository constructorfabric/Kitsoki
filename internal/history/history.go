// Package history implements the Room history stack (§5).
//
// The stack records prior state references (state path + slots snapshot) for
// back-navigation. It is consumed by three features:
//   - Proposal on_success: back (§3)
//   - Inbox teleport return (§4)
//   - Oracle Room exit (§7)
//
// # Semantics
//
// Push on transition-in; pop on back. Bounded to maxDepth entries (oldest evicted).
// Teleport transitions push the inbox predecessor, not the inbox itself.
// Transitions flagged push_history: false are stackless (used for Oracle, Inbox).
// Transitioning to the Main Room (reset point) clears the stack.
// Empty-stack back returns to the main_room defined at construction time.
//
// # Persistence
//
// The stack is stored as a JSON-serialisable slice in world state under the
// reserved key $history. Callers read/write via Get/Set helpers below.
package history

import (
	"kitsoki/internal/app"
	"kitsoki/internal/world"
)

const (
	// WorldKey is the reserved world variable name for the history stack.
	WorldKey = "$history"
	// maxDepth is the maximum number of entries in the stack (§5.1).
	maxDepth = 10
)

// Entry is one element of the room history stack.
type Entry struct {
	// State is the state path of the room.
	State app.StatePath `json:"state"`
	// Slots are the slots bound when the user arrived at that state.
	Slots map[string]any `json:"slots,omitempty"`
}

// Stack is the in-memory representation of the room history.
// It wraps a bounded slice of entries with push/pop/peek operations.
type Stack struct {
	entries []Entry
	mainRoom app.StatePath
}

// New creates an empty Stack with the given main-room fallback.
func New(mainRoom app.StatePath) *Stack {
	return &Stack{mainRoom: mainRoom}
}

// Push adds a state to the top of the history stack.
// If the stack exceeds maxDepth, the oldest entry is evicted.
func (s *Stack) Push(state app.StatePath, slots map[string]any) {
	e := Entry{State: state, Slots: cloneSlots(slots)}
	s.entries = append(s.entries, e)
	if len(s.entries) > maxDepth {
		// Evict oldest (index 0).
		s.entries = s.entries[len(s.entries)-maxDepth:]
	}
}

// Pop removes and returns the top entry. Returns (mainRoom, nil, false) when empty.
func (s *Stack) Pop() (app.StatePath, map[string]any, bool) {
	if len(s.entries) == 0 {
		return s.mainRoom, nil, false
	}
	top := s.entries[len(s.entries)-1]
	s.entries = s.entries[:len(s.entries)-1]
	return top.State, top.Slots, true
}

// Peek returns the top entry without removing it.
func (s *Stack) Peek() (Entry, bool) {
	if len(s.entries) == 0 {
		return Entry{}, false
	}
	return s.entries[len(s.entries)-1], true
}

// Clear empties the stack (called on transition to Main Room).
func (s *Stack) Clear() {
	s.entries = s.entries[:0]
}

// Len returns the current stack depth.
func (s *Stack) Len() int {
	return len(s.entries)
}

// Entries returns a copy of the entries, oldest first.
func (s *Stack) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	copy(out, s.entries)
	return out
}

// ── World integration helpers ─────────────────────────────────────────────────

// FromWorld reads the history stack stored in world state.
// Returns an empty stack if the world key is absent or malformed.
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

// ToWorld writes the history stack back into a world snapshot.
// Returns a new world with the $history key updated.
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
