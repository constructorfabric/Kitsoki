package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
// agent prompt, an ide.context_captured datapoint, and the outcome.
const digestTrace = `{"turn":0,"kind":"session.story","payload":{"app_id":"x"}}
{"turn":1,"kind":"turn.start","state_path":"core.proposal","payload":{"input":"use this doc","routed_by":"default","match_type":"free_text"}}
{"turn":1,"kind":"ide.context_captured","state_path":"core.proposal","payload":{"connected":true,"source":"selection","file":"/repo/a.go","injected":true}}
{"turn":1,"kind":"harness.called","state_path":"core.proposal","payload":{"namespace":"host.agent.converse"}}
{"turn":1,"kind":"agent.call.start","state_path":"core.proposal","payload":{"verb":"converse","prompt":"use this doc\n\n## Active editor selection (via /ide)"}}
{"turn":1,"kind":"turn.end","state_path":"core.proposal","payload":{"outcome":"transitioned","to":"core.proposal"}}
`

func TestDigestTurns(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, digestTurns(strings.NewReader(digestTrace), &buf, 0))
	out := buf.String()

	assert.NotContains(t, out, "T0", "bookkeeping-only turn 0 is suppressed")
	assert.Contains(t, out, "use this doc")
	assert.Contains(t, out, "route=default (free_text)")
	assert.Contains(t, out, "source=selection")
	assert.Contains(t, out, "injected=true")
	assert.Contains(t, out, "host.agent.converse")
	assert.Contains(t, out, "## Active editor selection (via /ide)")
	assert.Contains(t, out, "transitioned → core.proposal")
}

func TestTraceRuntimeContractCmd(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	require.NoError(t, os.WriteFile(trace, []byte("{\"kind\":\"agent.runtime.start\",\"call_id\":\"live\"}\n"), 0o600))
	cmd := traceRuntimeContractCmd()
	cmd.SetArgs([]string{trace})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "runtime lifecycle contract failed")
	assert.Contains(t, stderr.String(), "runtime start has no matching end")
}

func TestTraceSequenceContractCmd(t *testing.T) {
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	require.NoError(t, os.WriteFile(trace, []byte(
		"{\"kind\":\"session.header\",\"schema_version\":1}\n"+
			"{\"turn\":1,\"seq\":0,\"kind\":\"turn.start\",\"payload\":{}}\n"+
			"{\"turn\":1,\"seq\":2,\"kind\":\"turn.end\",\"payload\":{}}\n",
	), 0o600))
	cmd := traceSequenceContractCmd()
	cmd.SetArgs([]string{trace})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trace sequence contract failed")
	assert.Contains(t, err.Error(), "gap in seq")
}

func TestDigestTurns_SurfacesErrors(t *testing.T) {
	const tr = `{"turn":1,"kind":"turn.start","state_path":"implementing","payload":{"input":"go","routed_by":"deterministic"}}
{"turn":1,"kind":"host.on_error.redirect","state_path":"implementing","payload":{"from":"implementing","to":"idle"}}
{"turn":1,"kind":"machine.error","state_path":"implementing","payload":{"error":"git.commit: nothing to commit"}}
`
	var buf bytes.Buffer
	require.NoError(t, digestTurns(strings.NewReader(tr), &buf, 0))
	out := buf.String()
	assert.Contains(t, out, "on_error → idle")
	assert.Contains(t, out, "git.commit: nothing to commit")
}

// hostIOTrace mirrors the real slidey-edit "edits never show" session: a
// host.starlark.run call whose `inputs:` reached the script as UNEVALUATED expr
// strings (the bare-expr bug), so resolve_scene looked up a file literally named
// "world.deck.spec_path" (fs miss → scene_index -1) and the gate received a
// string scene_index → a type error. The digest must surface the resolved inputs,
// the returned outputs, and the fs inspection so the cause is visible without jq.
const hostIOTrace = `{"turn":1,"kind":"turn.start","state_path":"refining","payload":{"input":"make it pop","routed_by":"llm"}}
{"turn":1,"kind":"harness.called","state_path":"refining","payload":{"namespace":"host.starlark.run","args":{"call":"resolve_scene","inputs":{"spec_path":"world.deck.spec_path","current_scene":"str(world.current_scene ?? \"\")"}}}}
{"turn":1,"kind":"harness.returned","state_path":"refining","payload":{"namespace":"host.starlark.run","data":{"scene_index":-1,"scene_label":"(deck not found)","__inspections":[{"op":"exists","target":"world.deck.spec_path","status":"missing"}]}}}
{"turn":1,"kind":"harness.called","state_path":"refining","payload":{"namespace":"host.starlark.run","args":{"call":"gate_edited_scene","inputs":{"scene_index":"world.scene_index"}}}}
{"turn":1,"kind":"harness.returned","state_path":"refining","payload":{"namespace":"host.starlark.run","error":"host.starlark.run: input \"scene_index\": expected int, got string"}}
{"turn":1,"kind":"turn.end","state_path":"refining","payload":{"outcome":"transitioned","to":"reviewing"}}
`

