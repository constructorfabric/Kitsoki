package orchestrator_test

// Reproduction test for bug9glm2 —
// "Concurrent dogfood sessions share one checkout — destructive git
// (WIP clobbered)".
// (issues/bugs/2026-06-03T121409Z-concurrent-dogfood-sessions-share-checkout-destructive-git.md)
//
// Root cause: the engine establishes per-session scratch state by STORY
// CONVENTION only. The bugfix story derives the working folder from the
// TICKET, never the SESSION:
//
//	stories/bugfix/rooms/idle.yaml (on_enter):
//	  workspace_id:   "bf-{{ world.ticket_id }}"
//	  feature_branch: "fix/{{ world.ticket_id }}"
//	  workdir:        ".worktrees/bf-{{ world.ticket_id }}"
//
// and the engine carries NO per-session workdir primitive: the `sessions`
// table (internal/store/schema.sql) has only (id, app_id, app_version,
// started_at, last_turn, status) — no workdir/branch/checkout column — and
// sessionRuntime has no checkout field. So nothing session-keys the
// checkout.
//
// Consequence: two dogfood sessions working the SAME ticket resolve to the
// SAME worktree path and run their agents in the SAME checkout. A
// destructive git op in one (reset --hard / clean / checkout --) clobbers
// the other's WIP — the P1 unrecoverable-data-loss incidents in the ticket.
//
// Correct behaviour (asserted here): each session is isolated to its OWN
// working folder, even on the same ticket. This test drives TWO sessions of
// the kitsoki-dev dogfood app against ONE shared git repo on the same ticket
// and asserts the agent-dispatch working_dir is DISTINCT per session. On the
// unfixed tree both resolve to ".worktrees/bf-<ticket>" (ticket-keyed), so
// the assertion FAILS — the sessions share one checkout — and a destructive
// git op run by session B clobbers session A's uncommitted WIP.
//
// The defect is timing-independent (the workdir is a pure function of the
// ticket), so it reproduces deterministically even when the sessions are
// driven sequentially; concurrency is what makes the shared checkout
// destructive, not what makes it collide.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// newCapturingSmokeOrchestrator is newSmokeOrchestrator whose agent stub
// records the working_dir each agent dispatch ran with, so a test can assert
// on which checkout a session's agents actually operated. Real (non-agent)
// builtins still run against the temp repo, exactly like the smoke tests.
func newCapturingSmokeOrchestrator(t *testing.T, repoRoot string) (*orchestrator.Orchestrator, app.SessionID, *[]string) {
	t.Helper()
	appPath := filepath.Join(repoRoot, ".kitsoki", "stories", "kitsoki-dev", "app.yaml")
	def, err := app.Load(appPath)
	require.NoError(t, err, "load kitsoki-dev/app.yaml from %s", appPath)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	var workdirs []string
	var mu sync.Mutex
	reg := host.NewRegistry()
	host.RegisterBuiltins(reg)
	captureStub := func(ctx context.Context, args map[string]any) (host.Result, error) {
		if wd, _ := args["working_dir"].(string); wd != "" {
			mu.Lock()
			workdirs = append(workdirs, wd)
			mu.Unlock()
		}
		stdoutJSON, _ := json.Marshal(dogfoodArtifact)
		return host.Result{Data: map[string]any{
			"submitted": dogfoodArtifact,
			"stdout":    string(stdoutJSON),
			"ok":        true,
		}}, nil
	}
	// Stub EVERY agent verb — automated tests must never invoke a real LLM
	// (CLAUDE.md). Replace overrides the real handler RegisterBuiltins set.
	for _, verb := range []string{
		"host.agent.ask", "host.agent.ask_with_mcp", "host.agent.task",
		"host.agent.decide", "host.agent.extract", "host.agent.converse",
		"host.agent.search",
	} {
		reg.Replace(verb, captureStub)
	}

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	return orch, sid, &workdirs
}

// resolveAbsWorkdir normalises a session's working_dir (which the bugfix
// story writes relative to the repo root, e.g. ".worktrees/bf-<ticket>") to
// an absolute path so two sessions' folders can be compared regardless of
// whether the orchestrator passed a relative or absolute value.
func resolveAbsWorkdir(t *testing.T, repoRoot, wd string) string {
	t.Helper()
	if wd == "" {
		return ""
	}
	if filepath.IsAbs(wd) {
		return filepath.Clean(wd)
	}
	return filepath.Clean(filepath.Join(repoRoot, wd))
}

// runDestructiveGit runs the kind of destructive working-tree reset a
// bugfix/git-ops room performs (reset tracked changes, remove untracked
// files). In a SHARED checkout this nukes the other session's WIP.
func runDestructiveGit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"reset", "--hard", "HEAD"},
		{"clean", "-fdx"},
	} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("git -C %s %v: %v: %s", dir, args, err, out)
		}
	}
}

