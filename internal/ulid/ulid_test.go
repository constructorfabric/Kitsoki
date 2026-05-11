package ulid_test

import (
	"testing"

	"kitsoki/internal/ulid"
)

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
		{"01ARZ3NDEKTSV4RRFFQ69G5FA", false},  // too short
		{"01ARZ3NDEKTSV4RRFFQ69G5FAVI", false}, // too long
		{"", false},
	}
	for _, tc := range cases {
		if got := ulid.IsValid(tc.s); got != tc.want {
			t.Errorf("IsValid(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}
