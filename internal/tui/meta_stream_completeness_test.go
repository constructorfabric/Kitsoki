package tui_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/host"
	tuipkg "kitsoki/internal/tui"
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
