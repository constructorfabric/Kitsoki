package host_test

// Tests for host.agent.task handler.
//
// All tests use the ClaudeRunner stub seam (WithClaudeRunner) and FakeTask so
// no real subprocess is ever forked. Tests run in milliseconds.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/host"
)

// ── Missing agent ─────────────────────────────────────────────────────────

// TestAgentTask_MissingAgent verifies that the handler rejects calls without
// an agent: arg.
func TestAgentTask_MissingAgent(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(context.Background(), host.FakeTask(`{"ok":true}`))
	res, err := host.AgentTaskHandler(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "agent:") {
		t.Fatalf("expected agent: error, got: %q", res.Error)
	}
}

// TestAgentTask_UnknownAgent verifies that the handler rejects an unknown
// agent name.
func TestAgentTask_UnknownAgent(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"real-agent": {SystemPrompt: "sp"},
		}),
		host.FakeTask(`{"ok":true}`),
	)
	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"agent": "no-such-agent",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "no-such-agent") {
		t.Fatalf("expected unknown agent error, got: %q", res.Error)
	}
}

func TestAgentTask_InlineAgentContract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "ok.schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	var captured []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		captured = append([]string(nil), args...)
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`{"ok":true}`), 0o600)
		}
		return host.ClaudeRun{Stdout: `{"ok":true}`}, nil
	}
	ctx := host.WithClaudeRunner(context.Background(), runner)

	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"working_dir": dir,
		"context":     map[string]any{"prompt": "do it"},
		"acceptance":  map[string]any{"schema": schemaPath, "max_retries": 1},
		"agent_contract": map[string]any{
			"system_prompt": "inline maker",
			"model":         "claude-sonnet-4-6",
			"tools":         []any{"host.Read"},
			"permissions":   map[string]any{"mode": "ask"},
			"mcp": map[string]any{
				"tools": []any{"mcp__custom__lookup"},
				"servers": map[string]any{
					"custom": map[string]any{"command": "true"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}
	joined := strings.Join(captured, " ")
	if !strings.Contains(joined, "claude-sonnet-4-6") {
		t.Fatalf("inline model was not forwarded; args=%v", captured)
	}
	if !strings.Contains(joined, "--permission-mode default") {
		t.Fatalf("inline permissions.mode ask must map to CLI default; args=%v", captured)
	}
	if !strings.Contains(joined, "mcp__custom__lookup") {
		t.Fatalf("inline MCP tool was not forwarded; args=%v", captured)
	}
}

func TestAgentTask_SlideyMCPOnlyContract(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	schemaPath := filepath.Join(dir, "ok.schema.json")
	if err := os.WriteFile(schemaPath, []byte(`{"type":"object","required":["ok"],"properties":{"ok":{"type":"boolean"}}}`), 0o644); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	var captured []string
	var mcpConfigs []string
	runner := func(_ context.Context, args []string, _, _ string) (host.ClaudeRun, error) {
		captured = append([]string(nil), args...)
		for i, a := range args {
			if a == "--mcp-config" && i+1 < len(args) {
				if data, rerr := os.ReadFile(args[i+1]); rerr == nil {
					mcpConfigs = append(mcpConfigs, string(data))
				}
			}
		}
		if outputPath := host.ParseMCPConfigSubmitOutput(args); outputPath != "" {
			_ = os.WriteFile(outputPath, []byte(`{"ok":true}`), 0o600)
		}
		return host.ClaudeRun{Stdout: `{"ok":true}`}, nil
	}

	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"slidey_editor": {
				SystemPrompt: "edit slidey",
				Model:        "claude-sonnet-4-6",
				MCPTools: []string{
					"mcp__slidey__workspace_tree",
					"mcp__slidey__read_spec",
					"mcp__slidey__write_spec",
					"mcp__slidey__patch_spec",
					"mcp__slidey__validate",
				},
				MCPServers: map[string]any{
					"slidey": map[string]any{"command": "slidey-mcp", "args": []any{"--root", "."}},
				},
				Permissions: host.AgentPermissions{
					Mode: "ask",
					DisallowedTools: []string{
						"Read", "Grep", "Glob", "Write", "Edit", "Bash", "WebFetch", "WebSearch",
					},
				},
			},
		}),
		runner,
	)

	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"agent":       "slidey_editor",
		"working_dir": dir,
		"context":     map[string]any{"prompt": "write a deck"},
		"acceptance":  map[string]any{"schema": schemaPath, "max_retries": 1},
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected Result.Error: %s", res.Error)
	}

	if got, ok := hostTestFlagValue(captured, "--permission-mode"); !ok || got != "default" {
		t.Fatalf("slidey MCP-only contract must enforce default permission mode, got %q args=%v", got, captured)
	}
	allowed, ok := hostTestFlagValue(captured, "--allowedTools")
	if !ok {
		t.Fatalf("expected --allowedTools for slidey MCP contract; args=%v", captured)
	}
	for _, want := range []string{
		"mcp__slidey__workspace_tree",
		"mcp__slidey__read_spec",
		"mcp__slidey__write_spec",
		"mcp__slidey__patch_spec",
		"mcp__slidey__validate",
		"mcp__validator__submit",
	} {
		if !strings.Contains(allowed, want) {
			t.Fatalf("--allowedTools missing %q; got %q", want, allowed)
		}
	}
	for _, notWant := range []string{"host.Read", "host.Write", "host.Edit", "host.Bash", "Read", "Write", "Edit", "Bash"} {
		if strings.Contains(allowed, notWant) {
			t.Fatalf("generic workspace tool %q leaked into --allowedTools %q", notWant, allowed)
		}
	}
	denied, ok := hostTestFlagValue(captured, "--disallowedTools")
	if !ok {
		t.Fatalf("expected --disallowedTools for generic workspace denials; args=%v", captured)
	}
	for _, want := range []string{"Read", "Grep", "Glob", "Write", "Edit", "Bash", "WebFetch", "WebSearch", "AskUserQuestion"} {
		if !strings.Contains(denied, want) {
			t.Fatalf("--disallowedTools missing %q; got %q", want, denied)
		}
	}

	var slideyAttached, validatorAttached bool
	for _, cfg := range mcpConfigs {
		if strings.Contains(cfg, `"slidey"`) && strings.Contains(cfg, `"slidey-mcp"`) {
			slideyAttached = true
		}
		if strings.Contains(cfg, `"validator"`) {
			validatorAttached = true
		}
	}
	if !slideyAttached {
		t.Fatalf("slidey MCP server was not attached; args=%v configs=%v", captured, mcpConfigs)
	}
	if !validatorAttached {
		t.Fatalf("validator MCP server must remain attached; configs=%v", mcpConfigs)
	}
}

