// Package host — shared CLI exec seam.
//
// Shared CLI exec seam used by git/cpt/local-test handlers;
// centralised here so per-handler mocking doesn't require importing
// git_vcs.go.  Previously this seam lived inside git_vcs.go under a
// vcs-prefixed name, but the name was misleading — non-vcs handlers
// (local_ci, github, cypilot_artifacts) also dispatch through the
// same variable.
//
// Production code wires `cliExec` to runRealCommand at package init.
// Tests substitute their own runner via SetExecRunnerForTest (defined
// here too).
package host

import (
	"context"
)

type cliExecEnvKey struct{}

// execRunner is the function signature used to run external commands.
// Production code wires it to runRealCommand; tests substitute their
// own via SetExecRunnerForTest.
type execRunner func(ctx context.Context, dir, name string, args ...string) (stdout string, stderr string, exitCode int, err error)

// cliExec is the active runner; tests swap it out via
// SetExecRunnerForTest.  All host handlers that shell out — git,
// cpt, local-test — dispatch through this single seam so mocking is
// uniform.
var cliExec execRunner = runRealCommand

// SetExecRunnerForTest installs a fake runner.  Returns a restore func.
// Test-only — production code calls runRealCommand directly via
// cliExec.
func SetExecRunnerForTest(r execRunner) func() {
	prev := cliExec
	cliExec = r
	return func() { cliExec = prev }
}

// WithCLIExecEnv returns a child context carrying per-command environment
// overrides for host CLI calls.
func WithCLIExecEnv(ctx context.Context, env map[string]string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	copied := make(map[string]string, len(env))
	for k, v := range env {
		copied[k] = v
	}
	return context.WithValue(ctx, cliExecEnvKey{}, copied)
}

// CLIExecEnvFromCtx returns the per-command environment overrides installed by
// WithCLIExecEnv, or nil when no overrides are installed.
func CLIExecEnvFromCtx(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	env, _ := ctx.Value(cliExecEnvKey{}).(map[string]string)
	if len(env) == 0 {
		return nil
	}
	copied := make(map[string]string, len(env))
	for k, v := range env {
		copied[k] = v
	}
	return copied
}

func envWithCLIExec(env []string, override map[string]string) []string {
	return envWithProvider(env, override)
}
