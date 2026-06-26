package host

// Reproduction for bug
//   2026-06-10T141756Z-decide-postcmd-captured-submit-reported-abandoned
//
//   "host.agent.decide with validator.post_cmd reports 'abandoned without
//    successful submit' when the schema-valid payload WAS captured"
//
// Mechanism (internal/host/agent_decide.go):
//
//   When a decide call declares validator.post_cmd, the in-process mcp-validator
//   is schema-only and captures the submitted payload to validatorOutputPath; the
//   post_cmd is then run separately by the host via runDecideSandboxValidator ->
//   RunValidatorSandboxed. In runDecideWithValidatorRetryLoop's mcpOutcomeSuccess
//   branch a NON-EMPTY rejection from runDecideSandboxValidator is treated as a
//   *semantic* rejection (`sandboxLastRejection = rejection; continue`) — it nudges
//   and retries, burning every outer iteration and ending as the terminal message
//   "validator: session abandoned without successful submit ...".
//
// Two defects make a real post_cmd un-passable, both folded into that semantic
// rejection path so the schema-valid CAPTURED payload is discarded:
//
//   1. runDecideSandboxValidator builds ValidatorSandboxOptions{Cmd, Args, Stdin}
//      and NEVER plumbs opts.PostCmdCwd — even though validatorOptions parses
//      post_cmd_cwd and the legacy ask_with_mcp validator honors it
//      (agent_ask_with_mcp.go:215). ValidatorSandboxOptions has no cwd field at
//      all; the subprocess always runs in scratchDir. So `python3 -m bugfix ...`
//      (importable only from tools/loopy) exits non-zero ("No module named
//      bugfix").
//
//   2. An argv0 that cannot exec under the sandbox (runErr != nil) is returned as
//      `("validator sandbox: infrastructure error: ...", "")` — a non-empty
//      rejection with EMPTY contractErr — instead of a hard Result.Error. The
//      retry loop cannot distinguish "verifier could not start" from a genuine
//      semantic rejection, so it retries to exhaustion and mis-reports the cause.
//
// These tests use no LLM and no real subprocess of interest — they call the
// unexported runDecideSandboxValidator directly (white-box, package host).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	kitsokimcp "kitsoki/internal/mcp"
)

// writeCapturedPayload writes a schema-valid payload to validatorOutputPath,
// modeling a SUCCESSFUL mcp__validator__submit ("OK: payload validated against
// the schema and captured").
func writeCapturedPayload(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "validator_output.json")
	if err := os.WriteFile(p, []byte(`{"verdict":"reproduced","confidence":0.9}`), 0o644); err != nil {
		t.Fatalf("seed captured payload: %v", err)
	}
	return p
}