// ── Missing acceptance.schema ─────────────────────────────────────────────

// TestAgentTask_MissingSchema verifies that the handler rejects calls without
// acceptance.schema.
func TestAgentTask_MissingSchema(t *testing.T) {
	t.Parallel()
	ctx := host.WithClaudeRunner(
		host.WithAgents(context.Background(), map[string]host.Agent{
			"worker": {SystemPrompt: "do the work"},
		}),
		host.FakeTask(`{"ok":true}`),
	)
	res, err := host.AgentTaskHandler(ctx, map[string]any{
		"agent": "worker",
	})
	if err != nil {
		t.Fatalf("unexpected Go error: %v", err)
	}
	if !strings.Contains(res.Error, "acceptance.schema") {
		t.Fatalf("expected schema error, got: %q", res.Error)
	}
}

// ── Replay mode inference ─────────────────────────────────────────────────

// TestInferReplayMode_FileDiff verifies Mode A inference.
func TestInferReplayMode_FileDiff(t *testing.T) {
	t.Parallel()
	agent := host.Agent{SystemPrompt: "sp"}
	mode := host.InferReplayModeExport(agent, []string{"Read", "Edit", "Write", "Bash"})
	if mode != host.ReplayModeFileDiff {
		t.Fatalf("expected file_diff, got %q", mode)
	}
}

// TestInferReplayMode_ExternalFromTools verifies that WebFetch → Mode C.
func TestInferReplayMode_ExternalFromTools(t *testing.T) {
	t.Parallel()
	agent := host.Agent{SystemPrompt: "sp"}
	mode := host.InferReplayModeExport(agent, []string{"Read", "WebFetch"})
	if mode != host.ReplayModeExternalSideEffect {
		t.Fatalf("expected external_side_effect, got %q", mode)
	}
}

