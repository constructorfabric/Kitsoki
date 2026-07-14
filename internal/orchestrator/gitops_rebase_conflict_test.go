package orchestrator_test

// Real-conflict regression tests for the git-ops story's rebase/merge/pull
// exit-code handling.
//
// WHY A NEW HARNESS AND NOT A FLOW FIXTURE: every git-ops flow fixture stubs
// host.run by binding a canned `stdout_json` payload, so the embedded bash
// script never executes. That makes the entire class of "the shell script
// mis-handles a real exit code" invisible to the flow suite. The bug these
// tests lock in lived exactly there:
//
//	OUTPUT=$(git rebase "$INTEGRATION" --no-autostash 2>&1 || true)
//	EXIT=$?
//
// `$( … || true )` always exits 0, so `EXIT=$?` captured 0 unconditionally — a
// conflicting rebase was mis-detected as success and routed to `on_branch`
// (the branch_ops hub) instead of `rebase_conflict` (the conflict room). The
// fix moves the failure-tolerance OUTSIDE the substitution
// (`OUTPUT=$(git rebase …) || EXIT=$?`) so the real exit code survives while
// `set -e` is still not tripped.
//
// TestGitOps_RebaseConflict_RoutesToConflictRoom proves the behaviour against a
// real on-disk conflict; TestGitOps_NoSwallowedExitAfterTrueGuard is a cheap
// static guard so the anti-pattern can't silently return in any room script.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/capsuletest"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// setupConflictRepo opens the shared synthetic capsule whose `feature` branch
// is guaranteed to conflict with `main` on rebase. Returns the repo root,
// checked out on `feature` (so git-ops's idle router routes to branch_ops).
func setupConflictRepo(t *testing.T) string {
	t.Helper()
	return capsuletest.Open(t, "rebase-conflict-ready")
}

