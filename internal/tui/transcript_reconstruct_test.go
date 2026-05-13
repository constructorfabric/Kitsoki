package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/journal"
)

// TestTranscript_ReconstructUsesUserInput is the TUI-side half of the
// "preserve user input on resume" guarantee.  It feeds the transcript
// model a view.rendered journal entry whose body carries user_input, and
// asserts the rendered transcript content includes the "> input" header
// — the visible difference between AppendTurn and AppendSystem.
//
// Pair with internal/orchestrator/attach_session_user_input_test.go which
// asserts the journal write side captures user_input.
func TestTranscript_ReconstructUsesUserInput(t *testing.T) {
	m := newTranscriptModel(80, 20)

	body, err := json.Marshal(map[string]any{
		"view_text":  "You are in the foyer.",
		"state_path": "foyer",
		"user_input": "look around",
	})
	require.NoError(t, err)

	entries := []journal.Entry{
		{
			Ts:    time.Now(),
			Turn:  1,
			Seq:   0,
			Kind:  journal.KindViewRendered,
			Body:  body,
		},
	}

	m.ReconstructFromEntries(entries)

	content := m.AllContent()
	require.Contains(t, content, "> look around",
		"resumed transcript must show the user input header — without this, "+
			"the user can't tell what they typed before the restart")
	require.Contains(t, content, "You are in the foyer.",
		"resumed transcript must show the rendered view body")
}

// TestTranscript_ReconstructFallsBackToSystemForEmptyInput verifies the
// synthetic-turn path (bg-job completion, timeout): when user_input is empty
// the view body is appended via AppendSystem (no "> " header), matching how
// the live TUI displays those turns.
func TestTranscript_ReconstructFallsBackToSystemForEmptyInput(t *testing.T) {
	m := newTranscriptModel(80, 20)

	body, err := json.Marshal(map[string]any{
		"view_text":  "A wolf attacks the wagon!",
		"state_path": "trail",
		"user_input": "",
	})
	require.NoError(t, err)

	entries := []journal.Entry{
		{
			Ts:    time.Now(),
			Turn:  5,
			Seq:   0,
			Kind:  journal.KindViewRendered,
			Body:  body,
		},
	}

	m.ReconstructFromEntries(entries)

	content := m.AllContent()
	require.NotContains(t, content, "> ",
		"synthetic-turn view.rendered (empty user_input) must not render a '> ' header")
	require.Contains(t, content, "A wolf attacks the wagon!",
		"synthetic view body must still render")
}

// TestTranscript_ReconstructPreservesOrder asserts ordered playback across
// a multi-turn sequence — the resumed transcript reads top-to-bottom in
// the same order the live one did.
func TestTranscript_ReconstructPreservesOrder(t *testing.T) {
	m := newTranscriptModel(80, 20)

	mkEntry := func(seq int, input, view string) journal.Entry {
		body, _ := json.Marshal(map[string]any{
			"view_text":  view,
			"user_input": input,
		})
		return journal.Entry{
			Ts:   time.Now(),
			Turn: 1,
			Seq:  seq,
			Kind: journal.KindViewRendered,
			Body: body,
		}
	}

	m.ReconstructFromEntries([]journal.Entry{
		mkEntry(0, "north", "You go north."),
		mkEntry(1, "east", "You go east."),
		mkEntry(2, "look", "An empty room."),
	})

	content := m.AllContent()
	require.Equal(t, 3,
		strings.Count(content, "> "),
		"expected three '> input' headers — one per turn")

	// Order: north must appear before east which must appear before look.
	iNorth := strings.Index(content, "> north")
	iEast := strings.Index(content, "> east")
	iLook := strings.Index(content, "> look")
	require.True(t, iNorth >= 0 && iEast > iNorth && iLook > iEast,
		"transcript entries must appear in turn order; got positions north=%d east=%d look=%d",
		iNorth, iEast, iLook)
}