// TestInferReplayMode_ExternalFromDecl verifies that ExternalSideEffect:true → Mode C.
func TestInferReplayMode_ExternalFromDecl(t *testing.T) {
	t.Parallel()
	extTrue := true
	agent := host.Agent{SystemPrompt: "sp", ExternalSideEffect: &extTrue}
	mode := host.InferReplayModeExport(agent, []string{"Read", "Bash"})
	if mode != host.ReplayModeExternalSideEffect {
		t.Fatalf("expected external_side_effect, got %q", mode)
	}
}

// TestInferReplayMode_SandboxedWrite verifies Mode B inference.
func TestInferReplayMode_SandboxedWrite(t *testing.T) {
	t.Parallel()
	agent := host.Agent{
		SystemPrompt: "sp",
		BashProfile:  &host.BashProfile{Kind: host.BashProfileSandboxWrite},
	}
	mode := host.InferReplayModeExport(agent, []string{"Bash"})
	if mode != host.ReplayModeSandboxedWrite {
		t.Fatalf("expected sandboxed_write, got %q", mode)
	}
}

// TestInferReplayMode_WebSearch_ModeC verifies that WebSearch in tools → Mode C.
func TestInferReplayMode_WebSearch_ModeC(t *testing.T) {
	t.Parallel()
	agent := host.Agent{SystemPrompt: "sp"}
	mode := host.InferReplayModeExport(agent, []string{"Read", "WebSearch", "Bash"})
	if mode != host.ReplayModeExternalSideEffect {
		t.Fatalf("expected external_side_effect, got %q", mode)
	}
}

// ── Read-snapshot cap ────────────────────────────────────────────────────

// TestCapReadToolOutput_WithinCap verifies that short outputs are returned verbatim.
func TestCapReadToolOutput_WithinCap(t *testing.T) {
	t.Parallel()
	output := strings.Repeat("x", 100)
	got := host.CapReadToolOutputExport(output)
	if got != output {
		t.Fatalf("expected verbatim; got %q", got)
	}
}

// TestCapReadToolOutput_OverCap verifies that over-cap outputs are truncated
// with a sha256 prefix.
func TestCapReadToolOutput_OverCap(t *testing.T) {
	t.Parallel()
	output := strings.Repeat("y", 300*1024) // 300 KiB > 256 KiB cap
	got := host.CapReadToolOutputExport(output)
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("expected sha256 prefix; got %q...", got[:50])
	}
	if strings.Contains(got, strings.Repeat("y", 10000)) {
		t.Fatal("over-cap output should be truncated")
	}
}

// ── Initial state hash ────────────────────────────────────────────────────

// TestCaptureInitialStateHash_GitTree verifies that a git tree returns a
// "git:<sha>" prefix.
func TestCaptureInitialStateHash_GitTree(t *testing.T) {
	t.Parallel()
	dir := capsuletest.Open(t, "clean-repo")
	hash := host.CaptureInitialStateHashExport(context.Background(), dir)
	if !strings.HasPrefix(hash, "git:") {
		t.Fatalf("expected git: prefix, got %q", hash)
	}
}

// TestCaptureInitialStateHash_NonGitTree verifies that a non-git directory
// returns a "tree:<sha>" prefix.
func TestCaptureInitialStateHash_NonGitTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	hash := host.CaptureInitialStateHashExport(context.Background(), dir)
	if !strings.HasPrefix(hash, "tree:") {
		t.Fatalf("expected tree: prefix, got %q", hash)
	}
}

// TestHashDirectory_Deterministic verifies that hashDirectory produces the
// same result on repeated calls.
func TestHashDirectory_Deterministic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h1 := host.CaptureInitialStateHashExport(context.Background(), dir)
	h2 := host.CaptureInitialStateHashExport(context.Background(), dir)
	if h1 != h2 {
		t.Fatalf("hash not deterministic: %q vs %q", h1, h2)
	}
}

// TestHashDirectory_ChangesWithContent verifies that the hash changes when a
// file is added.
func TestHashDirectory_ChangesWithContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h1 := host.CaptureInitialStateHashExport(context.Background(), dir)
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("beta"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h2 := host.CaptureInitialStateHashExport(context.Background(), dir)
	if h1 == h2 {
		t.Fatal("hash did not change after adding a file")
	}
}