// setupRerereConflictRepo builds the same guaranteed-conflict repo as
// setupConflictRepo, but ALSO primes git's rerere cache with a recorded
// resolution for that exact conflict (on a throwaway copy of `feature`, so the
// real `feature` branch is left in its untouched pre-rebase state). When git-ops
// later rebases `feature` onto `main`, rerere replays the recorded resolution and
// the rebase completes with no conflict-room/LLM involvement. Returns the repo
// root, checked out on `feature`.
func setupRerereConflictRepo(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()

	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=Rerere Test", "GIT_AUTHOR_EMAIL=rerere@test.invalid",
		"GIT_COMMITTER_NAME=Rerere Test", "GIT_COMMITTER_EMAIL=rerere@test.invalid",
	)
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir, cmd.Env = repoRoot, env
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v failed: %s", args, string(out))
	}
	// gitTolerant runs a git command whose non-zero exit is expected (the priming
	// rebase intentionally conflicts; the priming --continue is the resolution).
	gitTolerant := func(extraEnv string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		cmd.Env = env
		if extraEnv != "" {
			cmd.Env = append(cmd.Env, extraEnv)
		}
		_ = cmd.Run()
	}
	writeFile := func(name, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, name), []byte(body), 0o644))
	}

	git("init", "--quiet", "--initial-branch=main")
	git("config", "user.email", "rerere@test.invalid")
	git("config", "user.name", "Rerere Test")
	// Enable rerere so the prime RECORDS the resolution and the git-ops rebase
	// REPLAYS it. autoupdate stages the replayed file automatically.
	git("config", "rerere.enabled", "true")
	git("config", "rerere.autoupdate", "true")

	writeFile("file.txt", "base line\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "base")

	git("checkout", "--quiet", "-b", "feature")
	writeFile("file.txt", "feature change\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "feature edit")

	git("checkout", "--quiet", "main")
	writeFile("file.txt", "main change\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "main edit")

	// PRIME the rerere cache on a throwaway copy of feature. The rebase conflicts
	// on file.txt (expected); resolve it, stage it, and let `rebase --continue`
	// commit — rerere auto-records the resolution keyed on the conflict pre-image.
	git("checkout", "--quiet", "-b", "feature_prime", "feature")
	gitTolerant("", "rebase", "main") // conflicts; rerere records the pre-image
	writeFile("file.txt", "resolved line\n")
	git("add", "file.txt")
	gitTolerant("GIT_EDITOR=true", "rebase", "--continue") // records the resolution

	// Back to the real feature branch, untouched & pre-rebase — exactly the state
	// a user is in when they ask git-ops to "rebase onto main". The conflict it
	// will hit is byte-identical to the primed one, so rerere replays it.
	git("checkout", "--quiet", "feature")
	return repoRoot
}

// escalatingConflictResolver is the host.agent.task stub for the conflict
// room. It returns resolved:false so the conflict room SETTLES at `conflict`
// (the escalation view) rather than emitting rebase_continue and driving the
// rebase forward against a tree the stub never actually edited. No LLM, free.
func escalatingConflictResolver(ctx context.Context, args map[string]any) (host.Result, error) {
	verdict := map[string]any{
		"resolved":           false,
		"unresolvable_files": "file.txt",
		"reason":             "stub resolver escalates — no edits performed",
	}
	stdoutJSON, _ := json.Marshal(verdict)
	return host.Result{Data: map[string]any{
		"submitted": verdict,
		"stdout":    string(stdoutJSON),
		"ok":        true,
	}}, nil
}

// TestGitOps_RebaseConflict_RoutesToConflictRoom drives the real git-ops story
// against a real rebase conflict and asserts it lands in the `conflict` room.
//
// Pre-fix (`EXIT=$?` after `$( … || true )`), the rebase room bound
// route="on_branch" even on a conflict and this test would land at
// `branch_ops` — with the working tree stranded mid-rebase. Post-fix the real
// non-zero rebase exit propagates, route="rebase_conflict", and the session
// routes to `conflict`.
func TestGitOps_RebaseConflict_RoutesToConflictRoom(t *testing.T) {
	// app.Load uses a path relative to the package dir; resolve it to an
	// absolute path BEFORE we t.Chdir into the temp repo below.
	cwd, err := os.Getwd()
	require.NoError(t, err)
	appPath := filepath.Join(cwd, "..", "..", "stories", "git-ops", "app.yaml")

	def, err := app.Load(appPath)
	require.NoError(t, err, "load git-ops/app.yaml")
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.run", host.RunHandler)
	reg.Register("host.agent.task", escalatingConflictResolver)
	// decide isn't on the rebase→conflict path, but git-ops declares it; stub
	// it defensively so an unexpected call surfaces as a clean verdict, not a
	// missing-handler error.
	reg.Register("host.agent.decide", escalatingConflictResolver)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	repoRoot := setupConflictRepo(t)
	// git-ops' scripts run with cwd="{{ world.working_dir }}" which defaults to
	// ".", so point the process cwd at the conflict repo. t.Chdir restores it
	// (and forbids t.Parallel, which is correct for a cwd-mutating test).
	t.Chdir(repoRoot)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	// Boot route: idle detects the feature branch and routes to branch_ops.
	{
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid), "RunInitialOnEnter must finish fast")
		cancel()
	}
	j0, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("branch_ops"), j0.State,
		"idle should route a feature branch to branch_ops; got %q", j0.State)

	// Drive the rebase. The conflict is real; the only question is where the
	// story routes.
	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := orch.SubmitDirect(c, sid, "rebase", nil)
	require.NoError(t, err, "rebase intent must complete")
	require.NotNil(t, out)
	require.Equal(t, app.StatePath("conflict"), out.NewState,
		"a conflicting rebase must route to the conflict room, not branch_ops; got %q (view: %q)",
		out.NewState, out.View)

	// World provenance: the rebase room recorded the conflict, not success.
	j1, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "rebase", j1.World.Vars["conflict_origin"],
		"conflict_origin must be 'rebase' after a conflicting rebase")
	require.Equal(t, false, j1.World.Vars["rebase_done"],
		"rebase_done must be false after a conflicting rebase")
}

