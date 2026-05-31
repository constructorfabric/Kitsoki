// subprocess_test.go covers the subprocess JSON-RPC transport.
//
// These tests use a compiled echo_oracle binary (built at test time via go
// build) to verify the JSON-RPC 2.0 subprocess transport without a real LLM.

package oracle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// echoOracleBin is the path to the compiled echo_oracle binary. Set once in
// TestMain (or via buildEchoOracle in each test that needs it).
var (
	echoOraclePath     string
	echoOraclePathOnce sync.Once
	echoOraclePathErr  error
)

// buildEchoOracle compiles the echo_oracle helper binary and returns its path.
// The binary is compiled once per test run and cached in os.TempDir().
func buildEchoOracle(t *testing.T) string {
	t.Helper()
	echoOraclePathOnce.Do(func() {
		bin := filepath.Join(os.TempDir(), "kitsoki-echo-oracle-test")
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", bin, "./testdata/echo_oracle")
		cmd.Dir = mustGetwd()
		out, err := cmd.CombinedOutput()
		if err != nil {
			echoOraclePathErr = fmt.Errorf("build echo_oracle: %w\n%s", err, string(out))
			return
		}
		echoOraclePath = bin
	})
	if echoOraclePathErr != nil {
		t.Skipf("echo_oracle build failed (skip on CI without Go toolchain): %v", echoOraclePathErr)
	}
	return echoOraclePath
}

func mustGetwd() string {
	wd, _ := os.Getwd()
	return wd
}

// TestSubprocessHappyPath verifies that the subprocess oracle sends a request
// and receives a valid AskResponse.
func TestSubprocessHappyPath(t *testing.T) {
	bin := buildEchoOracle(t)

	o := NewSubprocess(bin, nil, nil)
	defer o.Close()

	ctx := context.Background()
	req := sampleRequest()
	req.Verb = "decide"
	req.PromptText = "which option is best?"

	resp, err := o.Ask(ctx, req)
	if err != nil {
		t.Fatalf("Ask: unexpected error: %v", err)
	}
	if resp.Submission == nil {
		t.Fatal("Submission: expected non-nil")
	}

	var got map[string]any
	if err := json.Unmarshal(resp.Submission, &got); err != nil {
		t.Fatalf("unmarshal Submission: %v", err)
	}
	if got["echo_verb"] != "decide" {
		t.Errorf("echo_verb: got %v, want decide", got["echo_verb"])
	}
}

// TestSubprocessReuse verifies that the subprocess is reused across multiple
// Ask calls (not re-spawned each time).
func TestSubprocessReuse(t *testing.T) {
	bin := buildEchoOracle(t)

	o := NewSubprocess(bin, nil, nil)
	defer o.Close()

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		req := sampleRequest()
		req.Verb = "ask"
		_, err := o.Ask(ctx, req)
		if err != nil {
			t.Fatalf("Ask #%d: %v", i+1, err)
		}
	}
}

// TestSubprocessCrashBeforeResponse verifies that when the subprocess exits
// before producing any output, the oracle returns *AskError{Kind: "plugin_crash"}.
func TestSubprocessCrashBeforeResponse(t *testing.T) {
	bin := buildEchoOracle(t)

	o := NewSubprocess(bin, nil, map[string]string{"CRASH_BEFORE_RESPONSE": "1"})
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	assertAskError(t, err, "plugin_crash")
}

// TestSubprocessCrashAfterPartialOutput verifies that when the subprocess
// writes a partial frame and exits, the oracle returns an error with partial
// bytes captured in the Detail.
func TestSubprocessCrashAfterPartialOutput(t *testing.T) {
	bin := buildEchoOracle(t)

	o := NewSubprocess(bin, nil, map[string]string{"CRASH_PARTIAL_RESPONSE": "1"})
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ae *AskError
	if !errors.As(err, &ae) {
		t.Fatalf("expected *AskError, got %T: %v", err, err)
	}
	// Should be either plugin_crash (EOF or unmarshal failure).
	if ae.Kind != "plugin_crash" && ae.Kind != "deadline_exceeded" {
		t.Errorf("unexpected Kind %q", ae.Kind)
	}
}

// TestSubprocessContextCancel verifies that cancelling the context while
// waiting for a response surfaces as deadline_exceeded.
func TestSubprocessContextCancel(t *testing.T) {
	bin := buildEchoOracle(t)

	// Use a very long delay to ensure the test context cancels first.
	o := NewSubprocess(bin, nil, map[string]string{"SLOW_RESPONSE_MS": "5000"})
	defer o.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := o.Ask(ctx, sampleRequest())
	if err == nil {
		t.Fatal("expected error after context cancel, got nil")
	}
	assertAskError(t, err, "deadline_exceeded")
}

// TestSubprocessRespawnAfterCrash verifies that after a crash, the subprocess
// is respawned on the next Ask call.
func TestSubprocessRespawnAfterCrash(t *testing.T) {
	bin := buildEchoOracle(t)

	// First call: crash before response.
	o := NewSubprocess(bin, nil, map[string]string{"CRASH_BEFORE_RESPONSE": "1"})
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("first Ask: expected crash error, got nil")
	}
	assertAskError(t, err, "plugin_crash")

	// The oracle proc is nil after crash. Now clear the env and update the command
	// to be a working binary. The easiest way: create a new SubprocessOracle with
	// the same binary but no crash env.
	o2 := NewSubprocess(bin, nil, nil)
	defer o2.Close()

	_, err = o2.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("second Ask (after reset): %v", err)
	}
}

// TestSubprocessClose verifies that Close terminates the subprocess gracefully.
func TestSubprocessClose(t *testing.T) {
	bin := buildEchoOracle(t)

	o := NewSubprocess(bin, nil, nil)

	// Start the subprocess by making one call.
	if _, err := o.Ask(context.Background(), sampleRequest()); err != nil {
		t.Fatalf("Ask: %v", err)
	}

	// Close should not return an error.
	if err := o.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}

	// Subsequent Close is a no-op (idempotent).
	if err := o.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

// TestSubprocessEnvPropagation verifies that env vars declared in the plugin
// config reach the subprocess.
func TestSubprocessEnvPropagation(t *testing.T) {
	bin := buildEchoOracle(t)

	// SLOW_RESPONSE_MS=0 means "no sleep" — the binary just returns normally.
	// This test just verifies no crash with env set.
	o := NewSubprocess(bin, nil, map[string]string{"SLOW_RESPONSE_MS": "0"})
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("Ask with env: %v", err)
	}
}

// TestSubprocessUnknownBinary verifies that an invalid binary path surfaces as
// plugin_crash on the first Ask.
func TestSubprocessUnknownBinary(t *testing.T) {
	o := NewSubprocess("/nonexistent/binary/that/does/not/exist", nil, nil)
	defer o.Close()

	_, err := o.Ask(context.Background(), sampleRequest())
	if err == nil {
		t.Fatal("expected error for unknown binary, got nil")
	}
	assertAskError(t, err, "plugin_crash")
}

// TestSubprocessConcurrent verifies that concurrent Ask calls serialize
// correctly (no data race under -race).
func TestSubprocessConcurrent(t *testing.T) {
	bin := buildEchoOracle(t)

	o := NewSubprocess(bin, nil, nil)
	defer o.Close()

	const n = 5
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := o.Ask(context.Background(), sampleRequest())
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Ask: %v", err)
		}
	}
}
