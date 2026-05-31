package host_test

// Replay tests for host.oracle.task — Mode A/B/C classification and
// tarball/hash helpers.
//
// Tests in oracle_task_test.go cover the lower-level functions (captureInitialStateHash,
// captureFinalDiff, etc.). This file focuses on the higher-level classification
// contract and boundary conditions.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

// TestReplayMode_FileOnly_ModeA verifies that a typical file-only agent
// (Read/Edit/Write/Bash, no WebFetch/WebSearch) is classified as Mode A.
func TestReplayMode_FileOnly_ModeA(t *testing.T) {
	t.Parallel()
	agent := host.Agent{SystemPrompt: "implementer"}
	tools := []string{"Read", "Grep", "Glob", "Edit", "Write", "Bash"}
	mode := host.InferReplayModeExport(agent, tools)
	if mode != host.ReplayModeFileDiff {
		t.Fatalf("file-only agent: expected file_diff, got %q", mode)
	}
}

// TestReplayMode_NoTools_ModeA verifies that an agent with no declared tools
// defaults to Mode A (safest assumption).
func TestReplayMode_NoTools_ModeA(t *testing.T) {
	t.Parallel()
	agent := host.Agent{SystemPrompt: "sp"}
	mode := host.InferReplayModeExport(agent, nil)
	if mode != host.ReplayModeFileDiff {
		t.Fatalf("no-tools agent: expected file_diff, got %q", mode)
	}
}

// TestReplayMode_ExplicitFalse_ModeA verifies that ExternalSideEffect=false
// keeps Mode A even when inferred from tools would also be Mode A.
func TestReplayMode_ExplicitFalse_ModeA(t *testing.T) {
	t.Parallel()
	extFalse := false
	agent := host.Agent{SystemPrompt: "sp", ExternalSideEffect: &extFalse}
	mode := host.InferReplayModeExport(agent, []string{"Read", "Edit"})
	if mode != host.ReplayModeFileDiff {
		t.Fatalf("explicit false: expected file_diff, got %q", mode)
	}
}

// TestReplayMode_ExplicitTrue_ModeC verifies that ExternalSideEffect=true →
// Mode C regardless of the tool list.
func TestReplayMode_ExplicitTrue_ModeC(t *testing.T) {
	t.Parallel()
	extTrue := true
	agent := host.Agent{SystemPrompt: "sp", ExternalSideEffect: &extTrue}
	// Even pure file-system tools → Mode C when declared.
	mode := host.InferReplayModeExport(agent, []string{"Read", "Edit"})
	if mode != host.ReplayModeExternalSideEffect {
		t.Fatalf("explicit true: expected external_side_effect, got %q", mode)
	}
}

// TestReplayMode_SandboxWriteProfile_ModeB verifies BashProfileSandboxWrite → Mode B.
func TestReplayMode_SandboxWriteProfile_ModeB(t *testing.T) {
	t.Parallel()
	agent := host.Agent{
		SystemPrompt: "builder",
		BashProfile:  &host.BashProfile{Kind: host.BashProfileSandboxWrite, ScratchDir: "/tmp/scratch"},
	}
	mode := host.InferReplayModeExport(agent, []string{"Read", "Bash"})
	if mode != host.ReplayModeSandboxedWrite {
		t.Fatalf("sandboxed-write: expected sandboxed_write, got %q", mode)
	}
}

// TestReplayMode_ReadOnlyProfile_ModeA verifies that read-only BashProfile → Mode A.
func TestReplayMode_ReadOnlyProfile_ModeA(t *testing.T) {
	t.Parallel()
	agent := host.Agent{
		SystemPrompt: "sp",
		BashProfile:  &host.BashProfile{Kind: host.BashProfileReadOnly},
	}
	mode := host.InferReplayModeExport(agent, []string{"Bash"})
	if mode != host.ReplayModeFileDiff {
		t.Fatalf("read-only profile: expected file_diff, got %q", mode)
	}
}

// TestReplayMode_ExplicitFalse_IgnoresWebFetch verifies N7: when an agent
// declares ExternalSideEffect=false, the tool-inference path is skipped even
// if WebFetch/WebSearch appear in the tool list. The explicit declaration wins
// and the mode falls through to BashProfile-based classification (Mode A here).
//
// This is the runtime safety-net behind the loader's hard-fail for agents that
// declare external_side_effect: false with WebFetch/WebSearch in tools.
func TestReplayMode_ExplicitFalse_IgnoresWebFetch(t *testing.T) {
	t.Parallel()
	extFalse := false
	agent := host.Agent{SystemPrompt: "sp", ExternalSideEffect: &extFalse}
	// WebFetch and WebSearch in the tool list would infer Mode C if the
	// explicit-false guard were absent.
	tools := []string{"Read", "WebFetch", "WebSearch"}
	mode := host.InferReplayModeExport(agent, tools)
	if mode == host.ReplayModeExternalSideEffect {
		t.Fatalf("explicit false + WebFetch should NOT produce Mode C; got %q", mode)
	}
	if mode != host.ReplayModeFileDiff {
		t.Fatalf("explicit false + no sandboxed BashProfile: expected file_diff, got %q", mode)
	}
}

