package tui_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	tuipkg "kitsoki/internal/tui"
	"kitsoki/internal/tui/shot"
)

// TestMetaStream_FullThoughtReachesScrollback drives real MetaStreamMsg
// events through Update (exercising handleMetaStreamEvent's routing) and
// asserts the FULL narration reaches the transcript scrollback — never
// the one-line preview the truncation bug produced. It also pins the
// combined case: a single assistant message carrying both a thought and
// a tool call renders BOTH the full thought and the compact breadcrumb.
//
// This covers the sink→render boundary; the host-side stream-cassette
// test covers the parse→sink boundary.
func TestMetaStream_FullThoughtReachesScrollback(t *testing.T) {
	forceTrueColor(t)
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = resizeForTest(rm, 100, 24)
	// ModeAwaitingLLM satisfies handleMetaStreamEvent's in-flight gate so
	// the events render instead of being dropped as stale.
	tuipkg.SetModeForTest(&rm, tuipkg.ModeAwaitingLLM)
	tuipkg.ClearTranscriptPendingForTest(&rm)

	// Both thoughts exceed the old 120-rune cap, so a clip would be visible.
	const thought1 = "I'll explore the PRD story tree to understand how clarification questions are currently handled before proposing anything substantial here."
	const thought2 = "The clarifying room treats refine_feedback as a binding directive, so I need to trace where that slot is threaded before I touch the gate decider."
	require.Greater(t, len([]rune(thought1)), 120)
	require.Greater(t, len([]rune(thought2)), 120)

	feed := func(ev host.StreamEvent) {
		next, _ := rm.Update(tuipkg.MetaStreamMsg{Event: ev})
		rm, ok = tuipkg.ExtractRootModel(next)
		require.True(t, ok)
	}

	// Mirror exactly what emitStreamEvent ships: full Text plus the
	// compact clipped Preview. The old handler rendered Preview (clipped)
	// as the thought — this test pins that we now render Text in full.
	clip := func(s string) string {
		r := []rune(s)
		if len(r) <= 120 {
			return s
		}
		return string(r[:120]) + "…"
	}

	// 1) text-only thought.
	feed(host.StreamEvent{Type: "assistant", Text: thought1, Preview: clip(thought1)})
	// 2) combined thought + tool call in one message.
	feed(host.StreamEvent{Type: "assistant", Text: thought2, Tool: "Read", Preview: "prompt.md"})

	// Update flushes pending to scrollback on every call, so read the
	// persisted transcript entries (the rendered history) rather than
	// the now-drained pending queue.
	visible := stripStyles(tuipkg.GetTranscriptContent(rm))
	require.NotEmpty(t, visible, "stream events should have populated the transcript")

	require.NotContains(t, visible, "…",
		"no thought should be clipped with an ellipsis")
	require.Contains(t, visible, thought1,
		"the full text-only thought must reach the transcript intact")
	require.Contains(t, visible, thought2,
		"the full thought from the combined text+tool message must survive")
	// The combined message also renders the compact tool breadcrumb.
	require.Contains(t, visible, "▸", "combined message should render the tool-use glyph")
	require.Contains(t, visible, "prompt.md", "tool breadcrumb args must show")
}

func TestMetaStream_BackendNeutralAssistantMessageReachesScrollback(t *testing.T) {
	forceTrueColor(t)
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = resizeForTest(rm, 100, 24)
	tuipkg.SetModeForTest(&rm, tuipkg.ModeAwaitingLLM)
	tuipkg.ClearTranscriptPendingForTest(&rm)

	const intro = "I will run the command and report its output."
	const reason = "The command output confirms the probe path works."
	const finalAnswer = "It printed hi."

	feed := func(ev host.StreamEvent) {
		next, _ := rm.Update(tuipkg.MetaStreamMsg{Event: ev})
		rm, ok = tuipkg.ExtractRootModel(next)
		require.True(t, ok)
	}

	feed(host.StreamEvent{Type: "assistant.message", Text: intro})
	feed(host.StreamEvent{Type: "tool.execution_start", Tool: "shell", Preview: "/bin/zsh -lc 'echo hi'", Tools: []host.StreamToolUse{{Name: "shell", Preview: "/bin/zsh -lc 'echo hi'"}}})
	feed(host.StreamEvent{Type: "assistant.reasoning", Thinking: reason})
	feed(host.StreamEvent{Type: "assistant.message", Text: finalAnswer})
	feed(host.StreamEvent{Type: "result", IsResult: true})

	visible := stripStyles(tuipkg.GetTranscriptContent(rm))
	require.Contains(t, visible, intro,
		"codex-style assistant.message narration before a tool must render as thinking")
	require.Contains(t, visible, "shell",
		"backend tool-start events must render as tool breadcrumbs")
	require.Contains(t, visible, reason,
		"backend reasoning events must render immediately as thinking")
	require.NotContains(t, visible, finalAnswer,
		"the terminal assistant.message is the room reply and must not duplicate as thinking")

	if dir := os.Getenv("KITSOKI_TUI_ACTIVITY_ARTIFACT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		screenANSI := tuipkg.GetTranscriptContent(rm) + "\n" + rm.View()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "backend-neutral-activity.ansi"), []byte(screenANSI), 0o644))

		pngFile, err := os.Create(filepath.Join(dir, "backend-neutral-activity.png"))
		require.NoError(t, err)
		err = shot.RenderPNG(pngFile, screenANSI, shot.Options{Cols: 100, Rows: 24})
		closeErr := pngFile.Close()
		require.NoError(t, err)
		require.NoError(t, closeErr)
	}
}

