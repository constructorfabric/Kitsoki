package host_test

// Stream-cassette regression harness.
//
// A "stream cassette" is a recorded claude stream-json transcript (one
// JSON event per line — exactly what `claude -p --output-format
// stream-json` prints) stored under testdata/stream_cassettes. Unlike a
// host_cassette (which replays the *final* agent response and bypasses
// the stream parser entirely), a stream cassette is fed through the REAL
// runClaudeStreamJSON parser via an injected ClaudeRunner stub, so the
// whole stream-json → emitStreamEvent → StreamSink path runs.
//
// The cassette is the source of truth: the harness decodes the recorded
// assistant text blocks independently, then asserts the StreamEvents the
// sink observed reproduce every thought VERBATIM. This pins the
// completeness contract that surfaced the truncation bug — narration
// must reach a consumer in full, never clipped to a one-line preview.
//
// Recording a real transcript: run an agent call with
// `--output-format stream-json --verbose`, capture stdout to a .jsonl
// under testdata/stream_cassettes, scrub any secrets, and add a case to
// the table below.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// streamCassetteRunner returns a ClaudeRunner whose stdout is the raw
// recorded transcript — runClaudeStreamJSON's stub branch parses it line
// by line, firing emitStreamEvent (and thus the StreamSink) for each.
func streamCassetteRunner(transcript string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Stdout: transcript}, nil
	}
}

// expectedAssistantTexts decodes the recorded transcript the same way a
// human reading the cassette would: for every assistant message, the
// full narration is the join of its text blocks. This is the ground
// truth the streamed StreamEvent.Text values must match — derived from
// the cassette, NOT from classifyStreamEvent, so a regression in the
// parser can't quietly agree with itself.
func expectedAssistantTexts(t *testing.T, transcript string) []string {
	t.Helper()
	var want []string
	sc := bufio.NewScanner(strings.NewReader(transcript))
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("cassette line is not valid JSON: %v\nline: %s", err, line)
		}
		if ev.Type != "assistant" {
			continue
		}
		var texts []string
		for _, b := range ev.Message.Content {
			if b.Type == "text" && b.Text != "" {
				texts = append(texts, b.Text)
			}
		}
		if joined := strings.Join(texts, "\n"); joined != "" {
			want = append(want, joined)
		}
	}
	return want
}

// TestStreamCassette_NarrationCompleteness replays a recorded transcript
// through the real parser and asserts every assistant thought reaches the
// StreamSink in full, in order — the file-based analogue of the inline
// TestAgentStream_ThinkingNotTruncated.
func TestStreamCassette_NarrationCompleteness(t *testing.T) {
	t.Parallel()

	path := filepath.Join("testdata", "stream_cassettes", "agent_thinking.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stream cassette: %v", err)
	}
	transcript := string(raw)

	want := expectedAssistantTexts(t, transcript)
	if len(want) == 0 {
		t.Fatal("cassette has no assistant narration — fixture is degenerate")
	}
	// Guard: at least one thought must exceed the old 120-rune cap so a
	// regression would actually be observable here.
	var sawLong bool
	for _, w := range want {
		if len([]rune(w)) > 120 {
			sawLong = true
		}
	}
	if !sawLong {
		t.Fatal("cassette has no thought longer than 120 runes — truncation would be invisible")
	}

	sink := &memSink{}
	stream := &captureStreamSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("trace it"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithStreamSink(agentCtxForTest(sink), stream)
	ctx = host.WithClaudeRunner(ctx, streamCassetteRunner(transcript))

	if _, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath}); err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	// Collect the narration the sink actually saw, in order.
	var got []string
	for _, ev := range stream.all() {
		if ev.Type != "assistant" || ev.Text == "" {
			continue
		}
		if strings.Contains(ev.Text, "…") {
			t.Errorf("streamed narration was clipped with an ellipsis: %q", ev.Text)
		}
		got = append(got, ev.Text)
	}

	if len(got) != len(want) {
		t.Fatalf("streamed %d assistant narration events, cassette has %d\n got=%q\nwant=%q",
			len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("narration[%d] not reproduced verbatim\n got=%q\nwant=%q", i, got[i], want[i])
		}
	}

	// The combined thought+tool message must keep the tool breadcrumb
	// compact and separate from the (full) thought.
	var sawCombined bool
	for _, ev := range stream.all() {
		if ev.Type == "assistant" && ev.Tool == "Read" {
			sawCombined = true
			if ev.Preview != "stories/bugfix/rooms/clarifying.yaml" {
				t.Errorf("combined event Preview = %q, want the compact tool args", ev.Preview)
			}
		}
	}
	if !sawCombined {
		t.Error("cassette's combined text+tool_use event never surfaced with a tool name")
	}
}

func TestStreamCassette_CodexAssistantMessageBeforeToolSurfaces(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile(filepath.Join("testdata", "codex", "with_tool_round.jsonl"))
	if err != nil {
		t.Fatalf("read codex stream cassette: %v", err)
	}

	sink := &memSink{}
	stream := &captureStreamSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("run the probe"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithStreamSink(agentCtxForTest(sink), stream)
	ctx = host.WithAgentBackendForTest(ctx, host.NewCodexBackendForTest())
	ctx = host.WithCodexRunner(ctx, streamCassetteRunner(string(raw)))

	if _, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath}); err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	introIdx, toolIdx := -1, -1
	for i, ev := range stream.all() {
		if ev.Type == "assistant.message" && strings.Contains(ev.Text, "run the command") {
			introIdx = i
		}
		if ev.Type == "assistant" && ev.Tool == "shell" && strings.Contains(ev.Preview, "echo hi") {
			toolIdx = i
		}
	}
	if introIdx == -1 {
		t.Fatalf("codex assistant.message narration never reached the stream sink; events=%+v", stream.all())
	}
	if toolIdx == -1 {
		t.Fatalf("codex command_execution tool start never reached the stream sink; events=%+v", stream.all())
	}
	if introIdx > toolIdx {
		t.Fatalf("codex activity order = narration:%d tool:%d, want narration before tool", introIdx, toolIdx)
	}
}

// compile-time: captureStreamSink (defined in agent_stream_test.go) must
// satisfy host.StreamSink.
var _ host.StreamSink = (*captureStreamSink)(nil)
