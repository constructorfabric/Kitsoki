package host_test

// bash_mcp_test.go — N2 coverage for bash_mcp.go (agent code review N2).
//
// Tests:
//   1. BuildBashMCPEntry roundtrip — build entry, parse config file, assert fields match.
//   2. RunBashMCPServerFromConfig with malformed JSON → returns error.
//   3. RunBashMCPServerFromConfig with non-existent path → returns error mentioning path.
//   4. rewriteToolsForBashMCP — "Bash" → "mcp__kitsoki-bash__Bash"; no-Bash list unchanged.
//   5. execCommand happy path — grep on real file under read-only profile.
//   6. execCommand quoted-arg (N3 regression) — grep "TODO" file matches without literal quotes.
//   7. execCommand blocked by profile — rm under read-only.
//   8. execCommand with out-of-range ProfileKind — tool error returned.
//   9. execCommand scratch cleanup — sandboxed-write creates file in scratch; scratch removed on shutdown.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// ── 1. BuildBashMCPEntry roundtrip ───────────────────────────────────────────

func TestBuildBashMCPEntry_Roundtrip(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{
		Kind:     host.BashProfileCommands,
		Commands: []string{"git", "jq"},
	}
	workDir := t.TempDir()

	entry, cfgPath, err := host.BuildBashMCPEntry(profile, workDir)
	if err != nil {
		t.Fatalf("BuildBashMCPEntry: %v", err)
	}
	defer os.Remove(cfgPath)

	// Entry must have command and args keys.
	if _, ok := entry["command"]; !ok {
		t.Error("entry missing 'command' key")
	}
	if _, ok := entry["args"]; !ok {
		t.Error("entry missing 'args' key")
	}

	// Config file must exist and round-trip to the right fields.
	data, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatalf("read config file: %v", readErr)
	}
	var cfg host.BashMCPConfig
	if jsonErr := json.Unmarshal(data, &cfg); jsonErr != nil {
		t.Fatalf("unmarshal config: %v", jsonErr)
	}
	if cfg.ProfileKind != int(host.BashProfileCommands) {
		t.Errorf("ProfileKind: got %d, want %d", cfg.ProfileKind, int(host.BashProfileCommands))
	}
	if cfg.WorkingDir != workDir {
		t.Errorf("WorkingDir: got %q, want %q", cfg.WorkingDir, workDir)
	}
	if len(cfg.Commands) != 2 || cfg.Commands[0] != "git" || cfg.Commands[1] != "jq" {
		t.Errorf("Commands: got %v, want [git jq]", cfg.Commands)
	}
}

// ── 2. RunBashMCPServerFromConfig with malformed JSON ────────────────────────

func TestRunBashMCPServerFromConfig_MalformedJSON(t *testing.T) {
	t.Parallel()
	f, err := os.CreateTemp(t.TempDir(), "bad-*.json")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	_, _ = f.WriteString("not json {{{{")
	_ = f.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runErr := host.RunBashMCPServerFromConfig(ctx, f.Name(), bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if runErr == nil {
		t.Fatal("expected error for malformed JSON config; got nil")
	}
	if !strings.Contains(runErr.Error(), "parse profile config") {
		t.Errorf("expected 'parse profile config' in error; got %q", runErr.Error())
	}
}

// ── 3. RunBashMCPServerFromConfig with non-existent path ─────────────────────

func TestRunBashMCPServerFromConfig_NonExistentPath(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runErr := host.RunBashMCPServerFromConfig(ctx, missing, bytes.NewReader(nil), &bytes.Buffer{}, &bytes.Buffer{})
	if runErr == nil {
		t.Fatal("expected error for missing config path; got nil")
	}
	if !strings.Contains(runErr.Error(), missing) {
		t.Errorf("expected path %q in error; got %q", missing, runErr.Error())
	}
}

// ── 4. rewriteToolsForBashMCP ────────────────────────────────────────────────

func TestRewriteToolsForBashMCP_RewritesBash(t *testing.T) {
	t.Parallel()
	in := []string{"Bash", "Read"}
	out := host.RewriteToolsForBashMCPExport(in)
	if len(out) != 2 {
		t.Fatalf("expected 2 tools; got %v", out)
	}
	if out[0] != "mcp__kitsoki-bash__Bash" {
		t.Errorf("first tool: got %q, want mcp__kitsoki-bash__Bash", out[0])
	}
	if out[1] != "Read" {
		t.Errorf("second tool: got %q, want Read", out[1])
	}
}

func TestRewriteToolsForBashMCP_NoBash_Unchanged(t *testing.T) {
	t.Parallel()
	in := []string{"Read", "Edit"}
	out := host.RewriteToolsForBashMCPExport(in)
	if len(out) != len(in) || out[0] != "Read" || out[1] != "Edit" {
		t.Errorf("tools without Bash must be unchanged; got %v", out)
	}
}

// ── 5. execCommand happy path ─────────────────────────────────────────────────

func TestExecCommand_HappyPath_GrepFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "haystack.txt")
	if err := os.WriteFile(target, []byte("needle line\nother line\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	profile := &host.BashProfile{Kind: host.BashProfileReadOnly}
	srv := host.NewBashMCPServer(profile, dir)
	result := srv.InvokeForTest(t, `{"command":"grep needle haystack.txt"}`)

	if result.IsError {
		t.Fatalf("expected success; got error: %q", result.Text)
	}
	if !strings.Contains(result.Text, "needle line") {
		t.Errorf("output must contain 'needle line'; got %q", result.Text)
	}
}

// ── 6. execCommand quoted-arg (N3 regression) ─────────────────────────────────
//
// Before N3 fix: strings.Fields split grep "TODO" file.go into
// ["grep", "\"TODO\"", "file.go"] — grep searched for the literal string
// "TODO" (with double-quote characters) and found nothing.
//
// After N3 fix: shellwords.Parse produces ["grep", "TODO", "file.go"] and
// grep correctly finds the needle.

func TestExecCommand_QuotedArg_N3Regression(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "source.go")
	// File contains the bare string TODO without quotes.
	if err := os.WriteFile(target, []byte("// TODO: fix this\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	profile := &host.BashProfile{Kind: host.BashProfileReadOnly}
	srv := host.NewBashMCPServer(profile, dir)

	// Command as the LLM would emit it: grep "TODO" source.go
	result := srv.InvokeForTest(t, `{"command":"grep \"TODO\" source.go"}`)

	if result.IsError {
		t.Fatalf("grep with quoted arg must succeed; got error: %q", result.Text)
	}
	if !strings.Contains(result.Text, "TODO") {
		t.Errorf("output must contain TODO; got %q — quoted-arg tokenisation may be broken (N3)", result.Text)
	}
}

// Also verify the find case: find . -name '*.go'
func TestExecCommand_SingleQuotedGlob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}

	profile := &host.BashProfile{Kind: host.BashProfileReadOnly}
	srv := host.NewBashMCPServer(profile, dir)

	// find . -name '*.go' — single-quoted glob must tokenise to ["find",".","-name","*.go"].
	result := srv.InvokeForTest(t, `{"command":"find . -name '*.go'"}`)

	if result.IsError {
		t.Fatalf("find with single-quoted glob must succeed; got error: %q", result.Text)
	}
	if !strings.Contains(result.Text, "main.go") {
		t.Errorf("output must contain main.go; got %q", result.Text)
	}
}

