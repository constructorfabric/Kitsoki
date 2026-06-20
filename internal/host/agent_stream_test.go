package host_test

// Stream-event fidelity tests — assert that assistant narration
// ("thinking") prose reaches the StreamSink in full, never clipped with
// an ellipsis. Regression for the bug where onelinePreview(.,120) was
// the *only* text the transcript ever saw, so a long thought rendered as
// "…before proposing anythin…". The compact Preview (slog trace + tool
// breadcrumb) stays clipped; the new Text field carries the full prose.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"kitsoki/internal/host"
)

// captureStreamSink records every StreamEvent it receives. OnStreamEvent
// runs on the agent stdout-reader goroutine, so guard with a mutex.
type captureStreamSink struct {
	mu     sync.Mutex
	events []host.StreamEvent
}

func (s *captureStreamSink) OnStreamEvent(_ context.Context, ev host.StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *captureStreamSink) all() []host.StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]host.StreamEvent(nil), s.events...)
}

// thinkingRunner emits a stream-json transcript with a long thought as a
// text-only assistant message, the same thought paired with a tool_use
// in one message, then a terminal result.
func thinkingRunner(thought, reply string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		lines := []string{
			`{"type":"system","subtype":"init","session_id":"sess-stream-1"}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":` + mustJSON(thought) + `}]}}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":` + mustJSON(thought) + `},` +
				`{"type":"tool_use","name":"Read","input":{"file_path":"prompt.md"}}]}}`,
			`{"type":"result","subtype":"success","result":` + mustJSON(reply) + `,"session_id":"sess-stream-1"}`,
		}
		return host.ClaudeRun{Stdout: strings.Join(lines, "\n") + "\n"}, nil
	}
}

// TestAgentStream_ThinkingNotTruncated asserts the full thought reaches
// the StreamSink (Text), while the compact Preview stays clipped — and
// that a combined text+tool_use message surfaces BOTH the full thought
// and the tool breadcrumb rather than one clobbering the other.
func TestAgentStream_ThinkingNotTruncated(t *testing.T) {
	t.Parallel()

	// Echoes the real symptom; must exceed the 120-rune preview cap so a
	// clip would be observable.
	const longThought = "I'll explore the PRD story tree to understand how " +
		"clarification questions are currently handled before proposing " +
		"anything substantial, then trace the routing and gate-decider wiring."
	if got := len([]rune(longThought)); got <= 120 {
		t.Fatalf("fixture must exceed the 120-rune cap to be meaningful; got %d runes", got)
	}

	sink := &memSink{}
	stream := &captureStreamSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("go"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithStreamSink(agentCtxForTest(sink), stream)
	ctx = host.WithClaudeRunner(ctx, thinkingRunner(longThought, "done"))

	if _, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath}); err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	var sawTextOnly, sawCombined bool
	for _, ev := range stream.all() {
		if ev.Type != "assistant" || ev.Text == "" {
			continue
		}
		// The thought must reach the sink intact — this is the bug.
		if strings.Contains(ev.Text, "…") {
			t.Errorf("assistant Text was clipped with an ellipsis: %q", ev.Text)
		}
		if ev.Text != longThought {
			t.Errorf("assistant Text = %q, want full thought %q", ev.Text, longThought)
		}

		switch ev.Tool {
		case "":
			sawTextOnly = true
			// The compact Preview is still deliberately clipped — that's
			// what makes the full-fidelity Text necessary.
			if !strings.HasSuffix(ev.Preview, "…") {
				t.Errorf("compact Preview should stay clipped; got %q", ev.Preview)
			}
		case "Read":
			sawCombined = true
			// Combined message: Preview carries the tool args, NOT the
			// thought (which lives in Text, in full).
			if ev.Preview != "prompt.md" {
				t.Errorf("combined event Preview = %q, want tool args %q", ev.Preview, "prompt.md")
			}
		}
	}

	if !sawTextOnly {
		t.Fatal("no text-only assistant narration reached the stream sink")
	}
	if !sawCombined {
		t.Fatal("combined text+tool_use event did not surface both Text and Tool")
	}
}

// parallelToolRunner emits ONE assistant message that batches three
// tool_use blocks — the shape claude produces when it fires parallel tool
// calls. Mirrors the real symptom where only the first tool surfaced.
func parallelToolRunner() host.ClaudeRunner {
	return func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		lines := []string{
			`{"type":"system","subtype":"init","session_id":"sess-parallel-1"}`,
			`{"type":"assistant","message":{"content":[` +
				`{"type":"tool_use","name":"Bash","input":{"command":"ls flows/"}},` +
				`{"type":"tool_use","name":"Read","input":{"file_path":"a.md"}},` +
				`{"type":"tool_use","name":"Read","input":{"file_path":"b.md"}}]}}`,
			`{"type":"result","subtype":"success","result":` + mustJSON("done") + `,"session_id":"sess-parallel-1"}`,
		}
		return host.ClaudeRun{Stdout: strings.Join(lines, "\n") + "\n"}, nil
	}
}

