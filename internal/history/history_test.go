package history_test

import (
	"testing"

	"kitsoki/internal/app"
	"kitsoki/internal/history"
	"kitsoki/internal/world"
)

func TestStack_PushPop(t *testing.T) {
	s := history.New("main")

	s.Push("room_a", nil)
	s.Push("room_b", map[string]any{"key": "val"})

	state, slots, ok := s.Pop()
	if !ok {
		t.Fatal("expected ok=true on pop")
	}
	if state != "room_b" {
		t.Fatalf("expected room_b, got %s", state)
	}
	if slots["key"] != "val" {
		t.Fatalf("expected key=val, got %v", slots)
	}

	state, _, ok = s.Pop()
	if !ok {
		t.Fatal("expected ok=true on second pop")
	}
	if state != "room_a" {
		t.Fatalf("expected room_a, got %s", state)
	}
}

func TestStack_EmptyFallback(t *testing.T) {
	s := history.New("main")
	state, slots, ok := s.Pop()
	if ok {
		t.Fatal("expected ok=false on empty pop")
	}
	if state != "main" {
		t.Fatalf("expected main room fallback, got %s", state)
	}
	if slots != nil {
		t.Fatalf("expected nil slots, got %v", slots)
	}
}

func TestStack_BoundedDepth(t *testing.T) {
	s := history.New("main")
	// Push 15 entries (max is 10).
	for i := 0; i < 15; i++ {
		s.Push(app.StatePath("room_"+string(rune('a'+i))), nil)
	}
	if s.Len() != 10 {
		t.Fatalf("expected stack depth 10, got %d", s.Len())
	}
	// The oldest 5 should have been evicted; top should be room_o (index 14).
	top, ok := s.Peek()
	if !ok {
		t.Fatal("expected non-empty peek")
	}
	if top.State != "room_o" {
		t.Fatalf("expected room_o at top, got %s", top.State)
	}
}

func TestStack_Clear(t *testing.T) {
	s := history.New("main")
	s.Push("room_a", nil)
	s.Push("room_b", nil)
	s.Clear()
	if s.Len() != 0 {
		t.Fatalf("expected empty stack after Clear, got %d", s.Len())
	}
}

func TestWorldRoundTrip(t *testing.T) {
	s := history.New("main")
	s.Push("room_a", map[string]any{"x": "1"})
	s.Push("room_b", nil)

	w := world.New()
	w = history.ToWorld(s, w)

	s2 := history.FromWorld(w, "main")
	if s2.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", s2.Len())
	}

	top, ok := s2.Peek()
	if !ok {
		t.Fatal("expected non-empty peek after round-trip")
	}
	if top.State != "room_b" {
		t.Fatalf("expected room_b at top, got %s", top.State)
	}

	s2.Pop() // pop room_b
	bottom, ok := s2.Peek()
	if !ok {
		t.Fatal("expected room_a after popping room_b")
	}
	if bottom.State != "room_a" {
		t.Fatalf("expected room_a, got %s", bottom.State)
	}
	if bottom.Slots["x"] != "1" {
		t.Fatalf("expected slot x=1, got %v", bottom.Slots)
	}
}
