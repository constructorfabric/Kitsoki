package testrunner_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

func TestPunchListLadderFlowDispatchesStructuredHarnessLadder(t *testing.T) {
	tracePath := filepath.Join(t.TempDir(), "ladder-flow.trace.jsonl")
	report, err := testrunner.RunFlows(t.Context(),
		"../../stories/punch-list/app.yaml",
		"../../stories/punch-list/flows/start_first_dispatches_ladder.yaml",
		testrunner.FlowOptions{TracePath: tracePath})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	result := report.Results[0]
	if !result.Passed {
		for _, turn := range result.Turns {
			for _, failure := range turn.Failures {
				t.Logf("flow=%s turn=%d failure: %s", filepath.Base(result.File), turn.TurnIndex+1, failure)
			}
		}
	}
	require.True(t, result.Passed)

	want := map[string]any{
		"models": []any{
			map[string]any{"backend": "claude", "provider": "synthetic-claude", "model": "hf:zai-org/GLM-5.2"},
			map[string]any{"backend": "codex", "provider": "codex-native", "model": "gpt-5.5"},
		},
		"efforts": []any{"low", "medium", "high", "xhigh", "max"},
	}
	events := readTraceEvents(t, tracePath)
	requireHostAgentTaskLadder(t, events, "driver", want)
	requireHostAgentTaskLadder(t, events, "implementer", want)
}

func readTraceEvents(t *testing.T, tracePath string) []map[string]any {
	t.Helper()
	f, err := os.Open(tracePath)
	require.NoError(t, err)
	defer f.Close()

	var events []map[string]any
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var event map[string]any
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &event))
		events = append(events, event)
	}
	require.NoError(t, scanner.Err())
	return events
}

func requireHostAgentTaskLadder(t *testing.T, events []map[string]any, agent string, want map[string]any) {
	t.Helper()
	for _, event := range events {
		if event["kind"] != "harness.called" {
			continue
		}
		payload, _ := event["payload"].(map[string]any)
		if payload["namespace"] != "host.agent.task" {
			continue
		}
		args, _ := payload["args"].(map[string]any)
		if args["agent"] != agent {
			continue
		}
		require.JSONEq(t, mustJSON(t, want), mustJSON(t, args["harness_ladder"]))
		return
	}
	t.Fatalf("host.agent.task for agent %q did not dispatch with harness_ladder", agent)
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	require.NoError(t, err)
	return string(b)
}
