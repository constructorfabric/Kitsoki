package agents

import "testing"

// TestBuiltinNames asserts that BuiltinNames() returns the same set of
// names that NewBuiltins() registers, so the loader can use it as a
// trustworthy "is this a builtin?" agent without instantiating a registry.
func TestBuiltinNames(t *testing.T) {
	names := BuiltinNames()
	if len(names) == 0 {
		t.Fatal("BuiltinNames() returned no names; expected at least story-author")
	}

	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	if !want["story-author"] {
		t.Errorf("BuiltinNames() = %v; expected to contain story-author", names)
	}

	// BuiltinNames() must agree with NewBuiltins() — every name returned
	// must actually be registered, and every registered builtin must
	// appear in the slice.
	r := NewBuiltins()
	for _, n := range names {
		if _, ok := r.Get(n); !ok {
			t.Errorf("BuiltinNames() returned %q but NewBuiltins() doesn't register it", n)
		}
	}
	for _, n := range r.List() {
		if !want[n] {
			t.Errorf("NewBuiltins() registers %q but BuiltinNames() doesn't return it", n)
		}
	}
}
