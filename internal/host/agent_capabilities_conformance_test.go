package host

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type requiredAgentCapability string

const (
	capFinalResponse   requiredAgentCapability = "final_response"
	capSessionID       requiredAgentCapability = "session_id"
	capUsage           requiredAgentCapability = "usage"
	capVisibleActivity requiredAgentCapability = "visible_activity"
	capThinking        requiredAgentCapability = "thinking"
	capToolUse         requiredAgentCapability = "tool_use"
)

type capabilityObservation struct {
	Reply         string
	SessionID     string
	RawEventCount int
	Usage         map[string]any
	Events        []StreamEvent
}

type capabilityCaptureSink struct {
	mu     sync.Mutex
	events []StreamEvent
}

func (s *capabilityCaptureSink) OnStreamEvent(_ context.Context, ev StreamEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

func (s *capabilityCaptureSink) all() []StreamEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StreamEvent(nil), s.events...)
}

// TestConformance_ProviderCapabilityCassettes is the offline provider
// capability matrix. It is intentionally broader than "can parse a final
// answer": every backend fixture declares the chat-visible capabilities the UI
// depends on, and the test fails if a parser stops surfacing them.
func TestConformance_ProviderCapabilityCassettes(t *testing.T) {
	cases := []struct {
		name                    string
		backend                 agentBackend
		fixture                 string
		want                    []requiredAgentCapability
		wantReplyContains       string
		wantActivityContains    string
		wantActivityOccurrences int
		wantTool                string
		activityBeforeTool      bool
	}{
		{
			name:                 "claude/text_and_tool",
			backend:              claudeBackend{},
			fixture:              "parity/claude_thinking_tool.jsonl",
			want:                 []requiredAgentCapability{capFinalResponse, capSessionID, capVisibleActivity, capToolUse},
			wantReplyContains:    "done",
			wantActivityContains: "I will inspect the target file first",
			wantTool:             "Read",
			activityBeforeTool:   true,
		},
		{
			name:                 "codex/reasoning_and_mcp_tool",
			backend:              codexBackend{},
			fixture:              "parity/codex_thinking_tool.jsonl",
			want:                 []requiredAgentCapability{capFinalResponse, capSessionID, capUsage, capVisibleActivity, capThinking, capToolUse},
			wantReplyContains:    "done",
			wantActivityContains: "I will inspect the target file first",
			wantTool:             "Read",
			activityBeforeTool:   true,
		},
		{
			name:                 "codex/message_before_shell_tool",
			backend:              codexBackend{},
			fixture:              "codex/with_tool_round.jsonl",
			want:                 []requiredAgentCapability{capFinalResponse, capSessionID, capUsage, capVisibleActivity, capToolUse},
			wantReplyContains:    "It printed",
			wantActivityContains: "run the command",
			wantTool:             "shell",
			activityBeforeTool:   true,
		},
		{
			name:                    "copilot/reasoning_and_shell_tool",
			backend:                 copilotBackend{},
			fixture:                 "copilot/with_tool_round.jsonl",
			want:                    []requiredAgentCapability{capFinalResponse, capSessionID, capUsage, capVisibleActivity, capThinking, capToolUse},
			wantReplyContains:       "kitsoki-tool-probe",
			wantActivityContains:    "specific command",
			wantActivityOccurrences: 1,
			wantTool:                "bash",
			activityBeforeTool:      true,
		},
		{
			name:              "agy/final_result",
			backend:           agyBackend{},
			fixture:           "agy/ask_simple.jsonl",
			want:              []requiredAgentCapability{capFinalResponse, capSessionID, capUsage},
			wantReplyContains: "pong",
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			obs := observeCapabilityFixture(t, c.backend, readFixture(t, c.fixture))
			assertAgentCapabilities(t, obs, c.want, capabilityExpectation{
				ReplyContains:       c.wantReplyContains,
				ActivityContains:    c.wantActivityContains,
				ActivityOccurrences: c.wantActivityOccurrences,
				Tool:                c.wantTool,
				ActivityBeforeTool:  c.activityBeforeTool,
			})
		})
	}
}

