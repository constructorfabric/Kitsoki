package testrunner

import (
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"
)

// A tiny hand-written 2-turn trace exercising the converter: an on_enter host
// call (turn 0), then two transitions whose intents differ, with a
// per-call-varying host.agent.converse handler invoked twice (different
// answers). No real LLM, no files — pure transform.
const tinyTrace = `{"kind":"session.header","schema_version":1,"written_at":"2026-06-01T00:00:00Z"}
{"turn":0,"seq":0,"ts":"2026-06-01T00:00:00.001Z","kind":"harness.returned","state_path":"idle","payload":{"namespace":"host.chat.resolve","data":{"chat_id":"chat-1","is_new":true}}}
{"turn":1,"seq":0,"ts":"2026-06-01T00:00:00.002Z","kind":"turn.input","state_path":"idle","payload":{"input":"hello there operator","intent":""}}
{"turn":1,"seq":1,"ts":"2026-06-01T00:00:00.003Z","kind":"harness.returned","state_path":"idle","payload":{"namespace":"host.agent.converse","data":{"answer":"first reply"}}}
{"turn":1,"seq":2,"ts":"2026-06-01T00:00:00.004Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"idle","intent":"discuss","slots":{"message":"hello"}}}
{"turn":2,"seq":0,"ts":"2026-06-01T00:00:00.005Z","kind":"turn.input","state_path":"idle","payload":{"input":"1 - they're an analyst","intent":""}}
{"turn":2,"seq":1,"ts":"2026-06-01T00:00:00.006Z","kind":"harness.returned","state_path":"idle","payload":{"namespace":"host.agent.converse","data":{"answer":"second reply"}}}
{"turn":2,"seq":2,"ts":"2026-06-01T00:00:00.007Z","kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"clarifying","intent":"start","slots":{}}}
`

func TestConvertTraceToFlow_MapsTransitionsAndResponses(t *testing.T) {
	t.Parallel()

	lines, err := parseTraceLines([]byte(tinyTrace))
	if err != nil {
		t.Fatalf("parseTraceLines: %v", err)
	}
	res, err := convertTraceLines(lines, ConvertOptions{
		AppPath:      "../app.yaml",
		CassettePath: "tiny.cassette.yaml",
		AppID:        "tiny",
	})
	if err != nil {
		t.Fatalf("convertTraceLines: %v", err)
	}

	// ── Flow: two turns, in order, intents + slots verbatim, NO expectations.
	if res.NumTurns != 2 {
		t.Fatalf("NumTurns = %d, want 2", res.NumTurns)
	}
	var flow flowFixtureDoc
	if err := goyaml.Unmarshal(res.FlowYAML, &flow); err != nil {
		t.Fatalf("unmarshal flow: %v\n%s", err, res.FlowYAML)
	}
	if flow.TestKind != "flow" {
		t.Errorf("test_kind = %q, want flow", flow.TestKind)
	}
	if flow.InitialState != "idle" {
		t.Errorf("initial_state = %q, want idle (FROM of first transition)", flow.InitialState)
	}
	if flow.HostCassette != "tiny.cassette.yaml" {
		t.Errorf("host_cassette = %q, want tiny.cassette.yaml", flow.HostCassette)
	}
	if len(flow.Turns) != 2 {
		t.Fatalf("flow has %d turns, want 2", len(flow.Turns))
	}
	if flow.Turns[0].Intent.Name != "discuss" {
		t.Errorf("turn[0] intent = %q, want discuss", flow.Turns[0].Intent.Name)
	}
	if got := flow.Turns[0].Intent.Slots["message"]; got != "hello" {
		t.Errorf("turn[0] slot message = %v, want hello", got)
	}
	if flow.Turns[1].Intent.Name != "start" {
		t.Errorf("turn[1] intent = %q, want start", flow.Turns[1].Intent.Name)
	}
	// The converter must carry each turn's original free-text utterance (from
	// the trace's turn.input event) onto the flow turn's display_input field so
	// replay reproduces the operator's real words, not "[intent] <name>".
	if flow.Turns[0].DisplayInput != "hello there operator" {
		t.Errorf("turn[0] display_input = %q, want %q", flow.Turns[0].DisplayInput, "hello there operator")
	}
	if flow.Turns[1].DisplayInput != "1 - they're an analyst" {
		t.Errorf("turn[1] display_input = %q, want %q", flow.Turns[1].DisplayInput, "1 - they're an analyst")
	}
	// And it must be emitted in the rendered YAML under display_input:.
	if !strings.Contains(stripComments(res.FlowYAML), "display_input: hello there operator") {
		t.Errorf("flow YAML must carry display_input; got:\n%s", res.FlowYAML)
	}
	// The converter must NOT emit expect_state on turns (story-drift policy).
	if strings.Contains(stripComments(res.FlowYAML), "expect_state") {
		t.Errorf("flow must not emit expect_state; got:\n%s", res.FlowYAML)
	}

	// ── Cassette: one episode per host.* call (3), in trace order, matched on
	// handler, NOT replay:any, with per-call-distinct response data.
	if res.NumEpisodes != 3 {
		t.Fatalf("NumEpisodes = %d, want 3", res.NumEpisodes)
	}
	var cas cassetteDoc
	if err := goyaml.Unmarshal(res.CassetteYAML, &cas); err != nil {
		t.Fatalf("unmarshal cassette: %v\n%s", err, res.CassetteYAML)
	}
	if cas.Kind != "host_cassette" {
		t.Errorf("cassette kind = %q, want host_cassette", cas.Kind)
	}
	if len(cas.Episodes) != 3 {
		t.Fatalf("cassette has %d episodes, want 3", len(cas.Episodes))
	}
	// Episode 0: the on_enter chat.resolve.
	if got := cas.Episodes[0].Match["handler"]; got != "host.chat.resolve" {
		t.Errorf("episode[0] handler = %v, want host.chat.resolve", got)
	}
	if got := cas.Episodes[0].Response.Data["chat_id"]; got != "chat-1" {
		t.Errorf("episode[0] chat_id = %v, want chat-1", got)
	}
	// Episodes 1 and 2: both host.agent.converse, distinct answers, in order.
	if got := cas.Episodes[1].Match["handler"]; got != "host.agent.converse" {
		t.Errorf("episode[1] handler = %v, want host.agent.converse", got)
	}
	if got := cas.Episodes[1].Response.Data["answer"]; got != "first reply" {
		t.Errorf("episode[1] answer = %v, want 'first reply'", got)
	}
	if got := cas.Episodes[2].Response.Data["answer"]; got != "second reply" {
		t.Errorf("episode[2] answer = %v, want 'second reply'", got)
	}
	// Episode IDs must be unique (otherwise ordered consumption is ambiguous).
	if cas.Episodes[1].ID == cas.Episodes[2].ID {
		t.Errorf("episodes 1 and 2 share id %q; ordered replay needs distinct ids", cas.Episodes[1].ID)
	}
	// No replay:any — ordered consumption is the whole point. The write-side
	// doc has no Replay field, so the rendered YAML must never set it.
	if strings.Contains(stripComments(res.CassetteYAML), "replay:") {
		t.Errorf("cassette must not set replay:; got:\n%s", res.CassetteYAML)
	}
}