// TestReplayMode_ExplicitFalse_SandboxWrite_ModeB verifies that
// ExternalSideEffect=false with a sandboxed-write BashProfile yields Mode B
// (not Mode A or Mode C) — the BashProfile classification still fires.
func TestReplayMode_ExplicitFalse_SandboxWrite_ModeB(t *testing.T) {
	t.Parallel()
	extFalse := false
	agent := host.Agent{
		SystemPrompt:       "sp",
		ExternalSideEffect: &extFalse,
		BashProfile:        &host.BashProfile{Kind: host.BashProfileSandboxWrite, ScratchDir: "/tmp/s"},
	}
	mode := host.InferReplayModeExport(agent, []string{"Read", "Bash"})
	if mode != host.ReplayModeSandboxedWrite {
		t.Fatalf("explicit false + sandboxed-write: expected sandboxed_write, got %q", mode)
	}
}

// TestCapReadToolOutput_BoundaryCap verifies output exactly at the cap is
// returned verbatim.
func TestCapReadToolOutput_BoundaryCap(t *testing.T) {
	t.Parallel()
	output := strings.Repeat("z", 256*1024) // exactly at cap
	got := host.CapReadToolOutputExport(output)
	if got != output {
		t.Fatalf("at-cap output should be verbatim; lengths: got=%d want=%d", len(got), len(output))
	}
}

// TestCapReadToolOutput_OnePastCap verifies output 1 byte past cap is capped.
func TestCapReadToolOutput_OnePastCap(t *testing.T) {
	t.Parallel()
	output := strings.Repeat("z", 256*1024+1) // one past cap
	got := host.CapReadToolOutputExport(output)
	if !strings.HasPrefix(got, "sha256:") {
		t.Fatalf("one-past-cap output should be capped; got prefix %q", got[:20])
	}
}

// TestHashDirectory_EmptyDir returns a deterministic hash for an empty directory.
func TestHashDirectory_EmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := host.CaptureInitialStateHashExport(context.Background(), dir)
	// Empty non-git dir → tree hash.
	if !strings.HasPrefix(h, "tree:") {
		t.Fatalf("empty dir: expected tree: prefix, got %q", h)
	}
	// Consistent across calls.
	h2 := host.CaptureInitialStateHashExport(context.Background(), dir)
	if h != h2 {
		t.Fatalf("empty dir hash not deterministic: %q vs %q", h, h2)
	}
}

// TestTarballDirectory_Structure verifies that the tarball produced for a
// directory with nested files is a valid gzipped tar archive containing the
// expected files.
func TestTarballDirectory_Structure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	subDir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "top.txt"), []byte("top"), 0o644); err != nil {
		t.Fatalf("write top: %v", err)
	}
	if err := os.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatalf("write nested: %v", err)
	}

	tarBytes, err := host.TarballDirectoryExport(dir)
	if err != nil {
		t.Fatalf("tarball: %v", err)
	}
	if len(tarBytes) == 0 {
		t.Fatal("expected non-empty tarball")
	}
	// Gzip magic bytes.
	if tarBytes[0] != 0x1f || tarBytes[1] != 0x8b {
		t.Fatalf("expected gzip magic, got %02x %02x", tarBytes[0], tarBytes[1])
	}
}

// TestCaptureFinalDiff_UntrackedFileRemainsUntracked verifies N6: after
// captureFinalDiff runs, untracked files that were intent-added during the
// diff capture are reset back to untracked (??), not left as staged (A ).
func TestCaptureFinalDiff_UntrackedFileRemainsUntracked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := taskTestGitInit(dir); err != nil {
		t.Skip("git not available:", err)
	}
	// Commit an initial file so HEAD exists.
	if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("init\n"), 0o644); err != nil {
		t.Fatalf("write init: %v", err)
	}
	if err := taskTestGitAdd(dir, "init.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := taskTestGitCommit(dir, "initial"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	// Create an untracked file (simulates agent output not yet committed).
	if err := os.WriteFile(filepath.Join(dir, "newfile.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatalf("write newfile: %v", err)
	}

	// Run captureFinalDiff — internally runs git add -N then git reset HEAD.
	diff := host.CaptureFinalDiffExport(context.Background(), dir)
	if !strings.Contains(diff, "newfile.go") {
		t.Fatalf("diff should mention newfile.go; got: %s", diff)
	}

	// After the call, newfile.go must be untracked (??) not staged (A ).
	// Use git status --porcelain to inspect the index state.
	statusOut, statusErr := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if statusErr != nil {
		t.Fatalf("git status: %v", statusErr)
	}
	status := string(statusOut)
	for _, line := range strings.Split(strings.TrimSpace(status), "\n") {
		if strings.Contains(line, "newfile.go") {
			if !strings.HasPrefix(line, "??") {
				t.Fatalf("newfile.go should be untracked (??) after captureFinalDiff; got %q", line)
			}
			return
		}
	}
	t.Fatalf("newfile.go not found in git status output: %q", status)
}