func TestDigestTurns_SurfacesHostInputsAndOutputs(t *testing.T) {
	// Compact --turns view: the call id, an inputs summary, the output summary,
	// the fs inspection, and the error all appear.
	var compact bytes.Buffer
	require.NoError(t, digestTurns(strings.NewReader(hostIOTrace), &compact, 0))
	c := compact.String()
	assert.Contains(t, c, "host.starlark.run [resolve_scene]", "names the call site")
	assert.Contains(t, c, "in=", "resolved inputs are surfaced (truncated in compact)")
	assert.Contains(t, c, "scene_index=-1", "the returned output is visible")
	assert.Contains(t, c, "exists world.deck.spec_path→missing", "the fs inspection (the smoking gun, untruncated) is visible")
	assert.Contains(t, c, `expected int, got string`, "the gate error is visible")

	// Focused --turn view: inputs/outputs print one line per key (100% detail),
	// paired to the right call (resolve_scene's data vs the gate's error).
	var full bytes.Buffer
	require.NoError(t, digestTurns(strings.NewReader(hostIOTrace), &full, 1))
	f := full.String()
	assert.Contains(t, f, "in   spec_path: world.deck.spec_path")
	assert.Contains(t, f, "out  scene_label: (deck not found)")
	assert.Contains(t, f, "fs   exists world.deck.spec_path→missing")
	// The error is paired to the SECOND host.starlark.run call (the gate), not the
	// first (resolve_scene returned data) — proves FIFO-by-namespace pairing.
	assert.Contains(t, f, "in   scene_index: world.scene_index")
	assert.Contains(t, f, `err  host.starlark.run: input "scene_index": expected int, got string`)
}

func TestDigestTurns_FocusShowsFullPrompt(t *testing.T) {
	// A long prompt that the default (truncated) view would cut off.
	long := "do the thing\\n\\n## Active editor selection (via /ide)\\n\\n" + strings.Repeat("x", 400)
	tr := `{"turn":1,"kind":"turn.start","payload":{"input":"a"}}
{"turn":2,"kind":"turn.start","state_path":"chat","payload":{"input":"do the thing","routed_by":"default"}}
{"turn":2,"kind":"agent.call.start","payload":{"verb":"converse","prompt":"` + long + `"}}
`
	var buf bytes.Buffer
	require.NoError(t, digestTurns(strings.NewReader(tr), &buf, 2))
	out := buf.String()
	assert.Contains(t, out, "T2", "focused turn is shown")
	assert.NotContains(t, out, "T1", "other turns are omitted when focused")
	assert.Contains(t, out, strings.Repeat("x", 400), "focused view prints the prompt in full (no truncation)")
}

func TestResolveTraceArg(t *testing.T) {
	root := t.TempDir()
	mk := func(app, name string, mod time.Time) string {
		dir := filepath.Join(root, app)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("{}\n"), 0o644))
		require.NoError(t, os.Chtimes(p, mod, mod))
		return p
	}
	base := time.Now().Add(-time.Hour)
	mk("appA", "1111-old.jsonl", base)
	newest := mk("kitsoki-dev", "7ca57b33-tui-x.jsonl", base.Add(time.Minute))
	ticketTrace := mk("kitsoki", "308b5d05-tui-x.jsonl", base.Add(2*time.Minute))
	require.NoError(t, os.WriteFile(ticketTrace, []byte(`{"turn":2,"kind":"world.update","payload":{"set":{"root__bf__ticket_id":"64","root__ticket_id":"64"}}}`+"\n"), 0o644))
	require.NoError(t, os.Chtimes(ticketTrace, base.Add(2*time.Minute), base.Add(2*time.Minute)))

	t.Run("stdin passthrough", func(t *testing.T) {
		got, err := resolveTraceArg(root, "-", "")
		require.NoError(t, err)
		assert.Equal(t, "-", got)
	})
	t.Run("explicit path wins", func(t *testing.T) {
		got, err := resolveTraceArg(root, newest, "")
		require.NoError(t, err)
		assert.Equal(t, newest, got)
	})
	t.Run("substring matches by filename", func(t *testing.T) {
		got, err := resolveTraceArg(root, "7ca57b33", "")
		require.NoError(t, err)
		assert.Equal(t, newest, got)
	})
	t.Run("app filter restricts the search", func(t *testing.T) {
		got, err := resolveTraceArg(root, "", "appA")
		require.NoError(t, err)
		assert.Contains(t, got, "appA")
	})
	t.Run("ticket filter matches qualified world ticket id", func(t *testing.T) {
		got, err := resolveTraceArgWithOptions(root, "", traceResolveOptions{TicketID: "64"})
		require.NoError(t, err)
		assert.Equal(t, ticketTrace, got)
	})
	t.Run("no match is a clear error", func(t *testing.T) {
		_, err := resolveTraceArg(root, "nope-nothing", "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no session trace found")
	})
	t.Run("ticket no match is a clear error", func(t *testing.T) {
		_, err := resolveTraceArgWithOptions(root, "", traceResolveOptions{TicketID: "65"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `ticket_id "65"`)
	})
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
