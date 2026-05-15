// Package host — internal helpers shared between the one-shot oracle
// handlers (host.oracle.ask, host.oracle.ask_with_mcp). Both run the
// claude CLI once with a rendered prompt piped on stdin and normalize
// exit / context errors the same way; this file is the seam.
package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ClaudeRun is the outcome of one `claude -p` invocation.
type ClaudeRun struct {
	// Stdout has its trailing newline trimmed.
	Stdout string
	// Stderr is left raw (untrimmed) so callers can format messages.
	Stderr   string
	ExitCode int
	// Infra is non-nil iff the process failed for a reason other than
	// producing an exit code (e.g. binary vanished mid-run). Callers
	// surface this through Result.Error rather than a Go error so
	// on_error: routing stays deterministic.
	Infra error
}

// ClaudeRunner runs the claude binary one-shot and returns its
// outcome. Production wires the real exec via runClaudeOneShotReal;
// tests inject an in-process stub via WithClaudeRunner so the
// package's ~180 tests can run without forking 180 bash subprocesses.
type ClaudeRunner func(ctx context.Context, args []string, stdin, workingDir string) (ClaudeRun, error)

type claudeRunnerCtxKey struct{}

// WithClaudeRunner returns a child context that uses r for every
// runClaudeOneShot invocation reached through it. Tests use this to
// install in-process stubs. Production never calls WithClaudeRunner.
func WithClaudeRunner(ctx context.Context, r ClaudeRunner) context.Context {
	return context.WithValue(ctx, claudeRunnerCtxKey{}, r)
}

// ClaudeRunnerFromContext returns the runner installed in ctx, or
// nil if none is installed (the real-exec path is used).
func ClaudeRunnerFromContext(ctx context.Context) ClaudeRunner {
	r, _ := ctx.Value(claudeRunnerCtxKey{}).(ClaudeRunner)
	return r
}

// runClaudeOneShot dispatches a claude one-shot call. When a
// ClaudeRunner is installed in ctx (test setup) the stub is invoked
// in-process. Otherwise the real exec path runClaudeOneShotReal forks
// the binary as before.
//
// The trailing-newline trim on Stdout is enforced here, not in the
// runner implementations. The ClaudeRun.Stdout field is documented to
// have its trailing newline trimmed; the real exec path (and any
// stub) might naively return raw output. Enforce the contract at the
// dispatcher so callers (and downstream code that splits on \n) never
// see drift between exec and stubbed paths.
func runClaudeOneShot(ctx context.Context, bin string, cliArgs []string, stdin, workingDir string) (ClaudeRun, error) {
	var (
		cr  ClaudeRun
		err error
	)
	if r := ClaudeRunnerFromContext(ctx); r != nil {
		cr, err = r(ctx, cliArgs, stdin, workingDir)
	} else {
		cr, err = runClaudeOneShotReal(ctx, bin, cliArgs, stdin, workingDir)
	}
	cr.Stdout = strings.TrimRight(cr.Stdout, "\n")
	return cr, err
}

// runClaudeOneShotReal executes the claude CLI with the given args, prompt
// piped on stdin, and working directory. Context cancellation is
// returned as a Go error; all other failures populate the ClaudeRun
// fields.
//
// Env: the kitsoki binary's own directory is prepended to PATH so
// agents whose tool surface includes Bash patterns that invoke
// `kitsoki <subcommand>` (e.g. `Bash(kitsoki bug create*)` on the
// bug-reporter agent) actually find the binary. Without this,
// `kitsoki` is only on PATH when the user has run `go install` or
// otherwise placed it there — which `go run ./cmd/kitsoki ...`
// users don't do, and the subprocess fails with "command not found".
func runClaudeOneShotReal(ctx context.Context, bin string, cliArgs []string, stdin, workingDir string) (ClaudeRun, error) {
	cmd := exec.CommandContext(ctx, bin, cliArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Dir = workingDir
	cmd.Env = envWithKitsokiBinOnPath(os.Environ())

	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se

	runErr := cmd.Run()
	out := strings.TrimRight(so.String(), "\n")
	if runErr != nil {
		if ctx.Err() != nil {
			return ClaudeRun{}, ctx.Err()
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			return ClaudeRun{Stdout: out, Stderr: se.String(), ExitCode: exitErr.ExitCode()}, nil
		}
		return ClaudeRun{Stdout: out, Stderr: se.String(), Infra: runErr}, nil
	}
	return ClaudeRun{Stdout: out, Stderr: se.String()}, nil
}

// claudeExitErrorMessage builds the Result.Error string for a non-zero
// claude exit, preferring stderr text, then stdout, then a fallback.
func claudeExitErrorMessage(exitCode int, stderr, stdout string) string {
	if s := strings.TrimSpace(stderr); s != "" {
		return s
	}
	if stdout != "" {
		return stdout
	}
	return fmt.Sprintf("claude exited with code %d", exitCode)
}

// envWithKitsokiBinOnPath returns a copy of env with the kitsoki
// binary's directory prepended to PATH. Idempotent: if PATH already
// starts with that directory the env is returned unchanged.
//
// The directory comes from os.Executable(). Under `go run` this is
// a per-invocation temp build artefact (e.g.
// /tmp/go-build3210293/.../exe/kitsoki) whose basename is "kitsoki"
// — exactly what an agent prompt that says `kitsoki bug create …`
// resolves. Under `go install` or a packaged build it's the
// install/release path; same shape.
//
// On platforms where os.Executable() fails (rare; unsupported OS or
// the parent removed the exe file) the function returns env
// unchanged. The downstream "command not found" error remains
// informative.
func envWithKitsokiBinOnPath(env []string) []string {
	self, err := os.Executable()
	if err != nil || self == "" {
		return env
	}
	dir := filepath.Dir(self)
	if dir == "" || dir == "." {
		return env
	}
	sep := string(os.PathListSeparator)
	prefix := dir + sep
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			found = true
			existing := strings.TrimPrefix(kv, "PATH=")
			if existing == dir || strings.HasPrefix(existing, prefix) {
				// Already first — no need to touch.
				out = append(out, kv)
				continue
			}
			out = append(out, "PATH="+dir+sep+existing)
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+dir)
	}
	return out
}

// resolveOracleBin returns the path to the claude binary, honoring
// OracleBinEnv and falling back to a PATH lookup. Returns
// ErrOracleUnavailable if neither is set.
//
// When a ClaudeRunner is installed in ctx (tests) the function
// short-circuits with a sentinel path because the runner stub does
// not need a real binary on disk.
func resolveOracleBin(ctx context.Context) (string, error) {
	if ClaudeRunnerFromContext(ctx) != nil {
		return "stub://claude", nil
	}
	if bin := os.Getenv(OracleBinEnv); bin != "" {
		return bin, nil
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", ErrOracleUnavailable
	}
	return path, nil
}