// TestGitOps_RebaseConflict_RerereAutoResolves proves the no-LLM lever: when a
// rebase conflict has a resolution already recorded in the rerere cache, the
// rebase room replays it and drives the rebase to completion, landing at
// branch_ops WITHOUT ever entering the conflict room.
//
// The host.agent.task stub escalates (resolved:false) → conflict room rests at
// `conflict`. So if rerere did NOT auto-resolve and the story fell through to the
// agent, this test would land at `conflict` and fail. A green run proves the
// conflict was resolved deterministically, no agent/LLM touched.
func TestGitOps_RebaseConflict_RerereAutoResolves(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	appPath := filepath.Join(cwd, "..", "..", "stories", "git-ops", "app.yaml")

	def, err := app.Load(appPath)
	require.NoError(t, err, "load git-ops/app.yaml")
	m, err := machine.New(def)
	require.NoError(t, err)
	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	reg := host.NewRegistry()
	reg.Register("host.run", host.RunHandler)
	// If rerere fails to auto-resolve, the story reaches the agent — which here
	// escalates and rests at `conflict`, failing the branch_ops assertion below.
	reg.Register("host.agent.task", escalatingConflictResolver)
	reg.Register("host.agent.decide", escalatingConflictResolver)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	repoRoot := setupRerereConflictRepo(t)
	t.Chdir(repoRoot)

	ctx := context.Background()
	sid, err := orch.NewSession(ctx)
	require.NoError(t, err)

	{
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		require.NoError(t, orch.RunInitialOnEnter(c, sid))
		cancel()
	}
	j0, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, app.StatePath("branch_ops"), j0.State,
		"idle should route the feature branch to branch_ops; got %q", j0.State)

	c, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := orch.SubmitDirect(c, sid, "rebase", nil)
	require.NoError(t, err, "rebase intent must complete")
	require.NotNil(t, out)
	require.Equal(t, app.StatePath("branch_ops"), out.NewState,
		"a rebase whose conflict is in the rerere cache must auto-resolve to branch_ops, "+
			"never the conflict room; got %q (view: %q)", out.NewState, out.View)

	// World provenance: the rebase recorded success, not a conflict.
	j1, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, true, j1.World.Vars["rebase_done"],
		"rebase_done must be true after a rerere auto-resolved rebase")
	require.Equal(t, "", j1.World.Vars["conflict_origin"],
		"conflict_origin must be empty after a rerere auto-resolved rebase")

	// The working tree must hold the recorded resolution and be clean (rebase done).
	body, err := os.ReadFile(filepath.Join(repoRoot, "file.txt"))
	require.NoError(t, err)
	require.Equal(t, "resolved line\n", string(body),
		"file.txt must carry the rerere-recorded resolution")
}

// TestGitOps_NoSwallowedExitAfterTrueGuard is a static guard against the
// exit-code-swallowing anti-pattern returning to any git-ops room script:
// a command substitution that ends in `|| true)` whose exit status is then
// captured by a following `…EXIT=$?` line is always-zero and a routing bug
// waiting to happen. The fixed idiom (`VAR=$(cmd) || VAR_EXIT=$?`) does not
// match because the guard is no longer inside the `$()`.
//
// Legitimate `$( … || true )` uses that DON'T capture the exit afterward (e.g.
// `STASH_OUT=$(… || true)`, a `grep … || true` that tolerates "no match") are
// intentionally not flagged.
func TestGitOps_NoSwallowedExitAfterTrueGuard(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	roomsDir := filepath.Join(cwd, "..", "..", "stories", "git-ops", "rooms")

	files, err := filepath.Glob(filepath.Join(roomsDir, "*.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, files, "expected git-ops room YAMLs at %s", roomsDir)

	swallowsTrue := func(line string) bool {
		return strings.Contains(line, "$(") && strings.Contains(line, "|| true)")
	}
	capturesExit := regexp.MustCompile(`^\s*\w*EXIT=\$\?\s*$`)

	var offenders []string
	for _, f := range files {
		data, readErr := os.ReadFile(f)
		require.NoError(t, readErr, "read %s", f)
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if !swallowsTrue(line) {
				continue
			}
			// Look at the next non-blank line: if it captures $? the exit is
			// already destroyed by the inner `|| true`.
			for j := i + 1; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "" {
					continue
				}
				if capturesExit.MatchString(lines[j]) {
					offenders = append(offenders,
						filepath.Base(f)+":"+itoa(i+1)+" → "+strings.TrimSpace(line)+
							" / "+strings.TrimSpace(lines[j]))
				}
				break
			}
		}
	}
	require.Empty(t, offenders,
		"exit-code-swallowing anti-pattern found ($( … || true ) followed by EXIT=$?):\n%s",
		strings.Join(offenders, "\n"))
}

// itoa avoids pulling strconv in for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
