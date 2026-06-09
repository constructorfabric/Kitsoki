package metamode

// stub_oracle.go — a deterministic, no-LLM OracleCaller for the
// `kitsoki web --flow` / `--host-cassette` posture.
//
// The real OracleCaller (adapter.go's NewOracleCallerAdapter) shells out to
// the `claude` CLI. That is unacceptable for the deterministic web posture the
// Playwright demo and the no-LLM tests run in: there must be no LLM, no cost,
// and no flakiness. StubOracleCaller replaces it whenever the session runtime
// is built with a flow fixture (see cmd/kitsoki's oracleForMeta).
//
// The subtlety: Controller.Send computes SendResult.ReloadRequested /
// ChangedFiles from a real filesystem stat-diff of the story tree taken around
// the oracle call — NOT from the oracle reply (see controller.go's sendLocked).
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
)

// StubOracleCaller is a deterministic OracleCaller. Read-only modes get a
// canned, context-aware reply and touch nothing; edit-capable modes
// additionally perform a real disk mutation so the controller's reload
// handshake fires exactly as it does for the real story-author agent.
type StubOracleCaller struct {
	// reply builds the assistant text. nil ⇒ defaultStubReply.
	reply func(in AskInput, edited bool) string
	// mutate performs the edit-capable turn's disk write. nil ⇒
	// defaultStubMutate (append to meta-edits.log under in.Cwd). Returning
	// an error fails the turn (surfaced as the oracle error).
	mutate func(in AskInput) error

	mu  sync.Mutex
	seq int // per-process turn counter, so successive edits change bytes
}

// StubOption configures a StubOracleCaller.
type StubOption func(*StubOracleCaller)

// WithStubReply overrides the canned reply builder.
func WithStubReply(fn func(in AskInput, edited bool) string) StubOption {
	return func(s *StubOracleCaller) { s.reply = fn }
}

// WithStubMutate overrides the edit-capable disk mutation. Pass a no-op to
// model an edit mode that changes nothing (ReloadRequested stays false).
func WithStubMutate(fn func(in AskInput) error) StubOption {
	return func(s *StubOracleCaller) { s.mutate = fn }
}

// NewStubOracleCaller builds a deterministic no-LLM OracleCaller.
func NewStubOracleCaller(opts ...StubOption) *StubOracleCaller {
	s := &StubOracleCaller{}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Ask returns a deterministic reply. For edit-capable turns it first performs
// the disk mutation so Controller.Send's stat-diff sees a change.
func (s *StubOracleCaller) Ask(ctx context.Context, in AskInput) (AskOutput, error) {
	edited := false
	if editCapable(in.ToolAllowlist) {
		if err := s.runMutate(in); err != nil {
			return AskOutput{}, fmt.Errorf("metamode stub: edit mutation: %w", err)
		}
		edited = true
	}
	reply := s.reply
	if reply == nil {
		reply = defaultStubReply
	}
	return AskOutput{
		Reply: reply(in, edited),
		// Echo the input id so Controller.Send's "did the id change?" check
		// stays a no-op (the real adapter does the same on an empty result).
		NewClaudeSessionID: in.ClaudeSessionID,
	}, nil
}

func (s *StubOracleCaller) runMutate(in AskInput) error {
	if s.mutate != nil {
		return s.mutate(in)
	}
	return s.defaultStubMutate(in)
}

// defaultStubMutate appends one line to meta-edits.log under the turn's working
// dir. The per-process counter guarantees successive edit turns change the
// file (so each fires a reload); the first edit creates it.
func (s *StubOracleCaller) defaultStubMutate(in AskInput) error {
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
	state := extractContextField(in.UserMessage, "state")
	if state != "" {
		return fmt.Sprintf("You're at `%s`. _(deterministic no-LLM reply to: “%s”)_", state, user)
	}
	return fmt.Sprintf("_(deterministic no-LLM reply to: “%s”)_", user)
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

// Compile-time assertion that StubOracleCaller satisfies the seam.
var _ OracleCaller = (*StubOracleCaller)(nil)
