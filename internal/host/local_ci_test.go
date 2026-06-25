package host_test

import (
	"context"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func TestLocalCI_RegisteredAsBuiltin(t *testing.T) {
	r := host.NewRegistry()
	host.RegisterBuiltins(r)
	for _, name := range []string{
		"host.local",
		"host.local.run_tests",
		"host.local.build",
		"host.local.remote_status",
	} {
		if _, ok := r.Get(name); !ok {
			t.Fatalf("registry: %s missing", name)
		}
	}
}

func TestLocalCI_MissingOp(t *testing.T) {
	res, err := host.LocalCIHandler(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error for missing op")
	}
}

func TestLocalCI_RemoteStatusNotSupported(t *testing.T) {
	res, err := host.LocalCIHandler(context.Background(), map[string]any{
		"op":    "remote_status",
		"pr_id": "1",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected domain error: local CI has no remote")
	}
}

func TestLocalCI_RunTests_DefaultGoTest(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["go test ./..."] = fakeResp{stdout: "--- PASS: TestA (0.01s)\n--- PASS: TestB (0.01s)\nPASS\n"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.LocalCIHandler(context.Background(), map[string]any{"op": "run_tests"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
	if res.Data["passed"] != 2 {
		t.Fatalf("passed: %v", res.Data["passed"])
	}
	if res.Data["failed"] != 0 {
		t.Fatalf("failed: %v", res.Data["failed"])
	}
}

func TestLocalCI_RunTests_Override(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["pytest -x"] = fakeResp{stdout: "ok"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.LocalCIHandler(context.Background(), map[string]any{
		"op":       "run_tests",
		"test_cmd": "pytest -x",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if !strings.Contains(strings.Join(fr.calls, "\n"), "pytest -x") {
		t.Fatalf("override not honored: %v", fr.calls)
	}
}

// A test_cmd with a leading POSIX env-prefix (`NAME=VALUE cmd`) must be
// routed through `env`, not fork/exec'd as a binary literally named
// `NAME=VALUE`.  Regression for the gears-rust bake-off finding where
// `CARGO_TARGET_DIR=... cargo test` bounced run_tests to idle with
// "no such file or directory".
func TestLocalCI_RunTests_EnvPrefixRoutedThroughEnv(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["env CARGO_TARGET_DIR=/tmp/t cargo test"] = fakeResp{stdout: "ok"}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.LocalCIHandler(context.Background(), map[string]any{
		"op":       "run_tests",
		"test_cmd": "CARGO_TARGET_DIR=/tmp/t cargo test",
	})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	got := strings.Join(fr.calls, "\n")
	if !strings.HasPrefix(got, "env CARGO_TARGET_DIR=/tmp/t cargo test") {
		t.Fatalf("env-prefix not routed through env: %v", fr.calls)
	}
}

func TestLocalCI_RunTests_FailureReportsFailedCount(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["go test ./..."] = fakeResp{
		stdout: "--- PASS: TestA (0.01s)\n--- FAIL: TestB (0.01s)\nFAIL\n",
		code:   1,
	}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.LocalCIHandler(context.Background(), map[string]any{"op": "run_tests"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != false {
		t.Fatalf("ok should be false: %v", res.Data["ok"])
	}
	if res.Data["passed"] != 1 || res.Data["failed"] != 1 {
		t.Fatalf("counts: passed=%v failed=%v", res.Data["passed"], res.Data["failed"])
	}
}

func TestLocalCI_Build_Happy(t *testing.T) {
	fr := newFakeRunner()
	fr.responses["go build ./..."] = fakeResp{}
	restore := host.SetExecRunnerForTest(fr.run)
	defer restore()

	res, err := host.LocalCIHandler(context.Background(), map[string]any{"op": "build"})
	if err != nil {
		t.Fatalf("infra: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("domain: %s", res.Error)
	}
	if res.Data["ok"] != true {
		t.Fatalf("ok: %v", res.Data["ok"])
	}
}