// ── Mode A replay: git tree ───────────────────────────────────────────────

// TestAgentTask_ModeA_ReplayFromDiff verifies the Mode A replay contract:
// given the initial hash + diff, the final state can be reconstructed.
func TestAgentTask_ModeA_ReplayFromDiff(t *testing.T) {
	t.Parallel()
	dir := capsuletest.Open(t, "agent-task-replay-base")
	initialContent := "initial content\n"
	filePath := filepath.Join(dir, "work.txt")

	initialHash := host.CaptureInitialStateHashExport(context.Background(), dir)
	if !strings.HasPrefix(initialHash, "git:") {
		t.Fatalf("expected git: hash, got %q", initialHash)
	}

	// Simulate agent editing the file.
	modifiedContent := "modified content\n"
	if err := os.WriteFile(filePath, []byte(modifiedContent), 0o644); err != nil {
		t.Fatalf("write modified: %v", err)
	}

	diff := host.CaptureFinalDiffExport(context.Background(), dir)
	filesChanged := host.CaptureFilesChangedExport(context.Background(), dir)

	if diff == "" {
		t.Fatal("expected non-empty diff")
	}
	if len(filesChanged) == 0 {
		t.Fatal("expected non-empty files_changed")
	}
	if filesChanged[0] != "work.txt" {
		t.Fatalf("expected work.txt in files_changed, got %v", filesChanged)
	}

	// Replay: restore the initial commit's state and apply the diff.
	// git checkout --detach only moves HEAD; we must also restore tracked files.
	sha := strings.TrimPrefix(initialHash, "git:")
	if err := taskTestGitCheckout(dir, sha); err != nil {
		t.Fatalf("git checkout initial: %v", err)
	}
	// Discard working-tree changes to return to the initial committed state.
	if err := taskTestGitRestore(dir); err != nil {
		t.Fatalf("git restore: %v", err)
	}
	readBack, _ := os.ReadFile(filePath)
	if string(readBack) != initialContent {
		t.Fatalf("after checkout+restore, expected initial content, got %q", readBack)
	}

	if err := taskTestGitApplyDiff(dir, diff); err != nil {
		t.Fatalf("git apply diff: %v", err)
	}
	readFinal, _ := os.ReadFile(filePath)
	if string(readFinal) != modifiedContent {
		t.Fatalf("replay: expected %q, got %q", modifiedContent, readFinal)
	}
}

// ── task.tool events ──────────────────────────────────────────────────────

