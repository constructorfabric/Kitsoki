package mcp_test

// validator_postcmd_test.go — coverage for the post-cmd / max-retries /
// Outcome() additions on ValidatorServer.
//
// These tests stand up a real subprocess via a small shell-script fixture
// rather than mocking exec.Cmd: the contract being tested is the wire
// shape (`--<key> <value>` argv, --submitted-json <tmp>, exit code,
// stderr capping) so faking it in-process would defeat the point.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	kitsokimcp "kitsoki/internal/mcp"
)

// connectValidatorWithCfg wires up an in-process client+server pair like
// connectValidator, but accepts the full ValidatorConfig so post-cmd
// tests can drive the new flags.
func connectValidatorWithCfg(t *testing.T, cfg kitsokimcp.ValidatorConfig) (*mcpsdk.ClientSession, *kitsokimcp.ValidatorServer, func()) {
	t.Helper()
	if cfg.SchemaJSON == nil {
		cfg.SchemaJSON = fixProposalSchema
	}
	srv, err := kitsokimcp.NewValidatorServer(cfg)
	require.NoError(t, err)

	clientT, serverT := mcpsdk.NewInMemoryTransports()

	ctx := context.Background()
	go func() {
		if _, err := srv.Connect(ctx, serverT, nil); err != nil {
			t.Logf("server connect error: %v", err)
		}
	}()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{
		Name:    "test-client",
		Version: "0",
	}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	require.NoError(t, err)

	return cs, srv, func() {
		_ = cs.Close()
	}
}

// validPayload returns a fix-proposal payload that always passes
// fixProposalSchema. Tests that need schema-pass-but-post-cmd-control
// use this so the schema layer is never the bottleneck.
func validPayload() map[string]any {
	return map[string]any{
		"summary":       "fix double-Close on rpc client connection",
		"confidence":    "high",
		"files_changed": []string{"a.go"},
	}
}

