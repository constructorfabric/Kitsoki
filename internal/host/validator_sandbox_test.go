package host_test

// validator_sandbox_test.go — tests for M2: RunValidatorSandboxed fallback
// behavior when unshare is unavailable or denied.

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// TestValidatorSandbox_FallsBackAndWarnsWhenUnshareDenied verifies that when
// `unshare` is not available (simulated by placing a fake unshare that exits 1
// ahead of the real one on PATH), RunValidatorSandboxed:
//  1. Runs the subprocess unsandboxed (no infrastructure error).
//  2. Emits a slog.Warn with "network not isolated" so the operator knows
//     sandbox is unavailable.
//
// Note: cannot use t.Parallel() because t.Setenv and slog.SetDefault are
// not goroutine-safe across parallel tests.
func TestValidatorSandbox_FallsBackAndWarnsWhenUnshareDenied(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("unshare fallback test only relevant on Linux")
	}

	// Build a fake "unshare" that always exits 1 (simulating kernel denial).
	fakeDir := t.TempDir()
	fakeUnshare := filepath.Join(fakeDir, "unshare")
	// Write a shell script that exits 1.
	script := "#!/bin/sh\nexit 1\n"
	if err := os.WriteFile(fakeUnshare, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake unshare: %v", err)
	}

	// Capture slog output by installing a handler that appends to a buffer.
	var logBuf bytes.Buffer
	handler := slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelWarn})
	ctx := context.Background()
	// We can't easily replace the global slog logger in a parallel test; instead,
	// we modify PATH to put the fake unshare first and rely on the warn landing
	// in the default handler. Instead, intercept the warn by using a slog.Logger
	// passed via context. But RunValidatorSandboxed uses slog.WarnContext which
	// respects the context's logger if one is set via slog.NewLogLogger or similar.
	//
	// Since the standard library's slog package doesn't support injecting a logger
	// via context, we use the global slog.SetDefault approach. To avoid races in
	// parallel tests, use t.Setenv to restore PATH and use a goroutine to capture.
	//
	// Approach: rely on slog.SetDefault for the duration of this test only.
	// Use testLogCapturer which captures from the default handler.
	_ = handler
	_ = logBuf

	// Override PATH so the fake unshare appears first.
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", fakeDir+string(os.PathListSeparator)+origPath)

	// Use a slogCapture to intercept warnings. Since slog.WarnContext uses the
	// global default logger, we temporarily swap it.
	captured := &warnCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(captured))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Run a simple true-ish command (the actual subprocess). On Linux without
	// sandbox, it should still succeed via the fallback (unsandboxed).
	res, err := host.RunValidatorSandboxed(ctx, host.ValidatorSandboxOptions{
		Cmd:  "/bin/true",
		Args: nil,
	})
	if err != nil {
		t.Fatalf("unexpected infra error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("subprocess should succeed: exit %d, stderr=%q", res.ExitCode, res.Stderr)
	}

	// The warn must have been emitted.
	if !captured.hasMsg("network not isolated") {
		t.Fatalf("expected 'network not isolated' warn; got: %v", captured.msgs)
	}
}

// TestValidatorSandbox_UnsafeNoSandbox_Passes verifies that UnsafeNoSandbox:true
// runs the subprocess directly (no warn emitted on any platform).
func TestValidatorSandbox_UnsafeNoSandbox_Passes(t *testing.T) {
	t.Parallel()
	res, err := host.RunValidatorSandboxed(context.Background(), host.ValidatorSandboxOptions{
		Cmd:             trueBin(t),
		UnsafeNoSandbox: true,
	})
	if err != nil {
		t.Fatalf("unexpected error with UnsafeNoSandbox: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit 0; got %d", res.ExitCode)
	}
}

// trueBin resolves the platform's `true` binary via PATH. macOS ships it at
// /usr/bin/true (there is no /bin/true), so a hardcoded /bin/true is not
// portable; exec.LookPath finds it on every platform the suite runs on.
func trueBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no `true` binary on PATH: %v", err)
	}
	return p
}

// warnCapture is a slog.Handler that records Warn-level messages.
type warnCapture struct {
	msgs []string
}

func (w *warnCapture) Enabled(_ context.Context, l slog.Level) bool {
	return l >= slog.LevelWarn
}

func (w *warnCapture) Handle(_ context.Context, r slog.Record) error {
	w.msgs = append(w.msgs, r.Message)
	return nil
}

func (w *warnCapture) WithAttrs(attrs []slog.Attr) slog.Handler { return w }
func (w *warnCapture) WithGroup(name string) slog.Handler       { return w }

func (w *warnCapture) hasMsg(substr string) bool {
	for _, m := range w.msgs {
		if strings.Contains(m, substr) {
			return true
		}
	}
	return false
}