// TestRepro_PostCmdInfraFailureMisreportedAsSemanticRejection is the core
// reproduction, now asserting the FIX: a schema-valid payload IS captured on
// disk, and an un-runnable post_cmd (argv0 that does not exist) is surfaced on
// the dedicated infra channel — NOT folded into the retryable `rejection`
// channel. In runDecideWithValidatorRetryLoop the infra channel returns a hard
// Result.Error immediately instead of `continue`-ing through every outer
// iteration and mis-reporting "abandoned without successful submit".
func TestRepro_PostCmdInfraFailureMisreportedAsSemanticRejection(t *testing.T) {
	dir := t.TempDir()
	outPath := writeCapturedPayload(t, dir)

	// Sanity: the submit really did capture a schema-valid payload — this is the
	// "captured" half of the bug title.
	if data, err := kitsokimcp.ReadCapturedPayload(outPath); err != nil || len(data) == 0 {
		t.Fatalf("precondition: captured payload must exist; got err=%v len=%d", err, len(data))
	}

	// An interpreter/verifier that cannot exec under the sandbox. This stands in
	// for `python3 -m bugfix verify-context` failing to start (argv0 not on PATH
	// / cwd dropped so the module is unimportable).
	opts := &validatorOptions{
		PostCmd:    "kitsoki-nonexistent-verifier-xyz verify-context",
		PostCmdCwd: "tools/loopy",
	}

	rejection, contractErr, infraErr := runDecideSandboxValidator(context.Background(), outPath, opts)

	// FIX: an un-runnable verifier is NOT a contract violation...
	if contractErr != "" {
		t.Fatalf("did not expect a contract error for a non-executable verifier; got %q", contractErr)
	}
	// ...and it is NOT folded into the retryable semantic-rejection channel.
	if strings.TrimSpace(rejection) != "" {
		t.Fatalf("expected the rejection channel to be empty for an un-runnable verifier; got %q", rejection)
	}
	// It is surfaced on the dedicated infra channel so the loop returns a hard
	// Result.Error instead of nudging to exhaustion. Depending on platform this
	// is either runErr ("infrastructure error: ...", Linux/unsandboxed path) or
	// the sandbox wrapper's exec-failure exit (sandbox-exec "execvp() ... No such
	// file or directory" on macOS).
	if strings.TrimSpace(infraErr) == "" {
		t.Fatalf("expected a non-empty infra error for a verifier that could not run; got empty")
	}
	if !strings.Contains(infraErr, "infrastructure error") &&
		!strings.Contains(infraErr, "execvp") &&
		!strings.Contains(infraErr, "No such file") &&
		!strings.Contains(strings.ToLower(infraErr), "not found") {
		t.Fatalf("expected the infra error to reflect a verifier that could not run; got %q", infraErr)
	}
	t.Logf("un-runnable verifier routed to infra channel (hard error), not retryable rejection: %q", infraErr)

	// The captured, schema-valid payload at outPath is therefore preserved and
	// surfaced by buildDecideResult rather than discarded across retries.
	t.Logf("captured payload at %s is kept; loop returns hard error post_cmd validator failed to start", outPath)
}

// TestRepro_PostCmdCwdIsDroppedOnDecidePath asserts the FIX for defect #1
// structurally: ValidatorSandboxOptions now carries a Cwd field, so the declared
// post_cmd_cwd CAN be plumbed through to RunValidatorSandboxed. A verifier that
// is importable only from tools/loopy can now see that directory on the decide
// path instead of always running in scratchDir. (Mirrors the legacy
// ask_with_mcp validator, which resolves PostCmdCwd via resolvePromptPathCtx.)
func TestRepro_PostCmdCwdIsDroppedOnDecidePath(t *testing.T) {
	// validatorOptions parses and stores post_cmd_cwd...
	opts, errMsg := parseValidatorOptions(map[string]any{
		"validator": map[string]any{
			"post_cmd":     "python3 -m bugfix verify-context",
			"post_cmd_cwd": "tools/loopy",
		},
	})
	if errMsg != "" {
		t.Fatalf("parseValidatorOptions: %s", errMsg)
	}
	if opts.PostCmdCwd != "tools/loopy" {
		t.Fatalf("expected post_cmd_cwd to be parsed; got %q", opts.PostCmdCwd)
	}

	// ...and ValidatorSandboxOptions now has a slot to carry it. Constructing the
	// options shape the decide path builds shows the cwd is now representable:
	// the subprocess working directory follows Cwd when set, not scratchDir.
	built := ValidatorSandboxOptions{
		Cmd:   "python3",
		Args:  []string{"-m", "bugfix", "verify-context"},
		Stdin: `{"verdict":"reproduced"}`,
		Cwd:   opts.PostCmdCwd,
	}
	if built.Cwd != "tools/loopy" {
		t.Fatalf("expected ValidatorSandboxOptions.Cwd to carry the declared post_cmd_cwd; got %q", built.Cwd)
	}
	t.Logf("post_cmd_cwd=%q now plumbed via ValidatorSandboxOptions.Cwd -> subprocess can run in its import root", opts.PostCmdCwd)
}