// TestObserveTaskToolCalls_ExtractsToolUse verifies that observeTaskToolCalls
// parses tool_use blocks from RawEvents and returns events.
func TestObserveTaskToolCalls_ExtractsToolUse(t *testing.T) {
	t.Parallel()
	streamLine := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"foo.go","new_string":"x","old_string":"y"}}]}}`
	cr := host.ClaudeRun{RawEvents: []json.RawMessage{json.RawMessage(streamLine)}}
	events := host.ObserveTaskToolCallsExport(context.Background(), cr, "parent-trace-id")
	if len(events) != 1 {
		t.Fatalf("expected 1 task.tool event, got %d", len(events))
	}
	if events[0].Tool != "Edit" {
		t.Fatalf("expected tool=Edit, got %q", events[0].Tool)
	}
	if events[0].ParentTraceID != "parent-trace-id" {
		t.Fatalf("expected parent_trace_id=parent-trace-id, got %q", events[0].ParentTraceID)
	}
}

// TestObserveTaskToolCalls_MultipleCalls verifies multiple tool calls in one
// invocation are all recorded.
func TestObserveTaskToolCalls_MultipleCalls(t *testing.T) {
	t.Parallel()
	line1 := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"a.go"}}]}}`
	line2 := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`
	cr := host.ClaudeRun{RawEvents: []json.RawMessage{
		json.RawMessage(line1),
		json.RawMessage(line2),
	}}
	events := host.ObserveTaskToolCallsExport(context.Background(), cr, "tid")
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

// ── KITSOKI_SESSION_ID propagation ────────────────────────────────────────

// TestTaskEnv_SessionIDPropagation verifies that extractSessionID reads from
// the environment.
func TestTaskEnv_SessionIDPropagation(t *testing.T) {
	// Cannot use t.Parallel() alongside t.Setenv.
	t.Setenv("KITSOKI_SESSION_ID", "test-session-abc")
	sid := host.ExtractSessionIDExport(context.Background())
	if sid != "test-session-abc" {
		t.Fatalf("expected test-session-abc, got %q", sid)
	}
}

// ── Tarball (Mode B) ──────────────────────────────────────────────────────

// TestTarballDirectory_NonExistent verifies that a missing directory returns nil.
func TestTarballDirectory_NonExistent(t *testing.T) {
	t.Parallel()
	tar, err := host.TarballDirectoryExport("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tar != nil {
		t.Fatalf("expected nil for nonexistent dir, got %d bytes", len(tar))
	}
}

// TestTarballDirectory_WithFiles verifies that a directory with files produces
// non-nil gzipped tar bytes.
func TestTarballDirectory_WithFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("output"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tarBytes, err := host.TarballDirectoryExport(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tarBytes) == 0 {
		t.Fatal("expected non-empty tarball")
	}
	// Verify gzip magic bytes (0x1f 0x8b).
	if tarBytes[0] != 0x1f || tarBytes[1] != 0x8b {
		t.Fatalf("expected gzip magic bytes, got %02x %02x", tarBytes[0], tarBytes[1])
	}
}

// ── git helper functions ──────────────────────────────────────────────────

func taskTestGitInit(dir string) error {
	c := exec.Command("git", "init")
	c.Dir = dir
	return c.Run()
}

func taskTestGitAdd(dir string, paths ...string) error {
	args := append([]string{"add"}, paths...)
	c := exec.Command("git", args...)
	c.Dir = dir
	return c.Run()
}

func taskTestGitCommit(dir, message string) error {
	c := exec.Command("git", "-c", "user.name=Kitsoki Test", "-c", "user.email=kitsoki-test@example.invalid", "commit", "-m", message)
	c.Dir = dir
	return c.Run()
}

func taskTestGitCheckout(dir, ref string) error {
	c := exec.Command("git", "checkout", "--detach", ref)
	c.Dir = dir
	return c.Run()
}

func taskTestGitRestore(dir string) error {
	c := exec.Command("git", "restore", ".")
	c.Dir = dir
	return c.Run()
}

// taskTestGitUnstage removes a file from the index (unstages it), reverting
// intent-to-add entries staged by git add -N. Equivalent to git reset HEAD -- <file>.
func taskTestGitUnstage(dir, file string) error {
	c := exec.Command("git", "reset", "HEAD", "--", file)
	c.Dir = dir
	return c.Run()
}

func taskTestGitApplyDiff(dir, diff string) error {
	f, err := os.CreateTemp("", "kitsoki-test-diff-*.patch")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(diff); err != nil {
		f.Close()
		return err
	}
	f.Close()
	c := exec.Command("git", "apply", f.Name())
	c.Dir = dir
	return c.Run()
}

// taskTestGitApplyDiffAllowNew is like taskTestGitApplyDiff but explicitly
// handles new-file hunks (intent-to-add diffs) by writing the file directly
// from the diff rather than relying on git apply's heuristics.
func taskTestGitApplyDiffAllowNew(dir, diff string) error {
	f, err := os.CreateTemp("", "kitsoki-test-diff-*.patch")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(diff); err != nil {
		f.Close()
		return err
	}
	f.Close()
	// Use --allow-empty to be lenient; also try plain git apply first.
	c := exec.Command("git", "apply", "--allow-empty", f.Name())
	c.Dir = dir
	return c.Run()
}

// ── H3: task.tool events emitted from RawEvents ────────────────────────────

// TestObserveTaskToolCalls_FromRawEvents verifies that observeTaskToolCalls
// reads from cr.RawEvents (not cr.Stdout) and emits task.tool events for
// tool_use blocks, plus calls emitTaskToolEnd for matching tool_result blocks.
func TestObserveTaskToolCalls_FromRawEvents(t *testing.T) {
	t.Parallel()
	toolUseJSON := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"call1","name":"Edit","input":{"file_path":"foo.go","new_string":"x","old_string":"y"}}]}}`
	toolResultJSON := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"call1","content":"ok"}]}}`
	cr := host.ClaudeRun{
		Stdout:    "plain text — should not be parsed",
		RawEvents: []json.RawMessage{json.RawMessage(toolUseJSON), json.RawMessage(toolResultJSON)},
	}
	events := host.ObserveTaskToolCallsExport(context.Background(), cr, "parent-trace-id")
	if len(events) != 1 {
		t.Fatalf("expected 1 task.tool event, got %d", len(events))
	}
	if events[0].Tool != "Edit" {
		t.Fatalf("expected tool=Edit, got %q", events[0].Tool)
	}
}

// TestObserveTaskToolCalls_BackfillsOutputPreview verifies that the
// taskToolEvent entry for a tool_use is updated with OutputPreview when the
// matching tool_result block is observed. Both directions of the call (input
// and output) end up on a single rolled-up journal record.
func TestObserveTaskToolCalls_BackfillsOutputPreview(t *testing.T) {
	t.Parallel()
	toolUseJSON := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"foo.go"}}]}}`
	toolResultJSON := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r1","content":"file contents go here"}]}}`
	cr := host.ClaudeRun{
		RawEvents: []json.RawMessage{json.RawMessage(toolUseJSON), json.RawMessage(toolResultJSON)},
	}
	events := host.ObserveTaskToolCallsExport(context.Background(), cr, "tid")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].OutputPreview == "" {
		t.Fatalf("expected OutputPreview to be backfilled from tool_result, got empty")
	}
	if !strings.Contains(events[0].OutputPreview, "file contents go here") {
		t.Fatalf("OutputPreview did not carry tool_result text; got %q", events[0].OutputPreview)
	}
}

