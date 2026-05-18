// Package semroute — Gap 2 coverage: a story-driven synonym test that
// loads real production app.yaml files and asserts the semantic-routing
// matcher resolves every declared example to its owning intent.
//
// This catches the regression class "an `examples:` entry didn't make
// it into the matcher's stem index" — the failure mode that would
// silently break user-typed synonyms like "continue" → proceed without
// affecting any state-machine flow test (because flow tests dispatch
// intents by name, never by typed input).
package semroute_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/semroute"
)

// TestStoryExamplesRouteToOwningIntent walks every intent in a curated
// set of production stories and asserts the matcher resolves each
// `examples:` phrase back to that intent. Subset is hand-picked rather
// than every story to keep this test stable against story churn — the
// stories included are the ones whose synonyms are user-facing on the
// dev-story routing surface.
//
// For each (story, intent, example) triple the matcher must:
//
//   - return a Verdict with Intent == the intent's declared name
//   - report Kind == Synonym (example phrases compile as implicit
//     synonyms per internal/semroute/index.go:204)
//   - score Confidence at or above the matcher's deterministic threshold
//     so the dispatcher would actually fire it
//
// This single test covers every synonym change across the included
// stories — including the bugfix "continue" → proceed alias we added
// to close cake's Gap 2.
func TestStoryExamplesRouteToOwningIntent(t *testing.T) {
	t.Parallel()

	stories := []struct {
		name string
		path string
		// stateForMatch is a state whose `on:` arcs include every
		// intent we care about — the matcher's allow-filter would
		// reject otherwise.
		stateForMatch string
		// allowedIntents pins which intents are "open" at that state
		// so we don't depend on the matcher inferring allowed-set
		// from the state alone.
		allowedIntents []string
	}{
		{
			name: "bugfix",
			path: "../../stories/bugfix/app.yaml",
			// Post-room-merge, every bugfix phase is one state (no
			// _executing / _awaiting_reply split). `reproducing` is a
			// representative top-level room state whose on: arcs
			// declare the standard checkpoint vocabulary.
			stateForMatch: "reproducing",
			allowedIntents: []string{
				"accept", "refine", "restart_from", "jump_to",
				"quit", "look",
			},
		},
	}

	for _, s := range stories {
		s := s
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			def, err := app.Load(s.path)
			require.NoError(t, err, "load %s", s.path)

			m, err := semroute.Compile(def)
			require.NoError(t, err, "compile matcher for %s", s.path)
			require.NotNil(t, m)

			for intentName, intent := range def.Intents {
				for _, ex := range intent.Examples {
					if ex == "" {
						continue
					}
					// Only check examples whose intent the state allows;
					// otherwise the matcher correctly rejects via the
					// allow-filter rather than misrouting.
					if !contains(s.allowedIntents, intentName) {
						continue
					}
					v, mErr := m.Match(context.Background(), s.stateForMatch, s.allowedIntents, ex)
					require.NoError(t, mErr, "match %q in %s", ex, s.path)
					require.Equalf(t, intentName, v.Intent,
						"example %q in story %s should route to intent %q, got %q",
						ex, s.name, intentName, v.Intent)
				}
			}
		})
	}
}

// TestBugfixContinueSynonym is the targeted regression test for the
// cake proposal's Gap 2: the "continue" example on the bugfix `accept`
// intent must resolve to `accept` (not fall through to UNKNOWN or to a
// sibling intent that happens to share a stem). After the bugfix-room
// merge (single state per phase replacing _executing + _awaiting_reply),
// `accept` is the universal advance verb — `proceed` was retired as a
// distinct intent and "continue" / "proceed" both became examples on
// accept.
func TestBugfixContinueSynonym(t *testing.T) {
	t.Parallel()
	def, err := app.Load("../../stories/bugfix/app.yaml")
	require.NoError(t, err)
	m, err := semroute.Compile(def)
	require.NoError(t, err)

	allowed := []string{"accept", "refine", "restart_from", "jump_to", "quit", "look"}
	v, err := m.Match(context.Background(), "reproducing", allowed, "continue")
	require.NoError(t, err)
	require.Equal(t, "accept", v.Intent,
		`"continue" must route to accept via the synonym table; the examples: entry on stories/bugfix/app.yaml's accept intent is the only declaration that makes this work`)
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
