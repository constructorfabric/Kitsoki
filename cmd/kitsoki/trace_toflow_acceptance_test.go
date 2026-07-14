package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalToFlowTrace is a one-turn trace with a single host call — just enough
// for `trace to-flow` to emit a fixture + cassette.
const minimalToFlowTrace = `{"kind":"session.header","schema_version":1,"written_at":"2026-07-01T00:00:00Z"}
{"turn":1,"seq":0,"kind":"turn.input","state_path":"idle","payload":{"input":"go"}}
{"turn":1,"seq":1,"kind":"harness.returned","state_path":"idle","payload":{"namespace":"host.chat.resolve","data":{"chat_id":"c1"}}}
{"turn":1,"seq":2,"kind":"machine.transition","state_path":"idle","payload":{"from":"idle","to":"core.landing","intent":"start","slots":{}}}
`

// runTraceToFlow executes `trace to-flow` against the root command tree and
// returns the generated fixture bytes.
func runTraceToFlow(t *testing.T, dir string, extraArgs ...string) []byte {
	t.Helper()
	tracePath := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(tracePath, []byte(minimalToFlowTrace), 0o644); err != nil {
		t.Fatalf("write trace: %v", err)
	}
	outPath := filepath.Join(dir, "flow.yaml")

	root := newRootCmd()
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	args := append([]string{"trace", "to-flow", tracePath, "--out", outPath, "--app", "../app.yaml"}, extraArgs...)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("trace to-flow: %v\nstderr: %s", err, stderr.String())
	}

	out, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read generated fixture: %v", err)
	}
	return out
}

// TestTraceToFlow_AcceptanceFlag verifies --acceptance threads through to the
// converter: the generated fixture carries the draft acceptance: block.
func TestTraceToFlow_AcceptanceFlag(t *testing.T) {
	t.Parallel()
	out := runTraceToFlow(t, t.TempDir(), "--acceptance")
	for _, want := range []string{"acceptance:", "final_state_in:", "core.landing", "host.chat.resolve", "world: {}"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("fixture missing %q; got:\n%s", want, out)
		}
	}
}

// TestTraceToFlow_NoAcceptanceByDefault verifies the flag is opt-in.
func TestTraceToFlow_NoAcceptanceByDefault(t *testing.T) {
	t.Parallel()
	out := runTraceToFlow(t, t.TempDir())
	if strings.Contains(string(out), "acceptance:") {
		t.Errorf("fixture must not carry acceptance: without --acceptance; got:\n%s", out)
	}
}
