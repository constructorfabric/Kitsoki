package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"kitsoki/internal/world"
)

func newTestWorld() world.World {
	return world.World{Vars: map[string]any{
		"session": map[string]any{
			"id": "sess_42",
			"user": map[string]any{
				"name": "brad",
				"role": "dev",
			},
		},
		"tickets":     []any{"PLTFRM-89912", "PLTFRM-90001"},
		"counter":     3,
		"on_break":    true,
		"placeholder": nil,
	}}
}

func TestWorldViewFlattenTopLevelCollapsed(t *testing.T) {
	t.Parallel()
	m := newWorldViewModel(newTestWorld(), "test", 80, 24)
	rows := m.flatten()
	// Top-level vars only: session, tickets, counter, on_break, placeholder
	// Order is alphabetical.
	var keys []string
	for _, r := range rows {
		keys = append(keys, r.key)
	}
	want := []string{"counter", "on_break", "placeholder", "session", "tickets [2]"}
	if !equalStringSlices(keys, want) {
		t.Errorf("collapsed top-level keys mismatch\nwant: %v\ngot:  %v", want, keys)
	}
}

func TestWorldViewExpandKey(t *testing.T) {
	t.Parallel()
	m := newWorldViewModel(newTestWorld(), "test", 80, 24)
	// Cursor at "session" (alphabetically last in top level but
	// before "tickets" — let me check: counter, on_break, placeholder,
	// session, tickets. Position 3.
	m.cursor = 3
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	rows := updated.flatten()
	// After expanding session, the rows should include id and user.
	found := false
	for _, r := range rows {
		if r.key == "id" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'id' to appear after expanding session\nrows: %v", rows)
	}
}

func TestWorldViewCollapseKey(t *testing.T) {
	t.Parallel()
	m := newWorldViewModel(newTestWorld(), "test", 80, 24)
	m.expanded["session"] = true
	m.cursor = 3 // session
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if updated.expanded["session"] {
		t.Errorf("session should be collapsed after Left key")
	}
}

func TestWorldViewExpandSubtree(t *testing.T) {
	t.Parallel()
	m := newWorldViewModel(newTestWorld(), "test", 80, 24)
	m.cursor = 3 // session
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("e")})
	if !updated.expanded["session"] {
		t.Errorf("e should expand session subtree")
	}
	if !updated.expanded["session.user"] {
		t.Errorf("e should recursively expand session.user")
	}
}

func TestWorldViewCursorBounds(t *testing.T) {
	t.Parallel()
	m := newWorldViewModel(newTestWorld(), "test", 80, 24)
	m.cursor = 0
	// up at the top stays at 0.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.cursor != 0 {
		t.Errorf("cursor up at 0 should stay at 0, got %d", updated.cursor)
	}
	// end jumps to last.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if updated.cursor != len(updated.flatten())-1 {
		t.Errorf("end should jump to last row")
	}
}

func TestWorldViewRendersTitle(t *testing.T) {
	t.Parallel()
	m := newWorldViewModel(newTestWorld(), "myroom", 80, 24)
	out := m.View()
	if !strings.Contains(out, "myroom") {
		t.Errorf("view should mention room id; got:\n%s", out)
	}
	if !strings.Contains(out, "navigate") {
		t.Errorf("view should include the footer keybinding hint")
	}
}

func TestWorldViewCurrentPath(t *testing.T) {
	t.Parallel()
	m := newWorldViewModel(newTestWorld(), "test", 80, 24)
	m.cursor = 0 // "counter"
	if got := m.CurrentPath(); got != "counter" {
		t.Errorf("expected 'counter' path, got %q", got)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
