package metamode

// stub_agent.go — a deterministic, no-LLM AgentCaller for the
// `kitsoki web --flow` / `--host-cassette` posture.
//
// The real AgentCaller (adapter.go's NewAgentCallerAdapter) shells out to
// the `claude` CLI. That is unacceptable for the deterministic web posture the
// Playwright demo and the no-LLM tests run in: there must be no LLM, no cost,
// and no flakiness. StubAgentCaller replaces it whenever the session runtime
// is built with a flow fixture (see cmd/kitsoki's agentForMeta).
//
// The subtlety: Controller.Send computes SendResult.ReloadRequested /
// ChangedFiles from a real filesystem stat-diff of the story tree taken around
// the agent call — NOT from the agent reply (see controller.go's sendLocked).
// So a stub that only returns text yields ReloadRequested:false. To exercise
// the edit→commit→reload path deterministically, an edit-capable turn must make
// a real, controlled disk mutation during Ask. The default mutation appends to
// a `meta-edits.log` in the working dir: a non-hidden file the tree walk
// detects (it skips dotfiles), harmless to the manifest (it is not imported, so
// the post-edit AppDef re-load stays valid), and committed by the same
// deterministic git step the real path uses.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kitsoki/internal/host"
)

// StubAgentCaller is a deterministic AgentCaller. Read-only modes get a
// canned, context-aware reply and touch nothing; edit-capable modes
// additionally perform a real disk mutation so the controller's reload
// handshake fires exactly as it does for the real story-author agent.
type StubAgentCaller struct {
	// reply builds the assistant text. nil ⇒ defaultStubReply.
	reply func(in AskInput, edited bool) string
	// mutate performs the edit-capable turn's disk write. nil ⇒
	// defaultStubMutate (append to meta-edits.log under in.Cwd). Returning
	// an error fails the turn (surfaced as the agent error).
	mutate func(in AskInput) error
	// streamDelay is the pause between emitted stream events when a
	// StreamSink is in context. Zero means events fire instantly (unit
	// tests). Set to ~60-100ms for demo/video renders to make streaming
	// visible. Controlled via KITSOKI_META_STREAM_DELAY_MS.
	streamDelay time.Duration

	mu  sync.Mutex
	seq int // per-process turn counter, so successive edits change bytes
}

// StubOption configures a StubAgentCaller.
type StubOption func(*StubAgentCaller)

// WithStubReply overrides the canned reply builder.
func WithStubReply(fn func(in AskInput, edited bool) string) StubOption {
	return func(s *StubAgentCaller) { s.reply = fn }
}

// WithStubMutate overrides the edit-capable disk mutation. Pass a no-op to
// model an edit mode that changes nothing (ReloadRequested stays false).
func WithStubMutate(fn func(in AskInput) error) StubOption {
	return func(s *StubAgentCaller) { s.mutate = fn }
}

// WithStubStreamDelay sets the per-event pause when emitting stream events
// via the context StreamSink. Use 60-100ms for demo/video renders; leave
// at zero (the default) for unit and fast E2E tests.
func WithStubStreamDelay(d time.Duration) StubOption {
	return func(s *StubAgentCaller) { s.streamDelay = d }
}