// TestMetaStream_FinalAnswerNotShownAsThinking pins that the model's
// FINAL response — the last text-only assistant message before the
// terminal `result` event — is NOT echoed into the transcript as
// "thinking". The room (on-path) or metaSendDone's AppendSystem (meta)
// is the one that presents the final answer; streaming it here too
// duplicates it, once as muted thinking and once as the real reply.
//
// Intermediate narration (a text-only thought that is followed by more
// model activity, e.g. a tool call) MUST still surface — the fix
// distinguishes "narration that precedes more work" from "the terminal
// answer" by deferring each pure-text message one event and dropping it
// only when the `result` event arrives next.
func TestMetaStream_FinalAnswerNotShownAsThinking(t *testing.T) {
	forceTrueColor(t)
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = resizeForTest(rm, 100, 24)
	tuipkg.SetModeForTest(&rm, tuipkg.ModeAwaitingLLM)
	tuipkg.ClearTranscriptPendingForTest(&rm)

	const intro = "I'll start by exploring the PRD story tree before proposing anything substantial."
	const mid = "The clarifying room reads refine_feedback as a binding directive, so I'll confirm the wiring."
	const finalAnswer = "Got it — a notes app as a living example of how the PRD process works end to end."

	feed := func(ev host.StreamEvent) {
		next, _ := rm.Update(tuipkg.MetaStreamMsg{Event: ev})
		rm, ok = tuipkg.ExtractRootModel(next)
		require.True(t, ok)
	}

	// Mirror the real claude stream-json sequence (see the stream
	// cassette): an intermediate thought, a combined thought+tool
	// message, then a terminal text-only answer followed by `result`.
	feed(host.StreamEvent{Type: "assistant", Text: intro})
	feed(host.StreamEvent{Type: "assistant", Text: mid, Tool: "Read", Preview: "clarifying.yaml"})
	feed(host.StreamEvent{Type: "assistant", Text: finalAnswer})
	feed(host.StreamEvent{Type: "result", IsResult: true})

	visible := stripStyles(tuipkg.GetTranscriptContent(rm))

	require.Contains(t, visible, intro,
		"intermediate narration (followed by more work) must still surface as thinking")
	require.Contains(t, visible, mid,
		"narration accompanying a tool call must surface as thinking")
	require.NotContains(t, visible, finalAnswer,
		"the final answer must NOT be echoed as thinking — the room presents it")
}

// TestMetaStream_OnPathNoActivityIsSilent pins the negative half of the
// on-path tool-call-visibility contract: an on-path (ModeAwaitingLLM,
// non-meta) turn that streams NO agent activity — no tool calls, no
// intermediate thoughts, only a terminal text answer — must leave the
// transcript free of any activity breadcrumb. The on-path streaming
// surface reuses meta-mode's observer, so without this guard a future
// change could start leaking a spurious "thinking"/tool line into every
// silent direct turn (deterministic routes, guard bounces, etc.).
//
// Concretely: the only assistant text is the final answer (dropped on
// `result`, presented by the room), so no tool glyph (▸), no thinking
// glyph (🧠), and no muted arrow breadcrumb (→) should appear.
func TestMetaStream_OnPathNoActivityIsSilent(t *testing.T) {
	forceTrueColor(t)
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = resizeForTest(rm, 100, 24)
	tuipkg.SetModeForTest(&rm, tuipkg.ModeAwaitingLLM)
	tuipkg.ClearTranscriptPendingForTest(&rm)

	const finalAnswer = "Opened the door; you're now in the foyer."

	feed := func(ev host.StreamEvent) {
		next, _ := rm.Update(tuipkg.MetaStreamMsg{Event: ev})
		rm, ok = tuipkg.ExtractRootModel(next)
		require.True(t, ok)
	}

	// A turn with no tool calls and no intermediate narration: just the
	// terminal answer, then `result`. Mirrors a deterministic/direct
	// route whose on_enter agent (if any) said its piece in one shot.
	feed(host.StreamEvent{Type: "assistant", Text: finalAnswer})
	feed(host.StreamEvent{Type: "result", IsResult: true})

	visible := stripStyles(tuipkg.GetTranscriptContent(rm))

	require.NotContains(t, visible, "▸",
		"a turn with no tool calls must not render a tool breadcrumb")
	require.NotContains(t, visible, "🧠",
		"a turn with no intermediate thought must not render a thinking line")
	require.NotContains(t, visible, finalAnswer,
		"the terminal answer is the room's to present, never an activity line")
}