// TestAgentStream_ParallelToolsAllSurface asserts that when one assistant
// message carries multiple tool_use blocks (parallel tool calls), EVERY
// tool reaches the sink via StreamEvent.Tools — not just the first. This
// is what lets the web/TUI transcript render each tool on its own line.
// Regression: classifyStreamEvent only ever surfaced firstTool, so two of
// the three calls below vanished from the breadcrumb stream.
func TestAgentStream_ParallelToolsAllSurface(t *testing.T) {
	t.Parallel()

	sink := &memSink{}
	stream := &captureStreamSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("go"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithStreamSink(agentCtxForTest(sink), stream)
	ctx = host.WithClaudeRunner(ctx, parallelToolRunner())

	if _, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath}); err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	var tools []host.StreamToolUse
	for _, ev := range stream.all() {
		if ev.Type == "assistant" && len(ev.Tools) > 0 {
			tools = ev.Tools
			// Back-compat scalar still mirrors the first tool.
			if ev.Tool != tools[0].Name {
				t.Errorf("scalar Tool = %q, want first tool %q", ev.Tool, tools[0].Name)
			}
		}
	}

	want := []string{"Bash", "Read", "Read"}
	if len(tools) != len(want) {
		t.Fatalf("surfaced %d tools, want %d: %+v", len(tools), len(want), tools)
	}
	for i, n := range want {
		if tools[i].Name != n {
			t.Errorf("tool[%d].Name = %q, want %q", i, tools[i].Name, n)
		}
		if tools[i].Preview == "" {
			t.Errorf("tool[%d] (%s) has empty Preview", i, n)
		}
	}
}

// thinkingBlockRunner emits a stream-json transcript using extended-thinking
// `{"type":"thinking"}` content blocks (the real claude shape when thinking is
// enabled): a thinking-only message, a thinking+tool message, a narration text
// message — and NO terminal result event, so the reply must come from the
// assembled-text fallback.
func thinkingBlockRunner(thoughtA, thoughtB, narration string) host.ClaudeRunner {
	return func(_ context.Context, _ []string, _ string, _ string) (host.ClaudeRun, error) {
		lines := []string{
			`{"type":"system","subtype":"init","session_id":"sess-think-1"}`,
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":` + mustJSON(thoughtA) + `}]}}`,
			`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":` + mustJSON(thoughtB) + `},` +
				`{"type":"tool_use","name":"Edit","input":{"file_path":"bar.go"}}]}}`,
			`{"type":"assistant","message":{"content":[{"type":"text","text":` + mustJSON(narration) + `}]}}`,
		}
		return host.ClaudeRun{Stdout: strings.Join(lines, "\n") + "\n"}, nil
	}
}

// TestAgentStream_ThinkingBlocksSurface asserts that extended-thinking
// content blocks reach the StreamSink on the dedicated Thinking field —
// in full, alongside any tool_use in the same message — while staying OUT
// of Text, so the reply-assembly fallback (which accumulates Text) is
// never polluted with reasoning prose. Regression: classifyStreamEvent
// only extracted `text` blocks, so thinking-block thoughts silently
// vanished from every live surface (web chat, TUI, meta overlay).
func TestAgentStream_ThinkingBlocksSurface(t *testing.T) {
	t.Parallel()

	const thoughtA = "The off-by-one is in the loop bound."
	const thoughtB = "Fix the bound, then re-run the tests."
	const narration = "Edited the loop bound; tests pass."

	sink := &memSink{}
	stream := &captureStreamSink{}
	dir := t.TempDir()
	promptPath := filepath.Join(dir, "p.md")
	if err := os.WriteFile(promptPath, []byte("go"), 0o644); err != nil {
		t.Fatalf("write prompt: %v", err)
	}

	ctx := host.WithStreamSink(agentCtxForTest(sink), stream)
	ctx = host.WithClaudeRunner(ctx, thinkingBlockRunner(thoughtA, thoughtB, narration))

	res, err := host.AgentAskHandler(ctx, map[string]any{"prompt_path": promptPath})
	if err != nil {
		t.Fatalf("AgentAskHandler: %v", err)
	}

	var thinkings []string
	for _, ev := range stream.all() {
		if ev.Type != "assistant" {
			continue
		}
		if ev.Thinking != "" {
			thinkings = append(thinkings, ev.Thinking)
			if ev.Text != "" {
				t.Errorf("thinking event leaked into Text: %q", ev.Text)
			}
		}
		// The thinking+tool message must surface BOTH.
		if ev.Thinking == thoughtB && ev.Tool != "Edit" {
			t.Errorf("thinking+tool event lost its tool: Tool=%q", ev.Tool)
		}
	}
	if len(thinkings) != 2 || thinkings[0] != thoughtA || thinkings[1] != thoughtB {
		t.Fatalf("thinking blocks did not surface in order: %q", thinkings)
	}

	// No result event → the reply is the assembled-Text fallback. It must
	// carry the narration and NONE of the thinking prose.
	out, _ := res.Data["stdout"].(string)
	if !strings.Contains(out, narration) {
		t.Fatalf("fallback reply lost the narration: %q", out)
	}
	if strings.Contains(out, thoughtA) || strings.Contains(out, thoughtB) {
		t.Fatalf("fallback reply polluted with thinking prose: %q", out)
	}
}