func observeCapabilityFixture(t *testing.T, backend agentBackend, raw string) capabilityObservation {
	t.Helper()
	sink := &capabilityCaptureSink{}
	ctx := WithStreamSink(WithAgentBackend(context.Background(), backend), sink)
	reply, sid, rawEvents, usage, _ := parseStreamJSONOutput(ctx, raw)
	return capabilityObservation{
		Reply:         reply,
		SessionID:     sid,
		RawEventCount: len(rawEvents),
		Usage:         usage,
		Events:        sink.all(),
	}
}

type capabilityExpectation struct {
	ReplyContains       string
	ActivityContains    string
	ActivityOccurrences int
	Tool                string
	ActivityBeforeTool  bool
}

func assertAgentCapabilities(t *testing.T, obs capabilityObservation, want []requiredAgentCapability, exp capabilityExpectation) {
	t.Helper()
	if obs.RawEventCount == 0 {
		t.Fatal("provider emitted no parseable JSONL events")
	}
	for _, cap := range want {
		switch cap {
		case capFinalResponse:
			if strings.TrimSpace(obs.Reply) == "" {
				t.Fatal("missing final response")
			}
			if exp.ReplyContains != "" && !strings.Contains(obs.Reply, exp.ReplyContains) {
				t.Fatalf("final response %q does not contain %q", obs.Reply, exp.ReplyContains)
			}
		case capSessionID:
			if strings.TrimSpace(obs.SessionID) == "" {
				t.Fatal("missing session id")
			}
		case capUsage:
			if len(obs.Usage) == 0 {
				t.Fatal("missing usage")
			}
		case capVisibleActivity:
			if idx := activityIndex(obs.Events, exp.ActivityContains); idx < 0 {
				t.Fatalf("missing visible activity containing %q; events=%+v", exp.ActivityContains, obs.Events)
			}
		case capThinking:
			if idx := thinkingIndex(obs.Events, exp.ActivityContains); idx < 0 {
				t.Fatalf("missing thinking containing %q; events=%+v", exp.ActivityContains, obs.Events)
			}
		case capToolUse:
			if idx := toolIndex(obs.Events, exp.Tool); idx < 0 {
				t.Fatalf("missing tool %q; events=%+v", exp.Tool, obs.Events)
			}
		default:
			t.Fatalf("unknown required capability %q", cap)
		}
	}
	if exp.ActivityOccurrences > 0 {
		if got := activityOccurrences(obs.Events, exp.ActivityContains); got != exp.ActivityOccurrences {
			t.Fatalf("activity occurrence count for %q = %d, want %d; events=%+v",
				exp.ActivityContains, got, exp.ActivityOccurrences, obs.Events)
		}
	}
	if exp.ActivityBeforeTool {
		activity := activityIndex(obs.Events, exp.ActivityContains)
		tool := toolIndex(obs.Events, exp.Tool)
		if activity < 0 || tool < 0 {
			t.Fatalf("cannot check activity/tool order; activity=%d tool=%d events=%+v", activity, tool, obs.Events)
		}
		if activity > tool {
			t.Fatalf("activity surfaced after tool; activity=%d tool=%d events=%+v", activity, tool, obs.Events)
		}
	}
}

func activityIndex(events []StreamEvent, needle string) int {
	for i, ev := range events {
		if eventActivityTextMatches(ev, needle) {
			return i
		}
	}
	return -1
}

func thinkingIndex(events []StreamEvent, needle string) int {
	for i, ev := range events {
		if strings.TrimSpace(ev.Thinking) == "" {
			continue
		}
		if needle == "" || strings.Contains(ev.Thinking, needle) {
			return i
		}
	}
	return -1
}