func TestMetaStream_LadderNoticesReachTranscript(t *testing.T) {
	forceTrueColor(t)
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = resizeForTest(rm, 120, 24)
	tuipkg.SetModeForTest(&rm, tuipkg.ModeAwaitingLLM)
	tuipkg.ClearTranscriptPendingForTest(&rm)

	feed := func(ev host.StreamEvent) {
		next, _ := rm.Update(tuipkg.MetaStreamMsg{Event: ev})
		rm, ok = tuipkg.ExtractRootModel(next)
		require.True(t, ok)
	}

	feed(host.StreamEvent{
		Type:     "ladder_attempt",
		Text:     "Kitsoki harness: trying claude-native / opus via claude (effort low).",
		Backend:  "claude",
		Provider: "claude-native",
		Model:    "opus",
		Effort:   "low",
	})
	feed(host.StreamEvent{
		Type:     "ladder_fallback",
		Subtype:  "infra",
		Text:     "Kitsoki harness: claude-native / opus via claude (effort low) failed (infra); backing it off and moving to the next configured model. Error: Claude Code needs login",
		Backend:  "claude",
		Provider: "claude-native",
		Model:    "opus",
		Effort:   "low",
		Error:    "Claude Code needs login",
	})
	feed(host.StreamEvent{
		Type:     "ladder_attempt",
		Text:     "Kitsoki harness: trying codex-native / gpt-5.5 via codex (effort low).",
		Backend:  "codex",
		Provider: "codex-native",
		Model:    "gpt-5.5",
		Effort:   "low",
	})

	visible := stripStyles(tuipkg.GetTranscriptContent(rm))
	require.Contains(t, visible, "Kitsoki harness: trying claude-native / opus via claude",
		"the first provider attempt must be visible before raw provider output")
	require.Contains(t, visible, "Claude Code needs login",
		"the fallback notice must include the failed provider's visible error")
	require.Contains(t, visible, "Kitsoki harness: trying codex-native / gpt-5.5 via codex",
		"the next provider attempt must be visible so the user sees the ladder movement")

	if dir := os.Getenv("KITSOKI_TUI_LADDER_ARTIFACT_DIR"); dir != "" {
		require.NoError(t, os.MkdirAll(dir, 0o755))
		frame := tuipkg.ComposeFrame(&rm, 120, 24)
		frameJSON, err := json.MarshalIndent(frame, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "frame.json"), frameJSON, 0o644))

		screenANSI := tuipkg.GetTranscriptContent(rm) + "\n" + rm.View()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "screen.ansi"), []byte(screenANSI), 0o644))

		pngFile, err := os.Create(filepath.Join(dir, "00-ladder-fallback.png"))
		require.NoError(t, err)
		err = shot.RenderPNG(pngFile, screenANSI, shot.Options{Cols: 120, Rows: 24})
		closeErr := pngFile.Close()
		require.NoError(t, err)
		require.NoError(t, closeErr)
	}
}

func TestMetaStream_OnPathFlushesWhileLiveLineActive(t *testing.T) {
	forceTrueColor(t)
	orch, sid := setupCloak(t)
	m := buildModel(t, orch, sid)
	rm, ok := tuipkg.ExtractRootModel(m)
	require.True(t, ok)
	rm = resizeForTest(rm, 100, 24)
	tuipkg.SetModeForTest(&rm, tuipkg.ModeAwaitingLLM)
	tuipkg.AppendLiveForTest(&rm, "routing: resolving…")
	tuipkg.ClearTranscriptPendingForTest(&rm)

	feed := func(ev host.StreamEvent) {
		next, _ := rm.Update(tuipkg.MetaStreamMsg{Event: ev})
		rm, ok = tuipkg.ExtractRootModel(next)
		require.True(t, ok)
	}

	feed(host.StreamEvent{Type: "assistant", Text: "I will inspect the current state before changing the TUI."})
	require.Empty(t, tuipkg.PendingTranscriptForTest(rm),
		"first pure narration is deferred until the next stream event proves it is not the final answer")

	feed(host.StreamEvent{Type: "assistant", Text: "The trace shows stream events arriving while the live line stays active.", Tool: "Read", Preview: "trace.redacted.jsonl"})

	require.Empty(t, tuipkg.PendingTranscriptForTest(rm),
		"stream activity must flush immediately even while ModeAwaitingLLM has an active live line")
	require.NotEmpty(t, tuipkg.LiveLineForTest(rm),
		"the live routing line should remain active; stream flushing must not finalize it")

	visible := stripStyles(tuipkg.GetTranscriptContent(rm))
	require.Contains(t, visible, "I will inspect the current state",
		"deferred narration should become visible once followed by more activity")
	require.Contains(t, visible, "trace.redacted.jsonl",
		"tool breadcrumb should be visible without waiting for Esc/cancel")
}
