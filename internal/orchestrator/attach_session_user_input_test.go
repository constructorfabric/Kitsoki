package orchestrator_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/journal"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestAttachSession_PreservesUserInput drives a session through two free-text
// turns and asserts the view.rendered entries carry the user_input field — so
// a resumed transcript can render "> first step" / "> second step" headers
// instead of just the rendered view bodies.
//
// Regression guard for the early phase-A bug where view.rendered carried only
// view_text + state_path; resumed sessions opened with the view but no record
// of what the user had typed to get there.
func TestAttachSession_PreservesUserInput(t *testing.T) {
	const appYAML = `
app:
  id: input-preservation-test
  version: 0.1.0
world: {}
intents:
  step:
    title: "Step"
root: a
states:
  a:
    view: "A."
    on:
      step:
        - target: b
  b:
    view: "B."
    on:
      step:
        - target: c
  c:
    terminal: true
    view: "C."
`
	def, err := app.LoadBytes([]byte(appYAML))
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jw, err := journal.NewSQLiteWriter(s.DB())
	require.NoError(t, err)
	jr, err := journal.NewSQLiteReader(s.DB())
	require.NoError(t, err)

	h := &recordingHarness{intentName: "step"}
	orch1 := orchestrator.New(def, m, s, h,
		orchestrator.WithJournalWriter(jw),
		orchestrator.WithJournalReader(jr),
	)

	ctx := context.Background()
	sid, err := orch1.NewSession(ctx)
	require.NoError(t, err)

	// Two free-text turns.  The text the user types must survive into the
	// resumed transcript — that's the whole point of view.rendered.user_input.
	_, err = orch1.Turn(ctx, sid, "first step")
	require.NoError(t, err)
	_, err = orch1.Turn(ctx, sid, "second step")
	require.NoError(t, err)

	// Restart: build a fresh orchestrator against the same store, attach.
	m2, err := machine.New(def)
	require.NoError(t, err)
	orch2 := orchestrator.New(def, m2, s, nil,
		orchestrator.WithJournalReader(jr),
	)

	bundle, err := orch2.AttachSession(sid)
	require.NoError(t, err)
	require.NotNil(t, bundle)

	// Collect user_input values from every view.rendered entry, in order.
	var inputs []string
	for _, e := range bundle.TranscriptEntries {
		if e.Kind != journal.KindViewRendered {
			continue
		}
		var body struct {
			ViewText  string `json:"view_text"`
			UserInput string `json:"user_input"`
		}
		require.NoError(t, json.Unmarshal(e.Body, &body),
			"view.rendered body should be JSON-decodable")
		inputs = append(inputs, body.UserInput)
	}
	require.Equal(t, []string{"first step", "second step"}, inputs,
		"view.rendered entries should carry the user inputs in order — without "+
			"this, the resumed transcript loses every '> input' header")
}

// TestAttachSession_SubmitDirectUsesIntentName verifies the SubmitDirect path
// also captures *something* for the transcript header — the intent name —
// since direct-intent submission has no free-text input.  Resumed sessions
// driven by external orchestrators (loop.py, Jira webhooks calling
// `session continue --intent <name>`) get a header like "> go" instead of
// a blank line above the view body.
func TestAttachSession_SubmitDirectUsesIntentName(t *testing.T) {
	def, err := app.Load("../../testdata/apps/cloak/app.yaml")
	require.NoError(t, err)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	jw, err := journal.NewSQLiteWriter(s.DB())
	require.NoError(t, err)
	jr, err := journal.NewSQLiteReader(s.DB())
	require.NoError(t, err)

	orch := orchestrator.New(def, m, s, nil,
		orchestrator.WithJournalWriter(jw),
		orchestrator.WithJournalReader(jr),
	)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	_, err = orch.SubmitDirect(ctx, sid, "go", map[string]any{"direction": "south"})
	require.NoError(t, err)

	bundle, err := orch.AttachSession(sid)
	require.NoError(t, err)

	var found bool
	for _, e := range bundle.TranscriptEntries {
		if e.Kind != journal.KindViewRendered {
			continue
		}
		var body struct {
			UserInput string `json:"user_input"`
		}
		require.NoError(t, json.Unmarshal(e.Body, &body))
		if body.UserInput == "go" {
			found = true
			break
		}
	}
	require.True(t, found,
		"view.rendered.user_input should carry the intent name for SubmitDirect turns")
}