// TestDogfoodConcurrent_SessionsMustNotShareCheckout reproduces bug9glm2.
func TestDogfoodConcurrent_SessionsMustNotShareCheckout(t *testing.T) {
	repoRoot, ticketID := setupDogfoodRepo(t)

	// Two independent sessions against the SAME on-disk repo (same cwd),
	// same ticket — the concurrent-dogfood scenario. Each gets its own
	// in-memory store/world (store.OpenMemory is per-call), so the only
	// thing they share is the on-disk git checkout — which is the bug.
	orchA, sidA, wdsA := newCapturingSmokeOrchestrator(t, repoRoot)
	orchB, sidB, wdsB := newCapturingSmokeOrchestrator(t, repoRoot)

	ctx := context.Background()

	// Boot + seed + auto-start each session through core__go_bugfix, which
	// fires bf.idle.on_enter (derives workspace_id/workdir from ticket_id,
	// creates the worktree, emits start → reproducing). The reproducing
	// agent is dispatched with working_dir = world.workdir.
	bootAndStart := func(orch *orchestrator.Orchestrator, sid app.SessionID, label string) {
		c, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		require.NoError(t, orch.RunInitialOnEnter(c, sid), "%s: RunInitialOnEnter must finish", label)
		_, err := orch.Teleport(c, sid, inbox.TeleportTarget{
			State: app.StatePath("core.landing"),
			Slots: seedDogfoodWorld(ticketID),
		})
		require.NoError(t, err, "%s: Teleport to core.landing with seeded ticket must succeed", label)
		out, err := orch.SubmitDirect(c, sid, "core__go_bugfix", nil)
		require.NoError(t, err, "%s: core__go_bugfix must complete (loop regression?)", label)
		require.NotNil(t, out)
		require.Equal(t, app.StatePath("core.bf.reproducing"), out.NewState,
			"%s: go_bugfix should auto-start through bf.idle and land at reproducing; got %q", label, out.NewState)
	}
	bootAndStart(orchA, sidA, "session A")
	bootAndStart(orchB, sidB, "session B")

	// Each session's reproducing agent was dispatched with a working_dir.
	require.NotEmpty(t, *wdsA, "session A's agents must have been dispatched with a working_dir")
	require.NotEmpty(t, *wdsB, "session B's agents must have been dispatched with a working_dir")

	wdA := resolveAbsWorkdir(t, repoRoot, (*wdsA)[0])
	wdB := resolveAbsWorkdir(t, repoRoot, (*wdsB)[0])
	t.Logf("session A agent working_dir=%q (raw %q)", wdA, (*wdsA)[0])
	t.Logf("session B agent working_dir=%q (raw %q)", wdB, (*wdsB)[0])

	// ── ROOT CAUSE (correct behaviour asserted, non-fatal so the
	// consequence check below also reports) ──
	// Two sessions on the same ticket must be isolated to DISTINCT working
	// folders. On the unfixed tree both resolve to the same
	// ".worktrees/bf-<ticket>" because the workdir is ticket-keyed, not
	// session-keyed.
	if wdA == wdB {
		t.Errorf("bug9glm2 root cause: concurrent dogfood sessions on the same "+
			"ticket resolved to the SAME working folder %q — the workdir is "+
			"derived from the ticket (stories/bugfix/rooms/idle.yaml: "+
			`workdir: ".worktrees/bf-{{ world.ticket_id }}")`+", not the session, so "+
			"the sessions share one checkout", wdA)
	}

	// ── CONSEQUENCE: destructive git clobbers WIP in the shared checkout ──
	// Session A writes an uncommitted WIP marker into ITS working folder.
	wipRel := "WIP_SESSION_A_MARKER.txt"
	wipPathA := filepath.Join(wdA, wipRel)
	require.NoError(t, os.WriteFile(wipPathA, []byte("session A work in progress\n"), 0o644),
		"write session A's WIP marker into %s", wdA)

	// Session B runs a destructive working-tree reset in ITS folder — the
	// kind of op the bugfix/git-ops cleanup/squash/restore rooms run. With
	// per-session isolation this must NOT touch session A's WIP.
	runDestructiveGit(t, wdB)

	if _, err := os.Stat(wipPathA); err != nil {
		t.Errorf("bug9glm2 consequence: session A's uncommitted WIP %q was "+
			"clobbered by session B's destructive git (reset --hard + clean -fdx) "+
			"run in %q — the sessions share one checkout, so B nuked A's work "+
			"(unrecoverable WIP loss). Per-session checkout isolation would have "+
			"left A's WIP untouched: %v", wipPathA, wdB, err)
	} else {
		t.Logf("session A's WIP survived session B's destructive git — checkouts isolated")
	}
}