// TestObserveTaskToolCalls_NilRawEventsNoOp verifies that nil RawEvents
// (buffered-text path) returns without error and produces zero events.
func TestObserveTaskToolCalls_NilRawEventsNoOp(t *testing.T) {
	t.Parallel()
	cr := host.ClaudeRun{Stdout: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}}]}}`}
	events := host.ObserveTaskToolCallsExport(context.Background(), cr, "tid")
	if len(events) != 0 {
		t.Fatalf("nil RawEvents should produce 0 events, got %d", len(events))
	}
}

// TestObserveTaskToolCalls_LargeReadResult_Capped verifies M4: when a
// tool_result block contains 300 KiB of content, the StreamSink preview
// emitted by emitTaskToolEnd contains the cap-summarised text
// (sha256: header) rather than the raw 300 KiB. This ensures the journal
// stays bounded for large Read/Grep/Glob outputs.
func TestObserveTaskToolCalls_LargeReadResult_Capped(t *testing.T) {
	t.Parallel()

	// Build a 300 KiB synthetic Read tool result.
	largeOutput := strings.Repeat("x", 300*1024)
	toolUseJSON := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"r1","name":"Read","input":{"file_path":"/big/file.go"}}]}}`
	// Embed the large output as the tool_result content string.
	toolResultJSON := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"r1","content":` +
		jsonStringForTest(largeOutput) + `}]}}`

	cr := host.ClaudeRun{
		RawEvents: []json.RawMessage{
			json.RawMessage(toolUseJSON),
			json.RawMessage(toolResultJSON),
		},
	}

	// Capture StreamSink events so we can inspect the capped preview.
	var captured []host.StreamEvent
	sink := &testStreamSink{onEvent: func(ev host.StreamEvent) { captured = append(captured, ev) }}
	ctx := host.WithStreamSink(context.Background(), sink)

	host.ObserveTaskToolCallsExport(ctx, cr, "parent-tid")

	// Find the task.tool.end event.
	var endEvent *host.StreamEvent
	for i := range captured {
		if captured[i].Type == "task.tool.end" {
			endEvent = &captured[i]
			break
		}
	}
	if endEvent == nil {
		t.Fatal("expected task.tool.end event from ObserveTaskToolCalls")
	}

	// The preview must start with the sha256 cap summary. capReadToolOutput
	// applies the cap when input > ReadSnapshotCap (256 KiB), replacing the
	// full content with "sha256:<hex> (first 4096 bytes follow)\n<prefix>".
	// The 200-char onelinePreview then truncates that summary for the stream
	// event. The critical check is that the raw 300 KiB is NOT present
	// verbatim — only the sha256 summary and the first ≤4096 bytes of prefix.
	if !strings.HasPrefix(endEvent.Preview, "sha256:") {
		t.Fatalf("task.tool.end Preview should start with sha256: cap summary; got %q",
			endEvent.Preview[:min(len(endEvent.Preview), 80)])
	}
	// The capped summary is further truncated to ≤200 runes + trailing ellipsis
	// by onelinePreview. Verify the rune count (not byte count) is bounded.
	if runeCount := len([]rune(endEvent.Preview)); runeCount > 204 {
		t.Fatalf("task.tool.end Preview exceeds onelinePreview limit; rune count=%d", runeCount)
	}
}

// jsonStringForTest encodes s as a JSON string literal.
func jsonStringForTest(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// testStreamSink captures StreamEvents for inspection in tests.
type testStreamSink struct {
	onEvent func(host.StreamEvent)
}

func (s *testStreamSink) OnStreamEvent(_ context.Context, ev host.StreamEvent) {
	if s.onEvent != nil {
		s.onEvent(ev)
	}
}

// ── H4: captureFinalDiff captures untracked files ─────────────────────────

// TestCaptureFinalDiff_UntrackedFile verifies that a brand-new (untracked)
// file appears in captureFinalDiff's output and in files_changed (H4 fix).
// The diff also applies cleanly via `git apply` in a fresh checkout.
func TestCaptureFinalDiff_UntrackedFile(t *testing.T) {
	t.Parallel()
	dir := capsuletest.Open(t, "agent-task-replay-base")

	// Create a brand-new untracked file (agent side-effect).
	if err := os.WriteFile(filepath.Join(dir, "newfile.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatalf("write newfile: %v", err)
	}

	diff := host.CaptureFinalDiffExport(context.Background(), dir)
	if diff == "" {
		t.Fatal("expected non-empty diff with untracked file after intent-to-add")
	}
	if !strings.Contains(diff, "newfile.go") {
		t.Fatalf("diff should contain newfile.go; got:\n%s", diff)
	}

	filesChanged := host.CaptureFilesChangedExport(context.Background(), dir)
	var found bool
	for _, f := range filesChanged {
		if f == "newfile.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("files_changed should contain newfile.go; got %v", filesChanged)
	}

	// Replay: reset the working tree, then apply the diff and verify the file
	// is recreated. intent-to-add stages newfile.go in the index but leaves it
	// in the working tree; we must unstage it and remove it manually.
	if err := taskTestGitUnstage(dir, "newfile.go"); err != nil {
		t.Fatalf("git reset (unstage): %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "newfile.go")); err != nil {
		t.Fatalf("remove newfile.go: %v", err)
	}
	// After unstage+remove, newfile.go should be gone from working tree.
	if _, err := os.Stat(filepath.Join(dir, "newfile.go")); err == nil {
		t.Fatal("newfile.go should not exist after git restore")
	}
	if err := taskTestGitApplyDiffAllowNew(dir, diff); err != nil {
		t.Fatalf("git apply: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "newfile.go"))
	if err != nil {
		t.Fatalf("new file not found after replay: %v", err)
	}
	if string(data) != "package p\n" {
		t.Fatalf("expected 'package p\\n', got %q", data)
	}
}

// ── H5: returned sessionID captured across iterations ─────────────────────

// TestAgentStreamer_ReturnedSessionID verifies that the sessionID returned by
// AgentStreamer.Run (from system.init) is captured by the task handler for
// use in --resume on subsequent iterations. A StreamSink must be installed to
// activate the stream-json path that parses system.init events.
func TestAgentStreamer_ReturnedSessionID(t *testing.T) {
	t.Parallel()
	initEvent := `{"type":"system","subtype":"init","session_id":"returned-from-claude"}`
	resultEvent := `{"type":"result","subtype":"success","result":"done","session_id":"returned-from-claude"}`
	stub := func(_ context.Context, _ []string, _, _ string) (host.ClaudeRun, error) {
		return host.ClaudeRun{Stdout: initEvent + "\n" + resultEvent}, nil
	}
	// Install a no-op StreamSink to activate the stream-json path.
	ctx := host.WithStreamSink(
		host.WithClaudeRunner(context.Background(), stub),
		host.NoopStreamSinkExport(),
	)
	cr, sid, err := host.AgentStreamerRunExport(ctx, "stub://claude", []string{"-p"}, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = cr
	if sid != "returned-from-claude" {
		t.Fatalf("expected returned session ID 'returned-from-claude', got %q", sid)
	}
}

// ── C3: per-subprocess sessionID (no global env mutation) ─────────────────

// TestEnvWithSessionID_SetsKey verifies that envWithSessionID adds the key
// when absent.
func TestEnvWithSessionID_SetsKey(t *testing.T) {
	t.Parallel()
	env := []string{"PATH=/usr/bin", "HOME=/home/x"}
	out := host.EnvWithSessionIDExport(env, "test-session-111")
	var found string
	for _, kv := range out {
		if strings.HasPrefix(kv, "KITSOKI_SESSION_ID=") {
			found = strings.TrimPrefix(kv, "KITSOKI_SESSION_ID=")
		}
	}
	if found != "test-session-111" {
		t.Fatalf("expected KITSOKI_SESSION_ID=test-session-111 in env; got %q", found)
	}
}

// TestEnvWithSessionID_OverwritesExisting verifies that an existing
// KITSOKI_SESSION_ID is replaced rather than duplicated.
func TestEnvWithSessionID_OverwritesExisting(t *testing.T) {
	t.Parallel()
	env := []string{"KITSOKI_SESSION_ID=old-session", "PATH=/usr/bin"}
	out := host.EnvWithSessionIDExport(env, "new-session-222")
	var count int
	var found string
	for _, kv := range out {
		if strings.HasPrefix(kv, "KITSOKI_SESSION_ID=") {
			count++
			found = strings.TrimPrefix(kv, "KITSOKI_SESSION_ID=")
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 KITSOKI_SESSION_ID entry, got %d", count)
	}
	if found != "new-session-222" {
		t.Fatalf("expected new-session-222, got %q", found)
	}
}

// TestEnvWithSessionID_EmptyIsNoop verifies that empty sessionID leaves env unchanged.
func TestEnvWithSessionID_EmptyIsNoop(t *testing.T) {
	t.Parallel()
	env := []string{"PATH=/usr/bin", "KITSOKI_SESSION_ID=existing"}
	out := host.EnvWithSessionIDExport(env, "")
	if len(out) != len(env) {
		t.Fatalf("empty sessionID should return env unchanged; got %v", out)
	}
}

// ── L3: hashDirectory skips .git/ etc. ───────────────────────────────────

// TestHashDirectory_SkipsGitDir verifies that a .git directory doesn't affect
// the hash so the tree hash is stable even when git updates the index.
func TestHashDirectory_SkipsGitDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "app.go"), []byte("package main"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	h1 := host.CaptureInitialStateHashExport(context.Background(), dir)

	// Create a .git directory with a fake index file.
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "index"), []byte("fake index"), 0o644); err != nil {
		t.Fatalf("write .git/index: %v", err)
	}

	h2 := host.CaptureInitialStateHashExport(context.Background(), dir)
	if h1 != h2 {
		t.Fatalf(".git dir should be excluded from hash; h1=%s h2=%s", h1, h2)
	}
}

// ── L4: tarball cap ────────────────────────────────────────────────────────

// TestTarballDirectory_CapReturnPlaceholder verifies that a scratch dir
// exceeding 32 MiB returns a placeholder string, not a binary tarball.
func TestTarballDirectory_CapReturnPlaceholder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Write a 33 MiB file to exceed the cap.
	bigFile := filepath.Join(dir, "big.bin")
	data := make([]byte, 33*1024*1024)
	if err := os.WriteFile(bigFile, data, 0o644); err != nil {
		t.Fatalf("write big file: %v", err)
	}
	result, err := host.TarballDirectoryExport(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Fatal("expected non-empty result for over-cap dir")
	}
	placeholder := string(result)
	if !strings.Contains(placeholder, "omitted") {
		t.Fatalf("expected placeholder text with 'omitted', got: %q", placeholder)
	}
	if result[0] == 0x1f {
		t.Fatal("over-cap result should be a text placeholder, not a gzip stream")
	}
}
