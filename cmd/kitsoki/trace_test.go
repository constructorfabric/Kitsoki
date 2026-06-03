package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cannedTrace is a synthetic EventSink JSONL trace (two turns).
// Each line is a store.Event in the traceEvent shape.
const cannedTrace = `{"kind":"session.header","schema_version":1,"written_at":"2026-04-21T13:02:44.000Z"}
{"turn":1,"seq":0,"ts":"2026-04-21T13:02:45.123Z","kind":"turn.start","state_path":"foyer","payload":{"input":"go south"}}
{"turn":1,"seq":1,"ts":"2026-04-21T13:02:45.200Z","kind":"turn.input","state_path":"foyer","payload":{"input":"go south","intent":""}}
{"turn":1,"seq":2,"ts":"2026-04-21T13:02:45.388Z","kind":"machine.transition","state_path":"foyer","payload":{"from":"foyer","to":"bar.dark","intent":"go"}}
{"turn":1,"seq":3,"ts":"2026-04-21T13:02:45.389Z","kind":"world.update","state_path":"foyer","payload":{"key":"disturbance","before":0,"after":1}}
{"turn":1,"seq":4,"ts":"2026-04-21T13:02:45.390Z","kind":"machine.state_exited","state_path":"foyer","payload":{"state":"foyer"}}
{"turn":1,"seq":5,"ts":"2026-04-21T13:02:45.391Z","kind":"machine.state_entered","state_path":"bar.dark","payload":{"state":"bar.dark"}}
{"turn":1,"seq":6,"ts":"2026-04-21T13:02:45.400Z","kind":"turn.end","state_path":"bar.dark","payload":{"outcome":"transitioned","to":"bar.dark"}}
{"turn":2,"seq":0,"ts":"2026-04-21T13:02:50.000Z","kind":"turn.start","state_path":"bar.dark","payload":{"input":"read message"}}
{"turn":2,"seq":1,"ts":"2026-04-21T13:02:50.500Z","kind":"turn.end","state_path":"bar.dark","payload":{"outcome":"transitioned"}}
`

// digestTrace exercises the --turns digest: routing provenance, the dispatched
// oracle prompt, an ide.context_captured datapoint, and the outcome.
const digestTrace = `{"turn":0,"kind":"session.story","payload":{"app_id":"x"}}
{"turn":1,"kind":"turn.start","state_path":"core.proposal","payload":{"input":"use this doc","routed_by":"default","match_type":"free_text"}}
{"turn":1,"kind":"ide.context_captured","state_path":"core.proposal","payload":{"connected":true,"source":"selection","file":"/repo/a.go","injected":true}}
{"turn":1,"kind":"harness.called","state_path":"core.proposal","payload":{"namespace":"host.oracle.converse"}}
{"turn":1,"kind":"oracle.call.start","state_path":"core.proposal","payload":{"verb":"converse","prompt":"use this doc\n\n## Active editor selection (via /ide)"}}
{"turn":1,"kind":"turn.end","state_path":"core.proposal","payload":{"outcome":"transitioned","to":"core.proposal"}}
`

func TestDigestTurns(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, digestTurns(strings.NewReader(digestTrace), &buf))
	out := buf.String()

	assert.NotContains(t, out, "T0", "bookkeeping-only turn 0 is suppressed")
	assert.Contains(t, out, "use this doc")
	assert.Contains(t, out, "route=default (free_text)")
	assert.Contains(t, out, "source=selection")
	assert.Contains(t, out, "injected=true")
	assert.Contains(t, out, "host.oracle.converse")
	assert.Contains(t, out, "## Active editor selection (via /ide)")
	assert.Contains(t, out, "transitioned → core.proposal")
}

func TestDigestTurns_SurfacesErrors(t *testing.T) {
	const tr = `{"turn":1,"kind":"turn.start","state_path":"implementing","payload":{"input":"go","routed_by":"deterministic"}}
{"turn":1,"kind":"host.on_error.redirect","state_path":"implementing","payload":{"from":"implementing","to":"idle"}}
{"turn":1,"kind":"machine.error","state_path":"implementing","payload":{"error":"git.commit: nothing to commit"}}
`
	var buf bytes.Buffer
	require.NoError(t, digestTurns(strings.NewReader(tr), &buf))
	out := buf.String()
	assert.Contains(t, out, "on_error → idle")
	assert.Contains(t, out, "git.commit: nothing to commit")
}

// TestPrettyPrintStructure feeds the canned JSONL and checks the output structure.
func TestPrettyPrintStructure(t *testing.T) {
	r := strings.NewReader(cannedTrace)
	var buf bytes.Buffer

	err := prettyPrint(r, &buf)
	require.NoError(t, err)

	out := buf.String()
	t.Logf("pretty output:\n%s", out)

	// Must have at least two "START" sections (one per turn.start).
	turnStartCount := strings.Count(out, "START")
	assert.GreaterOrEqual(t, turnStartCount, 2, "expected at least 2 TURN START sections")

	// Should contain MACHINE section.
	assert.Contains(t, out, "MACHINE", "expected MACHINE label")

	// Sessions should be separated by a blank line.
	assert.Contains(t, out, "\n\n", "expected blank line between turns")
}

// TestPrettyPrintEmptyInput produces no output (no error) for empty input.
func TestPrettyPrintEmptyInput(t *testing.T) {
	r := strings.NewReader("")
	var buf bytes.Buffer
	err := prettyPrint(r, &buf)
	require.NoError(t, err)
	assert.Empty(t, buf.String())
}

// TestPrettyPrintInvalidJSON falls back to raw line on bad JSON.
func TestPrettyPrintInvalidJSON(t *testing.T) {
	r := strings.NewReader("not json at all\n")
	var buf bytes.Buffer
	err := prettyPrint(r, &buf)
	require.NoError(t, err)
	// Raw line should be echoed.
	assert.Contains(t, buf.String(), "not json at all")
}
