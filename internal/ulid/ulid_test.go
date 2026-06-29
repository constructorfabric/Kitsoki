package ulid_test

import (
	"errors"
	"strings"
	"testing"

	"kitsoki/internal/ulid"
)

func TestNew_PanicsOnRandFailure(t *testing.T) {
	// readRandom is a package global; do not run in parallel with other
	// ulid.New callers. Restore it no matter how the test exits.
	restore := ulid.SetReadRandom(func([]byte) (int, error) {
		return 0, errors.New("boom")
	})
	t.Cleanup(restore)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected ulid.New to panic when rand.Read fails")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("expected panic value to be a string, got %T: %v", r, r)
		}
		if !strings.HasPrefix(msg, "ulid: rand.Read:") {
			t.Fatalf("unexpected panic message: %q", msg)
		}
	}()

	ulid.New()
}

func TestNew_Length(t *testing.T) {
	id := ulid.New()
	if len(id) != 26 {
		t.Fatalf("expected 26 chars, got %d: %q", len(id), id)
	}
}

func TestNew_Unique(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for i := 0; i < 1000; i++ {
		id := ulid.New()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ULID generated: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestNew_Valid(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := ulid.New()
		if !ulid.IsValid(id) {
			t.Fatalf("generated invalid ULID: %q", id)
		}
	}
}

func TestIsValid(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"01ARZ3NDEKTSV4RRFFQ69G5FAV", true},
		{"01ARZ3NDEKTSV4RRFFQ69G5FA", false},   // too short
		{"01ARZ3NDEKTSV4RRFFQ69G5FAVI", false}, // too long
		{"", false},
	}
	for _, tc := range cases {
		if got := ulid.IsValid(tc.s); got != tc.want {
			t.Errorf("IsValid(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}
