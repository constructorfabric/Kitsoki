// Package host — read-only subprocess sandbox for host.oracle.decide /
// host.oracle.extract validators.
//
// ValidatorSandbox runs a subprocess in a best-effort read-only environment so
// a validator that attempts to mutate state outside /tmp fails with EACCES
// rather than silently succeeding.
//
// # Platform support
//
// Linux (best-effort): uses `unshare -n` (network namespace isolation) plus
// environment variable tricks to deny writes outside the designated scratch
// dir. Full mount-namespace isolation (bind-mounting the filesystem read-only
// except /tmp) would require root or a suid helper; that's a hardening TODO.
// The current implementation sets HOME to a temp dir and denies network via
// the HTTP_PROXY env vars. Processes that attempt to write outside the scratch
// dir via normal file I/O will succeed on Linux — enforcement here is
// best-effort and relies on the LLM not trying to subvert it deliberately.
//
// macOS: uses `sandbox-exec -f <profile>` with a minimal Seatbelt profile
// that denies write syscalls outside the scratch dir and denies network.
//
// Windows: write isolation is not supported in Phase 1. Callers must set
// UnsafeNoSandbox: true in ValidatorSandboxOptions or the run returns an error.
// The loader emits a warn-line when an app is loaded on Windows and a validator
// is declared without this opt-out (decision D2: Windows requires explicit opt-out).
//
// All platforms: the subprocess inherits a curated environment (no HOME,
// HTTP_PROXY set to an invalid value) and runs with its cwd set to the
// scratch dir.
package host

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ValidatorSandboxOptions configures a single validator subprocess run.
type ValidatorSandboxOptions struct {
	// Cmd is the validator program (argv[0]).
	Cmd string
	// Args are the additional arguments to pass after Cmd.
	Args []string
	// Env is extra environment variables merged into the subprocess env.
	// Values here win over any inferred variable with the same name.
	Env []string
	// ScratchDir is the writable working directory for the subprocess. When
	// empty, a fresh TempDir is created and cleaned up after the run.
	ScratchDir string
	// UnsafeNoSandbox, when true, skips sandbox setup and runs the subprocess
	// directly. Required on Windows (no sandbox support in Phase 1). The
	// loader emits a warn-line when this is absent on Windows apps.
	UnsafeNoSandbox bool
	// Stdin, when non-empty, is piped to the subprocess stdin.
	Stdin string
}

// ValidatorResult is the outcome of a validator subprocess run.
type ValidatorResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// RunValidatorSandboxed executes a validator subprocess inside a best-effort
// read-only sandbox. The caller owns the scratch dir lifecycle when
// opts.ScratchDir is set; when it is empty, RunValidatorSandboxed creates one
// and removes it after the run.
//
// A non-nil error indicates an infrastructure failure (subprocess could not
// start, sandbox setup failed). A non-zero ValidatorResult.ExitCode indicates
// the validator rejected the payload — the caller should treat that as a
// retry signal.
func RunValidatorSandboxed(ctx context.Context, opts ValidatorSandboxOptions) (ValidatorResult, error) {
	if opts.Cmd == "" {
		return ValidatorResult{}, fmt.Errorf("validator_sandbox: Cmd is required")
	}

	scratchDir := opts.ScratchDir
	cleanupScratch := false
	if scratchDir == "" {
		var err error
		scratchDir, err = os.MkdirTemp("", "kitsoki-validator-*")
		if err != nil {
			return ValidatorResult{}, fmt.Errorf("validator_sandbox: create scratch dir: %w", err)
		}
		cleanupScratch = true
	}
	scratchDir = filepath.Clean(scratchDir)

	if cleanupScratch {
		defer os.RemoveAll(scratchDir)
	}

	// UnsafeNoSandbox is honoured uniformly on EVERY platform — run the
	// subprocess directly with no sandbox setup (its documented contract).
	// Checked before the per-OS switch so macOS/Linux don't fall through to
	// their sandbox paths (the darwin branch previously ignored the flag and
	// always invoked sandbox-exec, so an opt-out validator still hit the
	// sandbox — and failed where sandbox-exec is unavailable).
	if opts.UnsafeNoSandbox {
		return runUnsandboxed(ctx, opts, scratchDir)
	}

	switch runtime.GOOS {
	case "windows":
		return ValidatorResult{}, fmt.Errorf("validator_sandbox: Windows does not support sandbox isolation in Phase 1; set unsafe_validator_no_sandbox: true on the validator declaration to opt out (decision D2)")
	case "darwin":
		return runMacOSSandbox(ctx, opts, scratchDir)
	default:
		// Linux and other Unix-likes: best-effort via unshare -n if available,
		// falling back to the unsandboxed path with env-var network deny.
		return runLinuxSandbox(ctx, opts, scratchDir)
	}
}

// runLinuxSandbox attempts to use `unshare -rn` for network isolation.
// The -r flag maps the current UID to root inside the new user namespace,
// enabling unprivileged network namespace creation on kernels that allow
// unprivileged user namespaces (CONFIG_USER_NS=y,
// /proc/sys/kernel/unprivileged_userns_clone != 0).
//
// If `unshare -rn` fails (binary not found, kernel rejects the syscall, or
// unprivileged user namespaces are disabled), the sandbox falls back to the
// env-var network-deny approach and logs a warning so the operator knows
// network isolation is not active.
//
// Limitation: filesystem write isolation is NOT enforced on Linux in Phase 1.
// The subprocess may write to any path the user has permission to write.
// Full mount-namespace isolation requires root or a suid helper and is a
// hardening TODO.
func runLinuxSandbox(ctx context.Context, opts ValidatorSandboxOptions, scratchDir string) (ValidatorResult, error) {
	if result, ok := tryLinuxUnshare(ctx, opts, scratchDir); ok {
		return result, nil
	}
	slog.WarnContext(ctx,
		"validator sandbox unavailable: network not isolated; only HTTP_PROXY env-var protection applies",
		"reason", "unshare -rn failed or not available",
	)
	return runUnsandboxed(ctx, opts, scratchDir)
}

