package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// fakeDemoRunner is the "cassette" for host.demo.* — records the script
// path/args it was invoked with and returns canned output, so these tests
// exercise argument wiring, script-root resolution and JSON parsing without
// spawning node or a browser (A2, use-case-loop-plan §3.5: "cassette these
// verbs").
type fakeDemoRunner struct {
	calls  []fakeDemoCall
	stdout []byte
	stderr []byte
	err    error
}

type fakeDemoCall struct {
	script string
	args   []string
}

func (f *fakeDemoRunner) Run(_ context.Context, scriptPath string, args []string, _ string) ([]byte, []byte, error) {
	f.calls = append(f.calls, fakeDemoCall{script: scriptPath, args: args})
	return f.stdout, f.stderr, f.err
}

func withFakeToolRoot(t *testing.T, scripts ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("// fixture"), 0o644); err != nil {
			t.Fatalf("seed fixture script %s: %v", name, err)
		}
	}
	t.Setenv(demoScriptsRootEnv, dir)
	return dir
}

func TestDemoHandler_UnknownOp(t *testing.T) {
	_, err := DemoHandler(context.Background(), map[string]any{"op": "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown op")
	}
}

func TestDemoHandler_Create(t *testing.T) {
	root := withFakeToolRoot(t, "create-mockup.mjs")
	fake := &fakeDemoRunner{stdout: []byte("wrote mockup\n")}
	defer WithDemoRunner(fake)()

	res, err := DemoHandler(context.Background(), map[string]any{
		"op":            "create",
		"scenario_path": "scenario.mockup.json",
		"out_path":      "out.html",
		"manifest":      true,
	})
	if err != nil {
		t.Fatalf("DemoHandler(create): %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 runner call, got %d", len(fake.calls))
	}
	call := fake.calls[0]
	wantScript := filepath.Join(root, "create-mockup.mjs")
	if call.script != wantScript {
		t.Fatalf("script path = %q, want %q", call.script, wantScript)
	}
	wantArgs := []string{"scenario.mockup.json", "out.html", "--manifest"}
	if len(call.args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", call.args, wantArgs)
	}
	for i, a := range wantArgs {
		if call.args[i] != a {
			t.Fatalf("args[%d] = %q, want %q", i, call.args[i], a)
		}
	}
}

func TestDemoHandler_Create_MissingArgs(t *testing.T) {
	_, err := DemoHandler(context.Background(), map[string]any{"op": "create"})
	if err == nil {
		t.Fatal("expected error for missing scenario_path/out_path")
	}
}

func TestDemoHandler_Record(t *testing.T) {
	withFakeToolRoot(t, "record-tour.mjs")
	fake := &fakeDemoRunner{stdout: []byte("captured 3 tours\n")}
	defer WithDemoRunner(fake)()

	res, err := DemoHandler(context.Background(), map[string]any{
		"op":            "record",
		"manifest_path": "packet.demo.json",
	})
	if err != nil {
		t.Fatalf("DemoHandler(record): %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	if len(fake.calls) != 1 || fake.calls[0].args[0] != "packet.demo.json" {
		t.Fatalf("unexpected call: %+v", fake.calls)
	}
}

func TestDemoHandler_Doctor_OK(t *testing.T) {
	withFakeToolRoot(t, "demo-doctor.mjs")
	report := map[string]any{"ok": true, "checks": []any{
		map[string]any{"name": "states", "ok": true},
	}}
	stdout, _ := json.Marshal(report)
	fake := &fakeDemoRunner{stdout: stdout}
	defer WithDemoRunner(fake)()

	res, err := DemoHandler(context.Background(), map[string]any{
		"op":            "doctor",
		"manifest_path": "packet.demo.json",
	})
	if err != nil {
		t.Fatalf("DemoHandler(doctor): %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error for a passing report: %s", res.Error)
	}
	gotReport, ok := res.Data["report"].(map[string]any)
	if !ok {
		t.Fatalf("Data[report] missing or wrong type: %#v", res.Data)
	}
	if gotReport["ok"] != true {
		t.Fatalf("report.ok = %v, want true", gotReport["ok"])
	}
	if len(fake.calls) != 1 || fake.calls[0].args[1] != "--json" {
		t.Fatalf("expected --json flag, got args %v", fake.calls[0].args)
	}
}

func TestDemoHandler_Doctor_Fail(t *testing.T) {
	withFakeToolRoot(t, "demo-doctor.mjs")
	report := map[string]any{"ok": false, "checks": []any{
		map[string]any{"name": "freshness", "ok": false, "detail": "clip is stale"},
	}}
	stdout, _ := json.Marshal(report)
	// demo-doctor.mjs exits nonzero when any check fails; simulate that via
	// a non-nil runner error alongside the JSON report on stdout.
	fake := &fakeDemoRunner{stdout: stdout, err: errExitStatus1{}}
	defer WithDemoRunner(fake)()

	res, err := DemoHandler(context.Background(), map[string]any{
		"op":            "doctor",
		"manifest_path": "packet.demo.json",
	})
	if err != nil {
		t.Fatalf("DemoHandler(doctor) infra error: %v", err)
	}
	if res.Error == "" {
		t.Fatal("expected Result.Error to be set for a failing doctor report")
	}
}

func TestResolveDemoScript_MissingFallsBackWithClearError(t *testing.T) {
	t.Setenv(demoScriptsRootEnv, t.TempDir())
	_, err := resolveDemoScript(map[string]any{}, "create-mockup.mjs")
	if err == nil {
		t.Fatal("expected error when script is not present at the configured root")
	}
}

func TestResolveDemoScript_KitRelativeWins(t *testing.T) {
	kitDir := t.TempDir()
	scriptsDir := filepath.Join(kitDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "create-mockup.mjs"), []byte("// kit-relative"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A different (also valid) tool root, to prove kit-relative wins.
	withFakeToolRoot(t, "create-mockup.mjs")

	got, err := resolveDemoScript(map[string]any{"_kit_dir": kitDir}, "create-mockup.mjs")
	if err != nil {
		t.Fatalf("resolveDemoScript: %v", err)
	}
	want := filepath.Join(scriptsDir, "create-mockup.mjs")
	if got != want {
		t.Fatalf("resolveDemoScript = %q, want %q (kit-relative should win)", got, want)
	}
}

type errExitStatus1 struct{}

func (errExitStatus1) Error() string { return "exit status 1" }