// writePostCmdScript writes a small bash script to a tempfile that:
//   - prints its argv (one per line) to a sentinel file at $ARGV_OUT (if set);
//   - writes $STDERR_PAYLOAD to stderr (or "" if unset);
//   - exits with $EXIT_CODE (default 0).
//
// The script returns its absolute path. Tests use environment variables
// to drive its behaviour rather than baking in per-test scripts.
func writePostCmdScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "post-cmd.sh")
	body := `#!/usr/bin/env bash
set -u
if [[ -n "${ARGV_OUT:-}" ]]; then
  : > "$ARGV_OUT"
  for a in "$@"; do
    printf '%s\n' "$a" >> "$ARGV_OUT"
  done
fi
if [[ -n "${CWD_OUT:-}" ]]; then
  pwd > "$CWD_OUT"
fi
if [[ -n "${STDERR_PAYLOAD:-}" ]]; then
  printf '%s' "$STDERR_PAYLOAD" >&2
fi
exit "${EXIT_CODE:-0}"
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

// TestValidator_SchemaOnly_BackwardsCompat — no post-cmd configured, the
// validator behaves exactly like before: schema-pass succeeds, schema-fail
// returns an error response. Outcome() reports Success after one good
// submit.
func TestValidator_SchemaOnly_BackwardsCompat(t *testing.T) {
	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	attempts, successful, _ := srv.Stats()
	assert.Equal(t, 1, attempts)
	assert.Equal(t, 1, successful)
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv.Outcome())
}

// TestValidator_SchemaPass_PostCmdAccept — schema passes, post-cmd exits 0,
// successfulSubmits == 1, Outcome == Success.
func TestValidator_SchemaPass_PostCmdAccept(t *testing.T) {
	script := writePostCmdScript(t)
	t.Setenv("EXIT_CODE", "0")

	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd: script,
	})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "post-cmd exit 0 must be treated as accept")

	attempts, successful, _ := srv.Stats()
	assert.Equal(t, 1, attempts)
	assert.Equal(t, 1, successful)
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv.Outcome())
}

// TestValidator_SchemaPass_PostCmdReject — schema passes, post-cmd exits 1
// with stderr; validator returns IsError with the stderr text;
// successfulSubmits stays at 0.
func TestValidator_SchemaPass_PostCmdReject(t *testing.T) {
	script := writePostCmdScript(t)
	t.Setenv("EXIT_CODE", "1")
	t.Setenv("STDERR_PAYLOAD", "ImplVerifier: file foo.go did not change")

	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd: script,
	})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "post-cmd exit 1 must be treated as reject")
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	assert.Contains(t, tc.Text, "post-cmd verifier rejected")
	assert.Contains(t, tc.Text, "ImplVerifier: file foo.go did not change")

	attempts, successful, lastErr := srv.Stats()
	assert.Equal(t, 1, attempts)
	assert.Equal(t, 0, successful)
	assert.Contains(t, lastErr, "ImplVerifier: file foo.go did not change")
}

// TestValidator_SchemaFailFirst_ThenPass — first submit fails schema,
// second submits a valid payload. attempts == 2, successfulSubmits == 1.
func TestValidator_SchemaFailFirst_ThenPass(t *testing.T) {
	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{})
	defer done()

	r1, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: map[string]any{"summary": "missing files_changed", "confidence": "low"},
	})
	require.NoError(t, err)
	require.True(t, r1.IsError)

	r2, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, r2.IsError)

	attempts, successful, _ := srv.Stats()
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 1, successful)
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv.Outcome())
}

// TestValidator_PostCmdRejectThenAccept — first post-cmd rejects, second
// accepts. attempts == 2, successfulSubmits == 1.
func TestValidator_PostCmdRejectThenAccept(t *testing.T) {
	script := writePostCmdScript(t)
	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd: script,
	})
	defer done()

	t.Setenv("EXIT_CODE", "1")
	t.Setenv("STDERR_PAYLOAD", "first attempt failed")
	r1, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.True(t, r1.IsError)

	t.Setenv("EXIT_CODE", "0")
	t.Setenv("STDERR_PAYLOAD", "")
	r2, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, r2.IsError)

	attempts, successful, _ := srv.Stats()
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 1, successful)
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv.Outcome())
}

// TestValidator_MaxRetriesExhausted — 5 rejections in a row, the 6th call
// returns the MAX RETRIES EXHAUSTED sentinel, Outcome == RetriesExhausted.
func TestValidator_MaxRetriesExhausted(t *testing.T) {
	script := writePostCmdScript(t)
	t.Setenv("EXIT_CODE", "1")
	t.Setenv("STDERR_PAYLOAD", "always reject")

	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd:    script,
		MaxRetries: 5,
	})
	defer done()

	for i := 0; i < 5; i++ {
		res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name:      "submit",
			Arguments: validPayload(),
		})
		require.NoError(t, err)
		require.True(t, res.IsError, "iteration %d", i)
	}

	// 6th call: the validator must short-circuit with the exhaustion
	// sentinel without spawning the post-cmd again. attempts must NOT
	// increment.
	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	tc, _ := res.Content[0].(*mcpsdk.TextContent)
	assert.Contains(t, tc.Text, "MAX RETRIES EXHAUSTED")

	attempts, successful, _ := srv.Stats()
	assert.Equal(t, 5, attempts, "exhaustion check must NOT increment attempts on the post-exhaustion call")
	assert.Equal(t, 0, successful)
	assert.Equal(t, kitsokimcp.OutcomeRetriesExhausted, srv.Outcome())
}

// TestValidator_PostCmdArgsForwarded — the post-cmd subprocess sees the
// configured --post-cmd-arg KEY=VALUE entries on its argv as
// `--KEY VALUE`, plus the canonical `--submitted-json <tmp>`.
func TestValidator_PostCmdArgsForwarded(t *testing.T) {
	script := writePostCmdScript(t)
	argvOut := filepath.Join(t.TempDir(), "argv.txt")
	t.Setenv("EXIT_CODE", "0")
	t.Setenv("ARGV_OUT", argvOut)

	cs, _, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd: script,
		PostCmdArgs: []kitsokimcp.PostCmdArg{
			{Key: "ticket", Value: "PLTFRM-89912"},
			{Key: "worktree", Value: "/tmp/work"},
		},
	})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	raw, err := os.ReadFile(argvOut)
	require.NoError(t, err)
	got := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")

	// Expected argv: --ticket PLTFRM-89912 --worktree /tmp/work --submitted-json <some path>
	require.GreaterOrEqual(t, len(got), 6, "argv: %v", got)
	assert.Equal(t, "--ticket", got[0])
	assert.Equal(t, "PLTFRM-89912", got[1])
	assert.Equal(t, "--worktree", got[2])
	assert.Equal(t, "/tmp/work", got[3])
	assert.Equal(t, "--submitted-json", got[4])
	// argv[5] is the tempfile path; sanity-check that it ends in .json
	// and that its contents match the submitted payload.
	assert.True(t, strings.HasSuffix(got[5], ".json"), "got: %s", got[5])
}

// TestValidator_PostCmdCwd — the post-cmd subprocess runs in the
// configured working directory.
func TestValidator_PostCmdCwd(t *testing.T) {
	script := writePostCmdScript(t)
	cwdDir := t.TempDir()
	cwdOut := filepath.Join(t.TempDir(), "cwd.txt")
	t.Setenv("EXIT_CODE", "0")
	t.Setenv("CWD_OUT", cwdOut)

	cs, _, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd:    script,
		PostCmdCwd: cwdDir,
	})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, res.IsError)

	raw, err := os.ReadFile(cwdOut)
	require.NoError(t, err)
	gotCwd := strings.TrimSpace(string(raw))
	// Resolve symlinks on both sides — macOS /tmp -> /private/tmp etc.
	wantResolved, _ := filepath.EvalSymlinks(cwdDir)
	gotResolved, _ := filepath.EvalSymlinks(gotCwd)
	assert.Equal(t, wantResolved, gotResolved, "post-cmd must run in PostCmdCwd")
}

// TestValidator_PostCmdStderrCapped — stderr larger than 2000 bytes is
// capped (tail-preserved) and ANSI escape sequences are stripped.
func TestValidator_PostCmdStderrCapped(t *testing.T) {
	script := writePostCmdScript(t)
	// 5 KiB of stderr: a noisy banner, a colour-coded line, then a
	// distinctive trailing marker we should still see post-cap.
	banner := strings.Repeat("noise ", 1000) // ~6 KiB of "noise "
	colored := "\x1b[31mERROR\x1b[0m: things broke\n"
	trailing := "DISTINCTIVE_TAIL_MARKER\n"
	payload := banner + colored + trailing
	require.Greater(t, len(payload), 5000)

	t.Setenv("EXIT_CODE", "1")
	t.Setenv("STDERR_PAYLOAD", payload)

	cs, _, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd: script,
	})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	tc, _ := res.Content[0].(*mcpsdk.TextContent)

	// ANSI escape stripped: no \x1b in the response.
	assert.NotContains(t, tc.Text, "\x1b[", "ANSI escape sequences must be stripped")
	// Tail preserved: the distinctive trailing marker must survive.
	assert.Contains(t, tc.Text, "DISTINCTIVE_TAIL_MARKER")
	// Capped: total verifier-stderr block must be roughly 2000 bytes
	// (plus framing prose). The "[truncated]" marker must appear.
	assert.Contains(t, tc.Text, "[truncated]")
	// And the bulk of the noise banner must be gone.
	noiseCount := strings.Count(tc.Text, "noise ")
	// 2000 / 6 ≈ 333 occurrences; leave generous slack.
	assert.Less(t, noiseCount, 400, "stderr was not capped: %d 'noise ' occurrences", noiseCount)
}

// TestValidator_AbandonedOutcome — when the session ends with no
// successful submit but the retry budget was not spent, Outcome ==
// Abandoned. We simulate "session ends" by simply not calling Close
// after some failed attempts and reading Outcome() directly; the
// state-machine semantics of Outcome don't depend on Close having
// been observed by the validator.
func TestValidator_AbandonedOutcome(t *testing.T) {
	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		MaxRetries: 5,
	})
	defer done()

	// Two failed schema submissions, then "abandon" (just stop calling).
	for i := 0; i < 2; i++ {
		_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
			Name:      "submit",
			Arguments: map[string]any{"bogus": fmt.Sprintf("attempt-%d", i)},
		})
		require.NoError(t, err)
	}

	attempts, successful, _ := srv.Stats()
	assert.Equal(t, 2, attempts)
	assert.Equal(t, 0, successful)
	assert.Equal(t, kitsokimcp.OutcomeAbandoned, srv.Outcome(),
		"low attempt count + no success must report Abandoned")
}

// TestValidator_AbandonedOutcome_NoCalls — the LLM ends the session
// without ever calling submit at all. Outcome must be Abandoned (zero
// attempts, zero successes).
func TestValidator_AbandonedOutcome_NoCalls(t *testing.T) {
	_, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		MaxRetries: 5,
	})
	defer done()

	attempts, successful, _ := srv.Stats()
	assert.Equal(t, 0, attempts)
	assert.Equal(t, 0, successful)
	assert.Equal(t, kitsokimcp.OutcomeAbandoned, srv.Outcome())
}

// TestServerRun_OutcomeSurfaced — Outcome() is reachable via the server
// struct after the in-memory session ends. (The stdio Run() path's exit
// is exercised end-to-end by the CLI itself; here we just confirm the
// API surface that sub-task B will consume.)
func TestServerRun_OutcomeSurfaced(t *testing.T) {
	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{})
	defer done()

	_, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)

	// Outcome() must be callable any time and reflect the latest state.
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv.Outcome())
	// String() round-trips for logging.
	assert.Equal(t, "success", srv.Outcome().String())
}

// TestValidator_OutcomeStringRendering — defensive: the Outcome enum
// renders human-readable strings used in log lines and the CLI's exit
// banner.
func TestValidator_OutcomeStringRendering(t *testing.T) {
	assert.Equal(t, "success", kitsokimcp.OutcomeSuccess.String())
	assert.Equal(t, "retries_exhausted", kitsokimcp.OutcomeRetriesExhausted.String())
	assert.Equal(t, "abandoned", kitsokimcp.OutcomeAbandoned.String())
	assert.Equal(t, "unknown", kitsokimcp.OutcomeUnknown.String())
}

// TestValidator_PostCmdSubmittedJSONFile — verify that the file pointed
// to by --submitted-json contains the actual submitted payload (not the
// envelope, not the prompt-rendered text).
func TestValidator_PostCmdSubmittedJSONFile(t *testing.T) {
	// A self-validating script: read the submitted-json file, check it
	// has the expected fields, exit accordingly.
	script := filepath.Join(t.TempDir(), "verify.sh")
	body := `#!/usr/bin/env bash
set -u
file=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --submitted-json) file="$2"; shift 2 ;;
    *) shift ;;
  esac
done
if [[ -z "$file" ]]; then
  echo "no --submitted-json arg" >&2
  exit 2
fi
if ! grep -q "double-Close" "$file"; then
  echo "submitted-json missing expected payload (got: $(cat "$file"))" >&2
  exit 3
fi
exit 0
`
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))

	cs, srv, done := connectValidatorWithCfg(t, kitsokimcp.ValidatorConfig{
		PostCmd: script,
	})
	defer done()

	res, err := cs.CallTool(context.Background(), &mcpsdk.CallToolParams{
		Name:      "submit",
		Arguments: validPayload(),
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "verifier should accept the payload")
	assert.Equal(t, kitsokimcp.OutcomeSuccess, srv.Outcome())
}