// ── 7. execCommand blocked by profile ────────────────────────────────────────

func TestExecCommand_BlockedByProfile_Rm(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{Kind: host.BashProfileReadOnly}
	srv := host.NewBashMCPServer(profile, t.TempDir())

	result := srv.InvokeForTest(t, `{"command":"rm -rf /tmp/foo"}`)
	if !result.IsError {
		t.Fatal("read-only profile must reject rm")
	}
	if !strings.Contains(result.Text, "rejected by profile") {
		t.Errorf("expected 'rejected by profile' in error; got %q", result.Text)
	}
}

// ── 8. execCommand with out-of-range ProfileKind ─────────────────────────────

func TestExecCommand_OutOfRangeProfileKind(t *testing.T) {
	t.Parallel()
	profile := &host.BashProfile{Kind: host.BashProfileKind(99)}
	srv := host.NewBashMCPServer(profile, t.TempDir())

	// Any command — the profile should reject it because kind 99 is unknown.
	result := srv.InvokeForTest(t, `{"command":"echo hello"}`)
	if !result.IsError {
		t.Fatal("unknown profile kind must produce a tool error")
	}
	if !strings.Contains(result.Text, "rejected by profile") {
		t.Errorf("expected 'rejected by profile' in error; got %q", result.Text)
	}
}

// ── 9. execCommand scratch cleanup ───────────────────────────────────────────
//
// Under sandboxed-write, each tool call creates a per-call scratch dir and
// removes it with defer os.RemoveAll after the command exits. The file
// written inside the scratch dir must NOT survive in the original workDir,
// and the scratch dir itself must be gone after the call returns.

func TestExecCommand_SandboxedWrite_ScratchCleanup(t *testing.T) {
	t.Parallel()
	scratchBase := t.TempDir()
	workDir := t.TempDir()

	profile := &host.BashProfile{
		Kind:       host.BashProfileSandboxWrite,
		ScratchDir: scratchBase,
	}
	srv := host.NewBashMCPServer(profile, workDir)

	result := srv.InvokeForTest(t, `{"command":"touch out.txt"}`)
	// The touch may fail on some systems but must not be rejected by profile.
	if result.IsError && strings.Contains(result.Text, "rejected by profile") {
		t.Fatalf("sandboxed-write must allow touch; got profile rejection: %q", result.Text)
	}

	// out.txt must NOT exist in the original workDir.
	if _, statErr := os.Stat(filepath.Join(workDir, "out.txt")); statErr == nil {
		t.Error("out.txt must NOT exist in workDir; scratch writes must be confined to scratch dir")
	}

	// The scratch subdirectory under scratchBase must have been cleaned up.
	entries, readErr := os.ReadDir(scratchBase)
	if readErr != nil {
		t.Fatalf("read scratchBase: %v", readErr)
	}
	if len(entries) > 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("scratch dir must be cleaned up after call; found leftover entries: %v", names)
	}
}