// NewStubAgentCaller builds a deterministic no-LLM AgentCaller.
func NewStubAgentCaller(opts ...StubOption) *StubAgentCaller {
	s := &StubAgentCaller{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Ask returns a deterministic reply. For edit-capable turns it first performs
// the disk mutation so Controller.Send's stat-diff sees a change. When a
// StreamSink is present in ctx (the meta-stream SSE path), Ask also emits
// realistic tool + delta events so the web UI's streaming bubble, brain icon,
// and tool breadcrumbs become visible in demos and E2E tests.
func (s *StubAgentCaller) Ask(ctx context.Context, in AskInput) (AskOutput, error) {
	edited := false
	if editCapable(in.ToolAllowlist) {
		if err := s.runMutate(in); err != nil {
			return AskOutput{}, fmt.Errorf("metamode stub: edit mutation: %w", err)
		}
		edited = true
	}
	replyFn := s.reply
	if replyFn == nil {
		replyFn = defaultStubReply
	}
	text := replyFn(in, edited)

	// Emit stream events when a sink is installed (SSE path only). Unit tests
	// don't install a sink so this block never runs there — zero cost.
	if sink := host.StreamSinkFrom(ctx); sink != nil {
		s.emitStreamEvents(ctx, sink, in, text)
	}

	return AskOutput{
		Reply: text,
		// Echo the input id so Controller.Send's "did the id change?" check
		// stays a no-op (the real adapter does the same on an empty result).
		NewClaudeSessionID: in.ClaudeSessionID,
	}, nil
}

// emitStreamEvents simulates the event shape a real claude turn produces, so
// the no-LLM posture exercises every case the live surfaces must render:
// extended-thinking prose (StreamEvent.Thinking → "think" frames), tool-call
// breadcrumbs, and the reply streamed as narration deltas. Flow:
//
//  1. Emit a thinking event → 🧠 thought appears in the activity feed
//  2. Emit a tool event → ▸ ToolName breadcrumb below the thought
//  3. Edit-capable turns add a second thought + the Edit tool call
//     (a thought BETWEEN tools — the interleave order the feed must keep)
//  4. "Thinking" pause (4× streamDelay) so the viewer sees that state clearly
//  5. Stream the reply word-by-word at streamDelay each
//
// This gives the web UI's streaming bubble visible, unhurried stages: the
// interleaved thinking/tool feed, then progressive reply text.
func (s *StubAgentCaller) emitStreamEvents(ctx context.Context, sink host.StreamSink, in AskInput, reply string) {
	pause := func(mult int) {
		if s.streamDelay > 0 {
			time.Sleep(s.streamDelay * time.Duration(mult))
		}
	}

	// A thought, then the tool call it explains — like a real claude message
	// carrying a thinking block ahead of a tool_use block.
	sink.OnStreamEvent(ctx, host.StreamEvent{
		Type:     "assistant",
		Thinking: "Let me look at the story definition first.",
	})
	pause(2)
	sink.OnStreamEvent(ctx, host.StreamEvent{
		Type:    "assistant",
		Tool:    "Read",
		Preview: "app.yaml",
	})
	pause(2)

	if editCapable(in.ToolAllowlist) {
		// A second thought between tool calls: this is the arrival-order
		// interleave the feed must preserve (thought ABOVE the tool it
		// explains, never clumped at the bottom).
		sink.OnStreamEvent(ctx, host.StreamEvent{
			Type:     "assistant",
			Thinking: "I'll apply that change to the story files.",
		})
		pause(2)
		sink.OnStreamEvent(ctx, host.StreamEvent{
			Type:    "assistant",
			Tool:    "Edit",
			Preview: "rooms/idle.yaml",
		})
	}
	// Hold: browser renders the feed + "…" — viewer sees the "thinking" state.
	pause(4)

	// Stream the reply text in small chunks (split on whitespace + newlines).
	// Each chunk becomes a "delta" frame on the wire.
	words := strings.Fields(reply)
	for i, word := range words {
		chunk := word
		if i < len(words)-1 {
			chunk += " "
		}
		sink.OnStreamEvent(ctx, host.StreamEvent{
			Type: "assistant",
			Text: chunk,
		})
		pause(1)
	}
}

func (s *StubAgentCaller) runMutate(in AskInput) error {
	if s.mutate != nil {
		return s.mutate(in)
	}
	return s.defaultStubMutate(in)
}

// defaultStubMutate appends one line to meta-edits.log under the turn's working
// dir. The per-process counter guarantees successive edit turns change the
// file (so each fires a reload); the first edit creates it.
func (s *StubAgentCaller) defaultStubMutate(in AskInput) error {
	dir := in.Cwd
	if dir == "" {
		// No working dir to write into: model a no-op edit (no reload). This
		// keeps the stub safe when a caller forgets to thread the story dir.
		return nil
	}
	s.mu.Lock()
	s.seq++
	n := s.seq
	s.mu.Unlock()

	path := filepath.Join(dir, "meta-edits.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "meta-mode edit #%d\n", n)
	return err
}

// editCapable reports whether a mode with this tool allowlist can write. The
// builtin read-only modes (story.ask, kitsoki.ask) carry an explicit
// {Read,Glob,Grep} allowlist; story.edit / kitsoki.edit leave Tools unset, so
// the allowlist arrives empty (inherit the agent's full surface). Any allowlist
// that includes a write/exec tool, or is empty, counts as edit-capable.
func editCapable(allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}
	for _, t := range allowlist {
		switch t {
		case "Write", "Edit", "MultiEdit", "NotebookEdit", "Bash":
			return true
		}
	}
	return false
}

// defaultStubReply produces a short, deterministic, clearly-labelled reply. It
// echoes the current state from the [context] preamble (when present) and a
// snippet of the user's message, so the transcript reads sensibly in the demo.
func defaultStubReply(in AskInput, edited bool) string {
	user := truncateRunes(extractUserBlock(in.UserMessage), 120)
	if edited {
		if user == "" {
			return "Done — I've applied that change to the story and reloaded it. _(deterministic no-LLM reply)_"
		}
		return fmt.Sprintf("Done — I've applied “%s” to the story and reloaded it. _(deterministic no-LLM reply)_", user)
	}
	if isImproveModeStub(in, user) {
		return defaultStubImproveReport(in, user)
	}
	state := extractContextField(in.UserMessage, "state")
	if state != "" {
		return fmt.Sprintf("You're at `%s`. _(deterministic no-LLM reply to: “%s”)_", state, user)
	}
	return fmt.Sprintf("_(deterministic no-LLM reply to: “%s”)_", user)
}

func isImproveModeStub(in AskInput, user string) bool {
	haystack := strings.ToLower(in.SystemPrompt + "\n" + user)
	return strings.Contains(haystack, "story-improver") ||
		strings.Contains(haystack, "kitsoki-improver") ||
		strings.Contains(haystack, "introspection improvement report") ||
		strings.Contains(haystack, "improvement report")
}

func defaultStubImproveReport(in AskInput, user string) string {
	state := extractContextField(in.UserMessage, "state")
	if state == "" {
		state = "the latest run"
	}
	return fmt.Sprintf("Introspection report\n\nObserved false start:\nThis deterministic no-LLM demo is reviewing `%s` after completion for the operator request: “%s”.\n\nLikely cause:\nThe run finished successfully, so there is no real failure in the cassette; the improvement opportunity is making the completion-to-improve loop visible and cheap.\n\nRecommended change:\nKeep the completion reminder near the terminal state, and route it to `story.improve` so story authors can review prompts, tools, scripts, and flow coverage without leaving the session.\n\nEvidence bundle:\nFile a durable meta-improve report with the redacted session trace and transcript/playback evidence. Browser sessions also attach scrubbed HAR (`har.json`), rrweb replay (`rrweb.json`), recent console (`console.json`), and the redacted trace (`trace.redacted.jsonl`). TUI demos pair the report with the recorded terminal playback and transcript evidence.\n\nPosting:\nDefault to local `.artifacts/issues/bugs` for private iteration, use `--ticket-repo` for kitsoki engine/community-story GitHub issues, or use the private ticket-provider posting path via `--improve-ticket-provider` for private story ticket systems.\n\nTool and permission notes:\n`story.improve` stays read-only with Read/Glob/Grep. Do not grant shell or write permissions to the report mode; use `story.edit` only after the author accepts a concrete recommendation.\n\nRegression coverage:\nPin the path with no-LLM UI evidence: completed session -> improve reminder -> `story.improve` report -> evidence-backed local or remote posting.\n\nNext action:\nApply one scoped story or engine change through the edit mode, or enable auto-run at completion for future sessions.\n\n_(deterministic no-LLM improvement report)_", state, user)
}

// extractContextField pulls a single-line `key: value` out of the
// [context]…[/context] preamble the controller prepends. Returns "" when
// absent. Best-effort, line-based — mirrors the preamble format in
// controller.go's renderTurnContextPreamble.
func extractContextField(message, key string) string {
	inCtx := false
	for _, line := range strings.Split(message, "\n") {
		switch strings.TrimSpace(line) {
		case "[context]":
			inCtx = true
			continue
		case "[/context]":
			return ""
		}
		if !inCtx {
			continue
		}
		prefix := key + ": "
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

// extractUserBlock returns the text inside the [user]…[/user] block the
// controller wraps the literal message in. Falls back to the whole message
// when the block is absent (turn-less callers).
func extractUserBlock(message string) string {
	const open, close = "[user]\n", "\n[/user]"
	i := strings.Index(message, open)
	if i < 0 {
		return strings.TrimSpace(message)
	}
	rest := message[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return strings.TrimSpace(rest)
	}
	return strings.TrimSpace(rest[:j])
}

// truncateRunes shortens s to max runes with a trailing ellipsis when cut.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// Compile-time assertion that StubAgentCaller satisfies the seam.
var _ AgentCaller = (*StubAgentCaller)(nil)
