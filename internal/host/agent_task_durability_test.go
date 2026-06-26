package host

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v (%s)", args, err, out)
		}
	}
	return dir
}

func writeAgent(tools ...string) Agent { return Agent{Tools: tools} }

func TestAgentHasWriteTools(t *testing.T) {
	if !agentHasWriteTools(writeAgent("Read", "Edit")) {
		t.Fatal("Edit should count as a write tool")
	}
	if !agentHasWriteTools(writeAgent("Write")) {
		t.Fatal("Write should count as a write tool")
	}
	if agentHasWriteTools(writeAgent("Read", "Grep", "Glob", "Bash")) {
		t.Fatal("read-only tool set must NOT count as write-capable")
	}
}

func TestWorktreeHasNonOwnerChanges(t *testing.T) {
	dir := initGitRepo(t)
	ctx := context.Background()

	if worktreeHasNonOwnerChanges(ctx, dir) {
		t.Fatal("clean repo: expected no non-owner changes")
	}

	// The ownership sentinel alone is not an agent write.
	if err := os.WriteFile(filepath.Join(dir, ".kitsoki-owner"), []byte("sid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if worktreeHasNonOwnerChanges(ctx, dir) {
		t.Fatal(".kitsoki-owner alone must be ignored")
	}

	// A real file write IS a non-owner change.
	if err := os.WriteFile(filepath.Join(dir, "repro_test.go"), []byte("package x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !worktreeHasNonOwnerChanges(ctx, dir) {
		t.Fatal("expected the written test file to register as a change")
	}
}

func TestWaitForAgentWorktreeWrites(t *testing.T) {
	ctx := context.Background()
	dir := initGitRepo(t)

	// Read-only agent: returns immediately regardless of tree state.
	start := time.Now()
	waitForAgentWorktreeWrites(ctx, writeAgent("Read"), dir, "ro", "test")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("read-only agent should not wait, waited %v", d)
	}

	// Write-capable agent with changes already present: returns fast.
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	waitForAgentWorktreeWrites(ctx, writeAgent("Edit"), dir, "rw", "test")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("changes already present should return fast, waited %v", d)
	}

	// Deadline of 0 disables the wait even with no changes (no stall).
	t.Setenv("KITSOKI_TASK_DURABILITY_WAIT_MS", "0")
	clean := initGitRepo(t)
	start = time.Now()
	waitForAgentWorktreeWrites(ctx, writeAgent("Write"), clean, "rw", "test")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("wait=0 must not stall, waited %v", d)
	}
}
