// Package host — shared streaming helper for all host.agent.* handlers.
//
// Every new verb (decide/ask/task/extract/converse) funnels its claude
// invocation through AgentStreamer so the streaming invariant
// (invariant 3) is upheld by construction rather
// than by per-handler discipline:
//
//   - Every call uses --output-format stream-json ("stream-json everywhere"),
//     so it captures per-invocation token usage and emits live events.
//   - Events are teed to a StreamSink in real time when one is in ctx (the TUI
//     wires one per turn); otherwise they go to slog only. There is no separate
//     buffered-text path.
//   - All five verbs produce a ClaudeRun; handlers extract what they need
//     (text, submitted JSON, etc.) from it using existing parsing helpers.
//
// Usage:
//
//	cr, sessionID, err := AgentStreamer{
//	    Bin:         bin,
//	    CLIArgs:     cliArgs,
//	    Stdin:       rendered,
//	    WorkingDir:  workingDir,
//	}.Run(ctx)
package host

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// AgentStreamer is the shared entry point for every host.agent.* claude
// invocation. It selects between stream-json and buffered-text output based
// on whether a StreamSink is wired into ctx, applies --verbose when needed,
// and returns a unified ClaudeRun + sessionID pair.
//
// CLIArgs must already contain the base flags (-p, --permission-mode, …) but
// MUST NOT include --output-format or --verbose — AgentStreamer adds those
// based on ctx.
//
// Per-call `tools:` (the effect-level override) wins over agent.Tools per
// precedence rule D5. Callers are responsible for resolving
// the effective tool list before calling AgentStreamer; this type only
// handles the transport.
type AgentStreamer struct {
	// Bin is the path to the claude binary (from resolveAgentBin).
	Bin string
	// CLIArgs are the base command-line flags for this call.
	// Must not include --output-format or --verbose.
	//
	// L11 convention: every element must start with '-'. Positional args must
	// not appear here; pass prompt content via Stdin and file paths via
	// --mcp-config / --append-system-prompt / etc. AgentStreamer.Run asserts
	// this invariant at runtime: panics in tests (to surface misconfiguration
	// early), logs a warning in production.
	CLIArgs []string
	// Stdin is the prompt text rendered and stripped of source-color sentinels.
	Stdin string
	// WorkingDir is the cwd for the claude subprocess.
	WorkingDir string
	// SessionID, when non-empty, is injected as KITSOKI_SESSION_ID into the
	// subprocess environment per-subprocess (not via os.Setenv). This is the
	// preferred propagation path for trace continuity; callers obtain the value
	// from extractSessionIDCtx(ctx) or a parent_session_id param.
	SessionID string
}

// Run dispatches the claude invocation. "stream-json everywhere": every agent
// call runs as --output-format stream-json so it captures per-invocation token
// usage and emits live progress events — to slog always, and to a StreamSink
// when one is installed in ctx (the TUI wires one per turn). There is no longer
// a separate buffered-text path; the no-sink case simply has no sink to tee to.
// The ClaudeRunner stub from ctx is honoured so tests don't fork subprocesses.
//
// Returns (run, sessionID, error). sessionID is populated from the stream's
// system.init / result event (empty when the run produced no session id).
// checkCLIArgs asserts that no element of CLIArgs that is not preceded by a
// flag name starts without '-'. Flag values (e.g. the "bypassPermissions" in
// "--permission-mode bypassPermissions") are exempt because they immediately
// follow a "-"-prefixed flag name. Panics in tests to surface misconfiguration
// early; logs slog.Warn in production.
func (s AgentStreamer) checkCLIArgs(ctx context.Context) {
	prevWasFlag := true // treat the implicit "start" as "after a flag"
	for _, a := range s.CLIArgs {
		if strings.HasPrefix(a, "-") {
			// This element is a flag name. If it contains '=' the value is
			// inlined (--flag=value) so the next element is NOT a value.
			prevWasFlag = !strings.Contains(a, "=")
		} else {
			if !prevWasFlag {
				// Not immediately following a flag — this is a positional arg.
				msg := "AgentStreamer.CLIArgs contains a positional argument: " + a + " (must start with '-'; positional args go in Stdin or via named flags)"
				if testing.Testing() {
					panic(msg)
				}
				slog.WarnContext(ctx, msg)
			}
			prevWasFlag = false
		}
	}
}

func (s AgentStreamer) Run(ctx context.Context) (ClaudeRun, string, error) {
	s.checkCLIArgs(ctx)
	// stream-json + --verbose (claude requires --verbose alongside
	// --output-format stream-json in -p mode). emitStreamEvent tees to a sink
	// only when one is installed, so the no-sink case still streams to slog and
	// captures usage. sessionID is threaded so the subprocess inherits
	// KITSOKI_SESSION_ID.
	args := append(s.CLIArgs, "--output-format", "stream-json", "--verbose")
	return runClaudeStreamJSON(ctx, s.Bin, args, s.Stdin, s.WorkingDir, s.SessionID)
}
