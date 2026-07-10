package host_test

// effect_classification_test.go proves the effect-taxonomy builtin
// classification table (internal/effect's ClassifyVerb) covers every verb
// RegisterBuiltins actually registers — the coverage acceptance criterion
// from docs/proposals/effect-taxonomy.md: "a coverage test fails if a new
// verb ships unclassified."

import (
	"testing"

	"kitsoki/internal/effect"
	"kitsoki/internal/host"
)

// TestBuiltinVerbClassificationCovers walks every verb name
// host.RegisterBuiltins registers into a fresh Registry and asserts each one
// has an entry in internal/effect's builtin classification table. A future
// verb added to RegisterBuiltins without a matching classification fails
// this test rather than silently shipping unclassified.
func TestBuiltinVerbClassificationCovers(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)

	classified := effect.RegisteredVerbs()
	names := r.Names()
	if len(names) == 0 {
		t.Fatal("RegisterBuiltins registered no verbs — registry wiring broken?")
	}
	for _, name := range names {
		if !classified[name] {
			t.Errorf("verb %q is registered but has no entry in the builtin effect classification table (internal/effect/hostverbs.go)", name)
		}
	}
}

// TestClassifyDispatchedCall_StaticVerb spot-checks a handful of
// single-purpose verbs resolve to a sensible, valid class.
func TestClassifyDispatchedCall_StaticVerb(t *testing.T) {
	cases := []struct {
		namespace string
		want      effect.Effect
	}{
		{"host.chat.list", effect.Read},
		{"host.chat.create", effect.Write},
		{"host.transport.post", effect.External},
		{"host.agent.task", effect.Write},
		{"host.agent.decide", effect.Read},
	}
	for _, tc := range cases {
		got, _ := host.ClassifyDispatchedCall(tc.namespace, nil)
		if got != tc.want {
			t.Errorf("ClassifyDispatchedCall(%q) = %q, want %q", tc.namespace, got, tc.want)
		}
		if !got.Valid() {
			t.Errorf("ClassifyDispatchedCall(%q) returned invalid effect %q", tc.namespace, got)
		}
	}
}

// TestClassifyDispatchedCall_PerOpVerbs proves host.git/gh.ticket/local
// classify PER-OPERATION, not as one fixed class for the whole verb — the
// explicit acceptance criterion ("host.git/gh/local classify per-op").
func TestClassifyDispatchedCall_PerOpVerbs(t *testing.T) {
	cases := []struct {
		namespace string
		op        string
		want      effect.Effect
	}{
		{"host.git", "diff", effect.Read},
		{"host.git", "commit", effect.Write},
		{"host.git", "push", effect.External},
		{"host.git", "pr_comment", effect.External},
		{"host.gh.ticket", "search", effect.Read},
		{"host.gh.ticket", "create", effect.External},
		{"host.local_github.ticket", "search", effect.Read},
		{"host.local_github.ticket", "transition", effect.External},
		{"host.local", "remote_status", effect.Read},
		{"host.local", "run_tests", effect.Write},
	}
	for _, tc := range cases {
		got, _ := host.ClassifyDispatchedCall(tc.namespace, map[string]any{"op": tc.op})
		if got != tc.want {
			t.Errorf("ClassifyDispatchedCall(%q, op=%q) = %q, want %q", tc.namespace, tc.op, got, tc.want)
		}
	}
	// An unrecognised op on a per-op verb falls back to the verb's own
	// (conservative) default rather than panicking or returning empty.
	got, _ := host.ClassifyDispatchedCall("host.git", map[string]any{"op": "no-such-op"})
	if !got.Valid() {
		t.Errorf("ClassifyDispatchedCall with unknown op returned invalid effect %q", got)
	}
}

// TestClassifyDispatchedCall_UnknownVerbFailsClosed verifies an
// unregistered/unclassified verb name fails closed to External, never
// silently trusted.
func TestClassifyDispatchedCall_UnknownVerbFailsClosed(t *testing.T) {
	got, det := host.ClassifyDispatchedCall("host.no.such.verb", nil)
	if got != effect.External {
		t.Errorf("unknown verb classified as %q, want external (fail closed)", got)
	}
	if det {
		t.Error("unknown verb should not be classified deterministic")
	}
}
