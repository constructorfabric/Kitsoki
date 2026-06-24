package host

import (
	"context"
	"fmt"
)

// RunClaudeOneShotForHarness adapts the canonical one-shot claude runner
// (runClaudeOneShot) to the intent-routing harness's exec contract so the
// routing harness execs the `claude` binary through the SAME mechanism as
// every agent verb. Stream/usage capture, the ClaudeRunner test seam, the
// IDE-link scrub, KITSOKI_SESSION_ID injection, and kitsoki-bin-on-PATH all
// apply uniformly — there is now exactly one place that forks claude.
//
// It lives here, not in harness, because runClaudeOneShot is package-private
// to host and host is the single owner of the claude invocation engine. The
// routing harness imports nothing from host (host → agent → harness already,
// so harness must not import host); instead the wiring site (cmd/kitsoki)
// assigns this bare function to harness.ClaudeCLIConfig.Exec. Go's structural
// func typing makes it satisfy harness.ClaudeExec without host importing
// harness.
//
// Return-value mapping mirrors the harness's former built-in invoke():
//   - context cancellation  → that error verbatim, empty stdout
//   - infra failure (Infra) → wrapped error, empty stdout
//   - non-zero exit code    → error carrying stderr/stdout, plus the stdout so
//     the caller can still parse a clarify envelope
//   - clean exit            → (stdout, nil)
//
// The harness passes --output-format json, so runClaudeOneShot takes its
// buffered-envelope branch and returns claude's raw JSON envelope as stdout —
// exactly what the harness parses for the "LLM never called submit" message.
// The schema-validated slot payload itself rides the MCP validator's
// side-channel capture file, independent of stdout.
func RunClaudeOneShotForHarness(ctx context.Context, bin string, args []string, stdin, workingDir string) (string, error) {
	cr, err := runClaudeOneShot(ctx, bin, args, stdin, workingDir)
	if err != nil {
		return "", err
	}
	if cr.Infra != nil {
		return "", fmt.Errorf("host: claude exec failed: %w", cr.Infra)
	}
	if cr.ExitCode != 0 {
		return cr.Stdout, fmt.Errorf("host: claude exited with code %d: %s",
			cr.ExitCode, claudeExitErrorMessage(cr.ExitCode, cr.Stderr, cr.Stdout))
	}
	return cr.Stdout, nil
}