func activityOccurrences(events []StreamEvent, needle string) int {
	var count int
	for _, ev := range events {
		if eventActivityTextMatches(ev, needle) {
			count++
		}
	}
	return count
}

func eventActivityTextMatches(ev StreamEvent, needle string) bool {
	text := strings.TrimSpace(strings.Join([]string{ev.Thinking, ev.Text}, "\n"))
	if text == "" {
		return false
	}
	return needle == "" || strings.Contains(text, needle)
}

func toolIndex(events []StreamEvent, name string) int {
	for i, ev := range events {
		if name == "" {
			if ev.Tool != "" || len(ev.Tools) > 0 {
				return i
			}
			continue
		}
		if ev.Tool == name {
			return i
		}
		for _, tool := range ev.Tools {
			if tool.Name == name {
				return i
			}
		}
	}
	return -1
}

const (
	liveCapabilityGateEnv = "KITSOKI_AGENT_LIVE_CONFORMANCE"
	liveBackendListEnv    = "KITSOKI_AGENT_LIVE_BACKENDS"

	liveThoughtMarker = "KITSOKI-CONFORMANCE-THOUGHT preparing shell probe."
	liveToolMarker    = "KITSOKI-CONFORMANCE-TOOL"
	liveDoneMarker    = "KITSOKI-CONFORMANCE-DONE"
)

// TestLiveAgentCapabilitiesConformance is the opt-in, real-provider drift
// check. It is deliberately NOT tied to KITSOKI_AGENT_LIVE so existing live
// smoke runs do not accidentally multiply provider calls. Run, for example:
//
//	KITSOKI_AGENT_LIVE_CONFORMANCE=1 KITSOKI_AGENT_LIVE_BACKENDS=codex,agy \
//	  go test ./internal/host -run TestLiveAgentCapabilitiesConformance -v
//
// Selected backends fail on missing CLIs, invocation errors, missing final
// output/session/usage, or missing stream activity/tool frames for backends
// that claim them.
func TestLiveAgentCapabilitiesConformance(t *testing.T) {
	if os.Getenv(liveCapabilityGateEnv) != "1" {
		t.Skipf("set %s=1 to run live provider capability conformance", liveCapabilityGateEnv)
	}

	cases := map[string]struct {
		backend              agentBackend
		prompt               string
		want                 []requiredAgentCapability
		expectation          capabilityExpectation
		timeout              time.Duration
		requiresStreamFrames bool
	}{
		"claude": {
			backend: claudeBackend{},
			prompt:  liveActivityToolPrompt(),
			want: []requiredAgentCapability{
				capFinalResponse, capSessionID, capUsage, capVisibleActivity, capToolUse,
			},
			expectation: capabilityExpectation{
				ReplyContains:      liveDoneMarker,
				ActivityContains:   liveThoughtMarker,
				Tool:               "Bash",
				ActivityBeforeTool: true,
			},
			timeout:              5 * time.Minute,
			requiresStreamFrames: true,
		},
		"copilot": {
			backend: copilotBackend{},
			prompt:  liveActivityToolPrompt(),
			want: []requiredAgentCapability{
				capFinalResponse, capSessionID, capUsage, capVisibleActivity, capToolUse,
			},
			expectation: capabilityExpectation{
				ReplyContains:      liveDoneMarker,
				ActivityContains:   liveThoughtMarker,
				Tool:               "bash",
				ActivityBeforeTool: true,
			},
			timeout:              5 * time.Minute,
			requiresStreamFrames: true,
		},
		"codex": {
			backend: codexBackend{},
			prompt:  liveActivityToolPrompt(),
			want: []requiredAgentCapability{
				capFinalResponse, capSessionID, capUsage, capVisibleActivity, capToolUse,
			},
			expectation: capabilityExpectation{
				ReplyContains:      liveDoneMarker,
				ActivityContains:   liveThoughtMarker,
				Tool:               "shell",
				ActivityBeforeTool: true,
			},
			timeout:              5 * time.Minute,
			requiresStreamFrames: true,
		},
		"agy": {
			backend: agyBackend{},
			prompt:  "Reply exactly " + liveDoneMarker + " and no other text.",
			want: []requiredAgentCapability{
				capFinalResponse, capSessionID, capUsage,
			},
			expectation: capabilityExpectation{
				ReplyContains: liveDoneMarker,
			},
			timeout: 3 * time.Minute,
		},
	}

	selected := selectedLiveBackends()
	var ran bool
	for _, name := range []string{"codex", "copilot", "agy", "claude"} {
		if !selected[name] {
			continue
		}
		c, ok := cases[name]
		if !ok {
			t.Fatalf("unknown live backend %q selected in %s", name, liveBackendListEnv)
		}
		ran = true
		t.Run(name, func(t *testing.T) {
			obs := runLiveCapabilityProbe(t, c.backend, c.prompt, c.timeout)
			if c.requiresStreamFrames && len(obs.Events) == 0 {
				t.Fatalf("%s produced no stream events", name)
			}
			assertAgentCapabilities(t, obs, c.want, c.expectation)
		})
	}
	for name := range selected {
		if _, ok := cases[name]; !ok {
			t.Fatalf("unknown live backend %q selected in %s", name, liveBackendListEnv)
		}
	}
	if !ran {
		t.Fatalf("no live backends selected; set %s=codex,agy or another backend list", liveBackendListEnv)
	}
}

