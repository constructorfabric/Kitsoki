package host_test

// Regression for bug
// 2026-06-03T121409Z-concurrent-dogfood-sessions-share-checkout-destructive-git
// ("Concurrent dogfood sessions share one checkout, causing destructive
// git churn and unrecoverable WIP loss").
//
// Root cause: the `workspace` host_interface (host.git_worktree) had NO
// per-session key. `workspace.create` derived the on-disk worktree path
// purely from the ticket-scoped `id` (`.worktrees/<id>`); the engine never
// threaded a session id into the provider. So two *distinct* concurrent
// sessions that happened to target the same ticket resolved to the SAME
// checkout — and the idempotency short-circuit handed the second session that
// same live tree as "ok". Once they shared a tree, a routine
// non-destructive-looking git op in one session (e.g. `git checkout -- <file>`
// to discard local edits before a restart) silently and unrecoverably
// destroyed the other session's uncommitted WIP.
//
// Fix — the host-side SAFETY NET (exercised here): the orchestrator projects
// its per-session SessionID into the story world as `world.session_id`, which
// stories/bugfix/rooms/idle.yaml threads into workspace.create. worktreeCreate
// writes a `.kitsoki-owner` sentinel on a successful `git worktree add`, and the
// idempotency short-circuit refuses to return an existing tree owned by a
// DIFFERENT session — so a second session racing the same on-disk worktree
// fails loudly instead of being silently handed the first session's live tree.
// (The worktree dir stays ticket-scoped `bf-<ticket>`; keying it per-session for
// same-ticket concurrency is a possible future enhancement, not needed to close
// the destructive bug — the sentinel already prevents the data loss.)
//
// This is a REAL on-disk git regression test (no fake runner): it exercises
// the actual `git worktree add` + the actual `cliExec` real-exec path.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kitsoki/internal/host"
)

func reproGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// TestRepro_ConcurrentSessionsShareCheckout_DestroysWIP proves the fix:
// a second session that races for the same on-disk worktree is refused by
// the `.kitsoki-owner` safety net (rather than silently handed the first
// session's live tree), so the first session's uncommitted WIP is never at
// risk — while legitimate same-session re-entry still short-circuits to ok.
func TestRepro_ConcurrentSessionsShareCheckout_DestroysWIP(t *testing.T) {
	repo := t.TempDir()

	// A real main checkout with one committed, tracked file.
	reproGit(t, repo, "init", "--quiet", "--initial-branch=main")
	reproGit(t, repo, "config", "user.email", "repro@test.invalid")
	reproGit(t, repo, "config", "user.name", "Repro")
	appYAML := filepath.Join(repo, "app.yaml")
	if err := os.WriteFile(appYAML, []byte("version: v1\n"), 0o644); err != nil {
		t.Fatalf("seed app.yaml: %v", err)
	}
	reproGit(t, repo, "add", "-A")
	reproGit(t, repo, "commit", "--quiet", "-m", "init")

	ctx := context.Background()
	ticket := "2026-06-03T121409Z-demo"

	// ── Session A starts work on the ticket ──────────────────────────
	// workspace_id / branch follow the bugfix story convention
	// (stories/bugfix/rooms/idle.yaml). Post-fix, the story keys the
	// on-disk dir on BOTH ticket and session (bf-<ticket>-<session>) and
	// threads `session_id` into workspace.create, which records a
	// `.kitsoki-owner` sentinel. Here we exercise the host-side SAFETY NET
	// directly: two sessions race for the SAME on-disk id (the worst case —
	// a caller that did NOT add the session dimension to the id), and the
	// sentinel must make the second session fail loudly instead of silently
	// sharing session A's live tree.
	resA, err := host.GitWorktreeHandler(ctx, map[string]any{
		"op":         "create",
		"repo":       repo,
		"id":         "bf-" + ticket,
		"name":       "fix/" + ticket,
		"base":       "main",
		"session_id": "session-A",
	})
	if err != nil {
		t.Fatalf("infra (session A create): %v", err)
	}
	if resA.Error != "" {
		t.Fatalf("session A create: %s", resA.Error)
	}
	pathA, _ := resA.Data["path"].(string)
	if pathA == "" {
		t.Fatalf("session A: empty path %#v", resA.Data)
	}

	// Session A makes uncommitted progress on a tracked file — its WIP.
	wipA := filepath.Join(pathA, "app.yaml")
	wipContent := "version: v1\nsession_A_wip: precious-unsaved-work\n"
	if err := os.WriteFile(wipA, []byte(wipContent), 0o644); err != nil {
		t.Fatalf("session A WIP write: %v", err)
	}

	// ── Session B (a *different*, concurrent session) takes the same
	//    ticket and (worst case) the identical workspace id/branch, but a
	//    DISTINCT session_id. The safety net must refuse to hand it session
	//    A's live tree. ─────────────────────────────────────────────────
	resB, err := host.GitWorktreeHandler(ctx, map[string]any{
		"op":         "create",
		"repo":       repo,
		"id":         "bf-" + ticket,
		"name":       "fix/" + ticket,
		"base":       "main",
		"session_id": "session-B",
	})
	if err != nil {
		t.Fatalf("infra (session B create): %v", err)
	}

	// FIX 1 — the safety net refuses to share: session B gets a loud error
	// naming the owning session, NOT a silent {ok:true} pointing at A's tree.
	if resB.Error == "" {
		t.Fatalf("expected session B create to be refused, but it succeeded: %#v", resB.Data)
	}
	if !strings.Contains(resB.Error, "already checked out by session") ||
		!strings.Contains(resB.Error, "session-A") {
		t.Fatalf("session B error did not name the owning session as expected: %q", resB.Error)
	}
	t.Logf("FIX 1: session B refused with: %s", resB.Error)

	// FIX 2 — A's WIP is never at risk: because B was refused, it never
	// obtained a handle to A's tree and so could not run a destructive
	// `git checkout --` against it. A's uncommitted work is intact.
	after, err := os.ReadFile(wipA)
	if err != nil {
		t.Fatalf("read session A WIP: %v", err)
	}
	if string(after) != wipContent {
		t.Fatalf("session A WIP was modified — isolation broke: %q", string(after))
	}
	t.Logf("FIX 2: session A's uncommitted WIP survives untouched")

	// Same-session re-entry (e.g. after a process restart) must still
	// short-circuit to success — the sentinel only blocks a DIFFERENT
	// session, never the legitimate owner.
	resReentry, err := host.GitWorktreeHandler(ctx, map[string]any{
		"op":         "create",
		"repo":       repo,
		"id":         "bf-" + ticket,
		"name":       "fix/" + ticket,
		"base":       "main",
		"session_id": "session-A",
	})
	if err != nil {
		t.Fatalf("infra (session A re-entry): %v", err)
	}
	if resReentry.Error != "" {
		t.Fatalf("session A re-entry should short-circuit to ok, got: %s", resReentry.Error)
	}
	if reentryPath, _ := resReentry.Data["path"].(string); reentryPath != pathA {
		t.Fatalf("session A re-entry path %q != original %q", reentryPath, pathA)
	}
}
