package host

// Unit coverage for the host.run transient-spawn-retry seam (hostRunForkRetries
// / isTransientSpawnError): a child that fails to *spawn* with a transient OS
// resource error (EAGAIN/ENOMEM) under fork load is retried, while a non-zero
// EXIT or a permanent spawn error (binary not found) is NOT — the root-cause
// guard for the punch-list studio flake (host.run → on_error → needs_human when
// a cassette-missed host.run shells out and fork transiently fails on a loaded
// runner).

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"testing"
)

func TestIsTransientSpawnError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"eagain", syscall.EAGAIN, true},
		{"enomem", syscall.ENOMEM, true},
		{"wrapped eagain", errors.New("fork/exec /usr/bin/python3: resource temporarily unavailable"), true},
		{"wrapped enomem", errors.New("fork/exec: cannot allocate memory"), true},
		{"exit error is not a spawn failure", &exec.ExitError{}, false},
		{"binary not found is permanent", errors.New(`exec: "nope": executable file not found in $PATH`), false},
		{"plain error", errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isTransientSpawnError(c.err); got != c.want {
				t.Fatalf("isTransientSpawnError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// TestRunHandler_PermanentSpawnErrorDoesNotLoop proves a permanent spawn failure
// (binary not found) returns promptly as an infra error rather than spinning
// through the transient-retry loop.
func TestRunHandler_PermanentSpawnErrorDoesNotLoop(t *testing.T) {
	res, err := RunHandler(context.Background(), map[string]any{
		"cmd":  "kitsoki-no-such-binary-xyz",
		"args": []any{"arg"},
	})
	if err == nil {
		t.Fatalf("expected an infra error for a missing binary, got result %+v", res)
	}
	if isTransientSpawnError(err) {
		t.Fatalf("missing-binary error must not be classified transient: %v", err)
	}
}
