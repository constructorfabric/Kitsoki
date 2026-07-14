package bugreport

import (
	"strings"
	"testing"

	"kitsoki/internal/runstatus/harscrub"
)

func TestEvidenceTriageMarkdownDerivesSummaryReproActualExpected(t *testing.T) {
	har := &harscrub.Har{Log: harscrub.Log{Entries: []harscrub.Entry{
		{
			Request: harscrub.Request{
				Method: "POST",
				URL:    "http://127.0.0.1:7761/rpc",
				PostData: &harscrub.PostData{
					Text: `{"jsonrpc":"2.0","method":"runstatus.session.submit","params":{"intent":"hide_trace"}}`,
				},
			},
			Response: harscrub.Response{Status: 500},
		},
	}}}
	rrweb := []byte(`[
		{"type":4,"data":{"href":"http://127.0.0.1:7761/#/s/sess-1/chat"},"timestamp":1000},
		{"type":3,"data":{"source":2,"type":2},"timestamp":1250},
		{"type":3,"data":{"source":5},"timestamp":1500}
	]`)
	console := []byte(`[{"level":"error","text":"layout gap"},{"level":"warn","text":"retrying"}]`)
	trace := []byte(strings.Join([]string{
		`{"msg":"turn.input","turn":1,"state_path":"main.idle","intent":"pin_artifact"}`,
		`{"msg":"machine.transition","turn":1,"state_path":"main.idle","from":"main.idle","to":"main.pinned","intent":"pin_artifact"}`,
	}, "\n"))

	md := EvidenceTriageMarkdown(EvidenceTriageInput{
		OperatorBody: strings.Join([]string{
			"Clicked location:",
			"- viewport: 120, 340",
			"- target: [data-testid=trace-toggle]",
			"- route: /#/s/sess-1/chat",
			"- expected: chat fills the hidden trace space",
			"- actual: empty area remains after hiding trace",
		}, "\n"),
		HAR:         har,
		HARSource:   "browser-fetch",
		HARDepth:    1,
		RRWebJSON:   rrweb,
		ConsoleJSON: console,
		TraceJSONL:  trace,
		ErrorCount:  2,
		LastRPC: LastRPCInfo{
			Method:  "runstatus.session.submit",
			Code:    "-32000",
			Message: "failed",
		},
	})

	for _, want := range []string{
		"## Evidence-derived triage",
		"Generated deterministically from captured evidence",
		"HAR: 1 exchange(s) from browser-observed capture",
		"1 failed HTTP response(s)",
		"RPC methods: `runstatus.session.submit`",
		"rrweb: 3 event(s)",
		"interactions: 1 click(s), 1 input change(s)",
		"Console/error state: 2 console entries (error=1, warn=1); captured client errors: 2",
		"Trace: 2 event(s) across 1 turn(s); states: `main.idle`; intents: `pin_artifact`",
		"Click placement: target `[data-testid=trace-toggle]` at viewport `120, 340` on route `/#/s/sess-1/chat`",
		"1. Open captured route `/#/s/sess-1/chat`.",
		"2. Click target `[data-testid=trace-toggle]` at viewport `120, 340`.",
		"3. In state `main.idle`, submit intent `pin_artifact`; observed transition `main.idle` -> `main.pinned`.",
		"Network evidence: POST /rpc (runstatus.session.submit) -> HTTP 500.",
		"Reporter-supplied actual behavior: empty area remains after hiding trace",
		"Reporter-supplied expected behavior: chat fills the hidden trace space",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("triage markdown missing %q:\n%s", want, md)
		}
	}
}

func TestEvidenceTriageMarkdownDoesNotInventExpectedBehavior(t *testing.T) {
	har := &harscrub.Har{Log: harscrub.Log{Entries: []harscrub.Entry{
		{Request: harscrub.Request{Method: "GET", URL: "https://example.test/status"}, Response: harscrub.Response{Status: 200}},
	}}}

	md := EvidenceTriageMarkdown(EvidenceTriageInput{HAR: har})
	if !strings.Contains(md, "Not deterministically captured in the evidence") {
		t.Fatalf("expected explicit non-inference note, got:\n%s", md)
	}
}
