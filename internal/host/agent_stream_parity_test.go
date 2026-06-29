package host_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

type streamParityCase struct {
	name     string
	backend  host.AgentBackendForTest
	withStub func(context.Context, host.ClaudeRunner) context.Context
	stdout   string
}

func TestAgentStream_HarnessParityThinkingAndToolUse(t *testing.T) {
	t.Parallel()

	const thought = "I will inspect the target file first, then run the narrow validation command so the operator can see why the next step is safe."
	const toolName = "Read"
	const toolPreview = "stories/harness-parity-qa/app.yaml"

	claudeStream := readParityFixture(t, "parity/claude_thinking_tool.jsonl")
	codexStream := readParityFixture(t, "parity/codex_thinking_tool.jsonl")

	cases := []streamParityCase{
		{
			name:     "claude",
			backend:  host.NewClaudeBackendForTest(),
			withStub: host.WithClaudeRunner,
			stdout:   claudeStream,
		},
		{
			name:     "codex",
			backend:  host.NewCodexBackendForTest(),
			withStub: host.WithCodexRunner,
			stdout:   codexStream,
		},
	}

	var baseline []string
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := normalizedActivityForBackend(t, c)
			want := []string{
				"thinking:" + thought,
				"tool:" + toolName + ":" + toolPreview,
			}
			if strings.Join(got, "\n") != strings.Join(want, "\n") {
				t.Fatalf("normalized activity mismatch\n got=%q\nwant=%q", got, want)
			}
			if baseline == nil {
				baseline = got
			} else if strings.Join(got, "\n") != strings.Join(baseline, "\n") {
				t.Fatalf("backend activity differs from baseline\n got=%q\nbase=%q", got, baseline)
			}
		})
	}
}

func readParityFixture(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", rel))
	if err != nil {
		t.Fatalf("read parity fixture %s: %v", rel, err)
	}
	return string(b)
}

func normalizedActivityForBackend(t *testing.T, c streamParityCase) []string {
	t.Helper()
	sink := &memSink{}
	stream := &captureStreamSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("probe parity"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	runner := func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Stdout: c.stdout}, nil
	}
	ctx := host.WithStreamSink(agentCtxForTest(sink), stream)
	ctx = host.WithAgentBackendForTest(ctx, c.backend)
	ctx = c.withStub(ctx, runner)

	if _, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath}); err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	var out []string
	seenTools := map[string]bool{}
	for _, ev := range stream.all() {
		if ev.Type == "assistant" && ev.Text != "" {
			out = append(out, "thinking:"+ev.Text)
		}
		if ev.Type == "assistant" {
			for _, tool := range ev.Tools {
				key := tool.Name + ":" + tool.Preview
				if seenTools[key] {
					continue
				}
				seenTools[key] = true
				out = append(out, "tool:"+key)
			}
		}
	}
	return out
}
