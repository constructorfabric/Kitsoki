// Package host — shared streaming helper for all host.oracle.* handlers.
//
// Every new verb (decide/ask/task/extract/converse) funnels its claude
// invocation through OracleStreamer so the streaming invariant
// (invariant 3) is upheld by construction rather
// than by per-handler discipline:
//
//   - When a StreamSink is in ctx, --output-format stream-json is used and
//     events are teed to the sink in real time.
//   - When no sink is present (one-shot CLI, tests), the output format falls
//     back to "text" unless the caller has explicitly set output_format: text.
//   - All five verbs produce a ClaudeRun; handlers extract what they need
//     (text, submitted JSON, etc.) from it using existing parsing helpers.
//
// Usage:
//
//	cr, sessionID, err := OracleStreamer{
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

// OracleStreamer is the shared entry point for every host.oracle.* claude
// invocation. It selects between stream-json and buffered-text output based
// on whether a StreamSink is wired into ctx, applies --verbose when needed,
// and returns a unified ClaudeRun + sessionID pair.
//
// CLIArgs must already contain the base flags (-p, --permission-mode, …) but
// MUST NOT include --output-format or --verbose — OracleStreamer adds those
// based on ctx.
//
// Per-call `tools:` (the effect-level override) wins over agent.Tools per
// precedence rule D5. Callers are responsible for resolving
// the effective tool list before calling OracleStreamer; this type only
// handles the transport.
type OracleStreamer struct {
	// Bin is the path to the claude binary (from resolveOracleBin).
	Bin string
	// CLIArgs are the base command-line flags for this call.
	// Must not include --output-format or --verbose.
	//
	// L11 convention: every element must start with '-'. Positional args must
	// not appear here; pass prompt content via Stdin and file paths via
	// --mcp-config / --append-system-prompt / etc. OracleStreamer.Run asserts
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

// Run dispatches the claude invocation via the appropriate output format.
// When a StreamSink is installed in ctx, it uses runClaudeStreamJSON and
// tees events to the sink. Otherwise it uses runClaudeOneShot (buffered
// text). Both paths honour the ClaudeRunner stub from ctx so tests don't
// need real subprocess forks.
//
// Returns (run, sessionID, error). sessionID is only populated on the
// stream-json path (from the system.init event); it is empty on the
// buffered-text path.
// checkCLIArgs asserts that no element of CLIArgs that is not preceded by a
// flag name starts without '-'. Flag values (e.g. the "bypassPermissions" in
// "--permission-mode bypassPermissions") are exempt because they immediately
// follow a "-"-prefixed flag name. Panics in tests to surface misconfiguration
// early; logs slog.Warn in production.
func (s OracleStreamer) checkCLIArgs(ctx context.Context) {
	prevWasFlag := true // treat the implicit "start" as "after a flag"
	for _, a := range s.CLIArgs {
		if strings.HasPrefix(a, "-") {
			// This element is a flag name. If it contains '=' the value is
			// inlined (--flag=value) so the next element is NOT a value.
			prevWasFlag = !strings.Contains(a, "=")
		} else {
			if !prevWasFlag {
				// Not immediately following a flag — this is a positional arg.
				msg := "OracleStreamer.CLIArgs contains a positional argument: " + a + " (must start with '-'; positional args go in Stdin or via named flags)"
				if testing.Testing() {
					panic(msg)
				}
				slog.WarnContext(ctx, msg)
			}
			prevWasFlag = false
		}
	}
}

func (s OracleStreamer) Run(ctx context.Context) (ClaudeRun, string, error) {
	s.checkCLIArgs(ctx)
	args := append(s.CLIArgs, "--output-format")

	if StreamSinkFrom(ctx) != nil {
		// Streaming path: append stream-json + --verbose (required by claude
		// in -p mode alongside --output-format stream-json).
		args = append(args, "stream-json", "--verbose")
		cr, sessionID, err := runClaudeStreamJSON(ctx, s.Bin, args, s.Stdin, s.WorkingDir, s.SessionID)
		return cr, sessionID, err
	}

	// Buffered path: append text output format and run one-shot.
	// Pass sessionID so the subprocess inherits KITSOKI_SESSION_ID even
	// on the buffered path (e.g. one-shot CLI invocations).
	args = append(args, "text")
	cr, err := runClaudeOneShot(ctx, s.Bin, args, s.Stdin, s.WorkingDir, s.SessionID)
	return cr, "", err
}