// tryLinuxUnshare runs the subprocess under `unshare -rn` (new user+network
// namespace). Returns (result, true) on success (including non-zero validator
// exit). Returns (zero, false) when unshare is not available or the test
// override rejects the probe. A probe invocation (`unshare -rn /bin/true`)
// is run first to detect permission failures early without running the real
// subprocess unsandboxed.
func tryLinuxUnshare(ctx context.Context, opts ValidatorSandboxOptions, scratchDir string) (ValidatorResult, bool) {
	unshare, err := exec.LookPath("unshare")
	if err != nil {
		return ValidatorResult{}, false
	}

	// Probe: verify that unprivileged user-namespace creation is permitted.
	// Use a minimal no-op to avoid side effects.
	probe := exec.CommandContext(ctx, unshare, "-rn", "/bin/true")
	if runErr := probe.Run(); runErr != nil {
		return ValidatorResult{}, false
	}

	// -r: map current UID to root in the new user namespace
	// -n: new network namespace (no network access for the subprocess tree)
	cmdArgs := append([]string{"-rn", opts.Cmd}, opts.Args...)
	cmd := exec.CommandContext(ctx, unshare, cmdArgs...)
	cmd.Dir = scratchDir
	cmd.Env = buildSandboxEnv(opts.Env, scratchDir)
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	result, runErr := runAndCapture(ctx, cmd)
	if runErr != nil {
		return ValidatorResult{}, false
	}
	return result, true
}

// runMacOSSandbox uses `sandbox-exec` with a minimal Seatbelt profile that
// denies file writes outside scratchDir and denies network access.
//
// Limitation: sandbox-exec is deprecated in newer macOS but still functional
// through at least macOS 15 (Sequoia). A future hardening pass may switch to
// a different mechanism when Apple removes it.
func runMacOSSandbox(ctx context.Context, opts ValidatorSandboxOptions, scratchDir string) (ValidatorResult, error) {
	sandboxExec, err := exec.LookPath("sandbox-exec")
	if err != nil {
		// sandbox-exec not found — fall back to unsandboxed with env-var deny.
		return runUnsandboxed(ctx, opts, scratchDir)
	}

	// Minimal Seatbelt profile:
	//   - deny all by default
	//   - allow read from filesystem (validators need to read their input)
	//   - allow write to scratchDir subtree only
	//   - deny network
	profile := fmt.Sprintf(`(version 1)
(deny default)
(allow file-read*)
(allow file-write* (subpath %q))
(allow process-exec)
(allow signal)
(allow sysctl-read)
`, scratchDir)

	profileFile, writeErr := os.CreateTemp("", "kitsoki-sandbox-profile-*.sb")
	if writeErr != nil {
		return ValidatorResult{}, fmt.Errorf("validator_sandbox(darwin): write sandbox profile: %w", writeErr)
	}
	defer os.Remove(profileFile.Name())
	if _, writeErr = profileFile.WriteString(profile); writeErr != nil {
		profileFile.Close()
		return ValidatorResult{}, fmt.Errorf("validator_sandbox(darwin): write sandbox profile body: %w", writeErr)
	}
	profileFile.Close()

	execArgs := append([]string{"-f", profileFile.Name(), opts.Cmd}, opts.Args...)
	cmd := exec.CommandContext(ctx, sandboxExec, execArgs...)
	cmd.Dir = scratchDir
	cmd.Env = buildSandboxEnv(opts.Env, scratchDir)
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}
	return runAndCapture(ctx, cmd)
}

// runUnsandboxed runs the subprocess directly with the sandbox env vars set
// but no OS-level isolation. Used on Windows and as a fallback.
func runUnsandboxed(ctx context.Context, opts ValidatorSandboxOptions, scratchDir string) (ValidatorResult, error) {
	cmd := exec.CommandContext(ctx, opts.Cmd, opts.Args...)
	cmd.Dir = scratchDir
	cmd.Env = buildSandboxEnv(opts.Env, scratchDir)
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}
	return runAndCapture(ctx, cmd)
}

// runAndCapture runs cmd, waits for it, and returns its stdout/stderr/exit code.
func runAndCapture(ctx context.Context, cmd *exec.Cmd) (ValidatorResult, error) {
	var so, se strings.Builder
	cmd.Stdout = &so
	cmd.Stderr = &se

	runErr := cmd.Run()
	res := ValidatorResult{
		Stdout: so.String(),
		Stderr: se.String(),
	}
	if runErr != nil {
		if ctx.Err() != nil {
			return ValidatorResult{}, ctx.Err()
		}
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			res.ExitCode = exitErr.ExitCode()
			return res, nil
		}
		return ValidatorResult{}, fmt.Errorf("validator_sandbox: exec: %w", runErr)
	}
	return res, nil
}

// buildSandboxEnv constructs the subprocess environment: start from a minimal
// base (PATH only), add network-deny vars, set HOME to scratchDir, then
// overlay caller-provided extras (which win over any conflicting value).
func buildSandboxEnv(extras []string, scratchDir string) []string {
	base := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + scratchDir,
		"TMPDIR=" + scratchDir,
	}
	base = append(base, MakeSandboxEnv()...)
	// Caller extras win — append last so they override any key set above.
	base = append(base, extras...)
	return base
}