func TestConvertTraceToFlow_SkipsSyntheticAndBackgroundTransitions(t *testing.T) {
	t.Parallel()

	const trace = `{"kind":"session.header","schema_version":1,"written_at":"2026-07-06T00:00:00Z"}
{"turn":1,"seq":0,"kind":"turn.input","state_path":"idle","payload":{"input":"","intent":"start"}}
{"turn":1,"seq":1,"kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"load","intent":"start","slots":{}}}
{"turn":2,"seq":0,"kind":"turn.input","state_path":"load","payload":{"input":"","intent":"next_item"}}
{"turn":2,"seq":1,"kind":"machine.transition","state_path":"load","payload":{"from":"load","to":"board","intent":"next_item","slots":{}}}
{"turn":3,"seq":0,"kind":"turn.input","state_path":"board","payload":{"input":"","intent":"next_item"}}
{"turn":3,"seq":1,"kind":"machine.transition","state_path":"board","payload":{"from":"board","to":"policy_check","intent":"next_item","slots":{}}}
{"turn":3,"seq":2,"kind":"machine.transition","state_path":"board","payload":{"from":"policy_check","to":"drive","intent":"policy_ok","slots":{},"synthetic":true}}
{"turn":4,"seq":0,"kind":"machine.transition","payload":{"from":"drive","to":"needs_human","intent":"__on_complete_target__","slots":{}}}
`
	lines, err := parseTraceLines([]byte(trace))
	if err != nil {
		t.Fatalf("parseTraceLines: %v", err)
	}
	res, err := convertTraceLines(lines, ConvertOptions{
		AppPath: "../app.yaml",
	})
	if err != nil {
		t.Fatalf("convertTraceLines: %v", err)
	}

	var flow flowFixtureDoc
	if err := goyaml.Unmarshal(res.FlowYAML, &flow); err != nil {
		t.Fatalf("unmarshal flow: %v\n%s", err, res.FlowYAML)
	}
	if res.NumTurns != 3 {
		t.Fatalf("NumTurns = %d, want 3; flow:\n%s", res.NumTurns, res.FlowYAML)
	}
	got := []string{}
	for _, turn := range flow.Turns {
		got = append(got, turn.Intent.Name)
	}
	want := []string{"start", "next_item", "next_item"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("turn intents = %v, want %v", got, want)
	}
	body := stripComments(res.FlowYAML)
	if strings.Contains(body, "policy_ok") || strings.Contains(body, "__on_complete_target__") {
		t.Fatalf("flow must not emit internal transitions; got:\n%s", res.FlowYAML)
	}
}

// stripComments removes whole-line YAML comments so body assertions don't trip
// on words that legitimately appear in the generated header.
func stripComments(b []byte) string {
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func TestConvertTraceToFlow_NoTransitionsIsError(t *testing.T) {
	t.Parallel()
	lines, err := parseTraceLines([]byte(`{"kind":"session.header","schema_version":1,"written_at":"2026-06-01T00:00:00Z"}
{"turn":1,"seq":0,"ts":"2026-06-01T00:00:00.002Z","kind":"turn.start","state_path":"idle","payload":{}}
`))
	if err != nil {
		t.Fatalf("parseTraceLines: %v", err)
	}
	if _, err := convertTraceLines(lines, ConvertOptions{AppPath: "../app.yaml"}); err == nil {
		t.Fatal("expected error for a trace with no machine.transition events")
	}
}
