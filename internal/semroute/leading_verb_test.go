package semroute_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/semroute"
)

// TestLeadingVerbTieBreak covers the leading-verb tie-break in the bare
// matcher (matcher.go matchBare default branch). Bag-of-stems subset
// matching ignores word order, so an imperative whose object happens to
// contain another intent's verb stem produces a 0.50 tie. The tie-break
// recovers the command from the leading content stem: when the input's
// first content stem belongs to exactly one tied candidate's matched
// entry, that candidate wins at the whole-synonym band; otherwise the
// tie stands (genuine ambiguity → disambiguation card).
//
// The canonical case is git-ops "commit the staged fix": commit (via
// "commit") ties stage (via "staged"→stage) under bag-of-stems, but
// "commit" leads, so it resolves to commit.
func TestLeadingVerbTieBreak(t *testing.T) {
	t.Parallel()

	def, err := app.Load("../../stories/git-ops/app.yaml")
	require.NoError(t, err)
	m, err := semroute.Compile(def)
	require.NoError(t, err)

	// branch_ops opens both commit and stage — the pair that ties.
	branch := []string{"commit", "rebase", "merge_into_main", "squash", "stage",
		"worktree_create", "worktree_list", "cleanup", "undo", "pull"}
	main := []string{"worktree_create", "worktree_list", "cleanup", "pull", "merge_branch"}

	cases := []struct {
		name      string
		utterance string
		state     string
		allowed   []string
		want      string // intent; "" means expect a tie (no resolution)
		wantTie   bool
	}{
		// The four mined real-session utterances must all route deterministically.
		{"commit-leading", "commit the staged fix", "branch_ops", branch, "commit", false},
		{"commit-amend-phrasing", "commit the staged work", "branch_ops", branch, "commit", false},
		{"rebase", "rebase onto main and resolve the conflicts", "branch_ops", branch, "rebase", false},
		{"merge", "merge the feature branch into main", "branch_ops", branch, "merge_into_main", false},
		{"worktree", "set up a worktree for the new cache feature", "main_ops", main, "worktree_create", false},

		// Symmetry: when "stage" leads, the tie resolves to stage, not commit.
		{"stage-leading", "stage the commit", "branch_ops", branch, "stage", false},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v, err := m.Match(context.Background(), c.state, c.allowed, c.utterance)
			require.NoError(t, err)
			if c.wantTie {
				require.Equal(t, semroute.ConfidenceTie, v.Confidence,
					"%q should remain a tie", c.utterance)
				require.Empty(t, v.Intent)
				return
			}
			require.Equalf(t, c.want, v.Intent,
				"%q should route to %q, got %q (conf %.2f, reason %q)",
				c.utterance, c.want, v.Intent, v.Confidence, v.MatchReason)
			require.Equal(t, semroute.ConfidenceWholeSynonym, v.Confidence,
				"%q should resolve at the whole-synonym band", c.utterance)
		})
	}
}
