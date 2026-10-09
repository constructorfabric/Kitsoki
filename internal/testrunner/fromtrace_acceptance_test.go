package testrunner

import (
	"strings"
	"testing"

	goyaml "github.com/goccy/go-yaml"
)

// A tiny 2-turn trace for the acceptance-draft derivation: two host handlers
// (one invoked twice — required entries must be UNIQUE per handler), two
// operator transitions, and a trailing synthetic transition whose target is
// the session's true final state (the draft must pin THAT, not the last
// operator-driven target).
const acceptanceDraftTrace = `{"kind":"session.header","schema_version":1,"written_at":"2026-07-01T00:00:00Z"}
{"turn":1,"seq":0,"kind":"turn.input","state_path":"idle","payload":{"input":"kick off"}}
{"turn":1,"seq":1,"kind":"harness.returned","state_path":"idle","payload":{"namespace":"host.chat.resolve","data":{"chat_id":"c1"}}}
{"turn":1,"seq":2,"kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"core.working","intent":"start","slots":{}}}
{"turn":2,"seq":0,"kind":"turn.input","state_path":"core.working","payload":{"input":"ship it"}}
{"turn":2,"seq":1,"kind":"harness.returned","state_path":"core.working","payload":{"namespace":"host.agent.converse","data":{"answer":"first"}}}
{"turn":2,"seq":2,"kind":"harness.returned","state_path":"core.working","payload":{"namespace":"host.agent.converse","data":{"answer":"second"}}}
{"turn":2,"seq":3,"kind":"machine.transition","state_path":"core.working","payload":{"from":"core.working","to":"core.gate","intent":"ship","slots":{}}}
{"turn":2,"seq":4,"kind":"machine.transition","state_path":"core.gate","payload":{"from":"core.gate","to":"core.landing","intent":"gate_ok","slots":{},"synthetic":true}}
`

// TestConvertTraceToFlow_EmitAcceptance_DerivesDraft verifies the draft
// acceptance: block appended by EmitAcceptance: final_state_in pins the final
// machine.transition target (including synthetic trailers), host_calls.required
// lists each dispatched handler once (handler-only, no args), world: is an
// empty map carrying the curation comment — and the story-drift policy holds
// (still no per-turn expect_state / expect_world).
func TestConvertTraceToFlow_EmitAcceptance_DerivesDraft(t *testing.T) {
	t.Parallel()

	lines, err := parseTraceLines([]byte(acceptanceDraftTrace))
	if err != nil {
		t.Fatalf("parseTraceLines: %v", err)
	}
	res, err := convertTraceLines(lines, ConvertOptions{
		AppPath:        "../app.yaml",
		CassettePath:   "draft.cassette.yaml",
		EmitAcceptance: true,
	})
	if err != nil {
		t.Fatalf("convertTraceLines: %v", err)
	}

	// The appended block must round-trip through the runner's read-side
	// FlowFixture struct — the draft is a real fixture field, not prose.
	var fixture FlowFixture
	if err := goyaml.Unmarshal(res.FlowYAML, &fixture); err != nil {
		t.Fatalf("unmarshal flow with acceptance draft: %v\n%s", err, res.FlowYAML)
	}
	acc := fixture.Acceptance
	if acc == nil {
		t.Fatalf("fixture has no acceptance block; got:\n%s", res.FlowYAML)
	}

	// final_state_in pins the LAST transition's target — the synthetic
	// gate_ok trailer landed the session in core.landing.
	if len(acc.FinalStateIn) != 1 || acc.FinalStateIn[0] != "core.landing" {
		t.Errorf("final_state_in = %v, want [core.landing]", acc.FinalStateIn)
	}

	// world: present but empty — the operator's curation hook.
	if acc.World == nil || len(acc.World) != 0 {
		t.Errorf("world = %v, want empty map", acc.World)
	}

	// host_calls.required: one handler-only entry per unique dispatched
	// handler, in first-seen order, no args, no times.
	if acc.HostCalls == nil {
		t.Fatalf("acceptance has no host_calls block; got:\n%s", res.FlowYAML)
	}
	var handlers []string
	for _, req := range acc.HostCalls.Required {
		handlers = append(handlers, req.Handler)
		if len(req.Args) != 0 {
			t.Errorf("required[%s] carries args %v; draft entries must be handler-only", req.Handler, req.Args)
		}
		if req.Times != nil {
			t.Errorf("required[%s] carries times; draft entries must be handler-only", req.Handler)
		}
	}
	want := []string{"host.chat.resolve", "host.agent.converse"}
	if strings.Join(handlers, ",") != strings.Join(want, ",") {
		t.Errorf("required handlers = %v, want %v (unique, first-seen order)", handlers, want)
	}
	if len(acc.HostCalls.Forbidden) != 0 {
		t.Errorf("draft must not emit forbidden:; got %v", acc.HostCalls.Forbidden)
	}

	// The rendered YAML must carry the operator-facing curation comments.
	if !strings.Contains(string(res.FlowYAML), "DRAFT acceptance contract") {
		t.Errorf("flow YAML must carry the draft banner comment; got:\n%s", res.FlowYAML)
	}
	if !strings.Contains(string(res.FlowYAML), "TODO(operator): curate the key world vars") {
		t.Errorf("flow YAML must carry the world curation comment; got:\n%s", res.FlowYAML)
	}

	// Story-drift policy untouched: still no per-turn expectations.
	if strings.Contains(stripComments(res.FlowYAML), "expect_state") {
		t.Errorf("flow must not emit expect_state; got:\n%s", res.FlowYAML)
	}
	if strings.Contains(stripComments(res.FlowYAML), "expect_world") {
		t.Errorf("flow must not emit expect_world; got:\n%s", res.FlowYAML)
	}
}

// TestConvertTraceToFlow_NoAcceptanceByDefault verifies EmitAcceptance is
// strictly opt-in: without it the generated fixture carries no acceptance key.
func TestConvertTraceToFlow_NoAcceptanceByDefault(t *testing.T) {
	t.Parallel()

	lines, err := parseTraceLines([]byte(acceptanceDraftTrace))
	if err != nil {
		t.Fatalf("parseTraceLines: %v", err)
	}
	res, err := convertTraceLines(lines, ConvertOptions{
		AppPath:      "../app.yaml",
		CassettePath: "draft.cassette.yaml",
	})
	if err != nil {
		t.Fatalf("convertTraceLines: %v", err)
	}
	if strings.Contains(stripComments(res.FlowYAML), "acceptance:") {
		t.Errorf("flow must not emit acceptance: without EmitAcceptance; got:\n%s", res.FlowYAML)
	}
}