func selectedLiveBackends() map[string]bool {
	raw := strings.TrimSpace(os.Getenv(liveBackendListEnv))
	if raw == "" {
		raw = "codex,copilot,agy"
	}
	selected := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		if name == "all" {
			for _, backend := range []string{"claude", "copilot", "codex", "agy"} {
				selected[backend] = true
			}
			continue
		}
		selected[name] = true
	}
	return selected
}

func liveActivityToolPrompt() string {
	return strings.Join([]string{
		"Kitsoki live provider capability probe.",
		"",
		"Perform these steps exactly:",
		"1. Before any tool call, send this visible assistant sentence exactly:",
		liveThoughtMarker,
		"2. Use one shell or command-execution tool to run exactly:",
		"printf '" + liveToolMarker + "\\n'",
		"3. After the tool result, reply exactly:",
		liveDoneMarker,
		"",
		"Do not call MCP tools. Do not ask questions. Do not add extra final text.",
	}, "\n")
}

func runLiveCapabilityProbe(t *testing.T, backend agentBackend, prompt string, timeout time.Duration) capabilityObservation {
	t.Helper()
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	sink := &capabilityCaptureSink{}
	ctx = WithStreamSink(WithAgentBackend(ctx, backend), sink)
	bin, err := backend.ResolveBin(ctx)
	if err != nil {
		t.Fatalf("resolve %s binary: %v", backend.Name(), err)
	}

	args := forceStreamJSONArgs([]string{
		"-p",
		"--permission-mode", "bypassPermissions",
		"--disable-slash-commands",
	})
	cr, sid, err := runClaudeStreamJSON(ctx, bin, args, prompt, repoRoot(t))
	if err != nil {
		t.Fatalf("%s live capability probe failed: %v", backend.Name(), err)
	}
	if cr.Infra != nil {
		t.Fatalf("%s live capability probe infra error: %v\nstderr:\n%s\nstdout:\n%s",
			backend.Name(), cr.Infra, cr.Stderr, cr.Stdout)
	}
	if cr.ExitCode != 0 {
		t.Fatalf("%s live capability probe exited %d\nstderr:\n%s\nstdout:\n%s",
			backend.Name(), cr.ExitCode, cr.Stderr, cr.Stdout)
	}

	return capabilityObservation{
		Reply:         cr.Stdout,
		SessionID:     sid,
		RawEventCount: len(cr.RawEvents),
		Usage:         cr.Usage,
		Events:        sink.all(),
	}
}
