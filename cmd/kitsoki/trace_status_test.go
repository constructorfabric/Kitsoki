package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// A synthetic JSONL trace: a couple of transitions (state/turn via StatePath),
// world.update events folding cost + last_error, ending non-terminal. Plus a
// deliberately PARTIAL trailing line to prove a live trace mid-append is safe.
const sampleTrace = `
{"turn":1,"seq":0,"ts":"2026-06-25T10:00:00Z","kind":"machine.transition","state_path":"reproducing"}
{"turn":1,"seq":1,"ts":"2026-06-25T10:00:01Z","kind":"world.update","state_path":"reproducing","payload":{"set":{"turn_cost_usd":0.5,"session_cost_usd":0.5}}}
{"turn":2,"seq":0,"ts":"2026-06-25T10:00:30Z","kind":"machine.transition","state_path":"proposing"}
{"turn":2,"seq":1,"ts":"2026-06-25T10:00:31Z","kind":"world.update","state_path":"proposing","payload":{"set":{"session_cost_usd":1.25,"last_error":"boom: something failed"}}}
{"turn":2,"seq":2,"ts":"2026-06-25T10:00:32Z","kind":"machine.say","state_pa`

func TestScanTraceStatus_DistilsLatest_SkipsPartialLine(t *testing.T) {
	st := scanTraceStatus(strings.NewReader(sampleTrace))
	if st.State != "proposing" {
		t.Errorf("state = %q, want proposing", st.State)
	}
	if st.Turn != 2 {
		t.Errorf("turn = %d, want 2", st.Turn)
	}
	if st.SessionCost != 1.25 {
		t.Errorf("session_cost = %v, want 1.25 (latest world.update)", st.SessionCost)
	}
	if st.LastError != "boom: something failed" {
		t.Errorf("last_error = %q", st.LastError)
	}
	// 4 well-formed lines; the partial trailing line must be skipped, not panic.
	if st.Events != 4 {
		t.Errorf("events = %d, want 4 (partial trailing line skipped)", st.Events)
	}
	if st.Exit != "" {
		t.Errorf("exit = %q, want empty (non-terminal)", st.Exit)
	}
}

func TestPrintTraceStatus_HumanFlagsStall(t *testing.T) {
	st := scanTraceStatus(strings.NewReader(sampleTrace))
	var buf bytes.Buffer
	// now = 10 minutes after the last event → idle 10m → STALLED on a non-terminal state.
	now := st.LastTs.Add(10 * time.Minute)
	if err := printTraceStatus(&buf, "/x/abc.jsonl", st, time.Time{}, false, now); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"state    proposing", "turn 2", "$1.2500", "boom: something failed", "STALLED"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestScanTraceStatus_ExitTerminal(t *testing.T) {
	tr := `{"turn":9,"seq":0,"ts":"2026-06-25T10:00:00Z","kind":"machine.transition","state_path":"__exit__shipped"}
{"turn":9,"seq":1,"ts":"2026-06-25T10:00:01Z","kind":"world.update","state_path":"__exit__shipped","payload":{"set":{"status":"shipped"}}}`
	st := scanTraceStatus(strings.NewReader(tr))
	if st.Exit != "__exit__shipped" || st.Status != "shipped" {
		t.Errorf("exit=%q status=%q, want __exit__shipped/shipped", st.Exit, st.Status)
	}
	var buf bytes.Buffer
	_ = printTraceStatus(&buf, "/x/s.jsonl", st, time.Time{}, false, st.LastTs.Add(time.Hour))
	if !strings.Contains(buf.String(), "✓ terminal") {
		t.Errorf("terminal session should not be flagged STALLED:\n%s", buf.String())
	}
}
