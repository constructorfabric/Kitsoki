package orchestrator_test

// Real-repo test for the git-ops checkpoint/restore arc. Like the rebase-conflict
// tests, the flow suite stubs host.run so the embedded bash never runs — this
// drives the story against a real on-disk git repo so the actual `git stash
// create` / `update-ref` / `reset --hard` / `stash apply` plumbing is exercised.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// TestGitOps_CheckpointRestore_Roundtrip proves the safe step-retry contract:
// checkpoint a dirty tracked tree, advance the branch (a "step"), then restore —
// landing back at the exact committed HEAD and dirty working content of the
// checkpoint, with the pre-restore state preserved under auto-pre-restore.
func TestGitOps_CheckpointRestore_Roundtrip(t *testing.T) {
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
	reg.Register("host.agent.decide", escalatingConflictResolver)
	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	// ── real repo on a feature branch ──────────────────────────────────────
	repoRoot := t.TempDir()
	env := append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME=CP Test", "GIT_AUTHOR_EMAIL=cp@test.invalid",
		"GIT_COMMITTER_NAME=CP Test", "GIT_COMMITTER_EMAIL=cp@test.invalid",
	)
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir, c.Env = repoRoot, env
		out, e := c.CombinedOutput()
		require.NoError(t, e, "git %v: %s", args, out)
		return strings.TrimSpace(string(out))
	}
	write := func(body string) {
		require.NoError(t, os.WriteFile(filepath.Join(repoRoot, "file.txt"), []byte(body), 0o644))
	}

	git("init", "--quiet", "--initial-branch=main")
	git("config", "user.email", "cp@test.invalid")
	git("config", "user.name", "CP Test")
	write("base\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "base")
	git("checkout", "--quiet", "-b", "feature")
	write("feature\n")
	git("add", "-A")
	git("commit", "--quiet", "-m", "feature commit")
	featureSHA := git("rev-parse", "HEAD")

	// Dirty tracked change present at checkpoint time.
	write("dirty-A\n")

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
	require.Equal(t, app.StatePath("branch_ops"), j0.State)

	// ── checkpoint ─────────────────────────────────────────────────────────
	c1, cancel1 := context.WithTimeout(ctx, 30*time.Second)
	out1, err := orch.SubmitDirect(c1, sid, "checkpoint", map[string]any{"label": "cp1"})
	cancel1()
	require.NoError(t, err)
	require.Equal(t, app.StatePath("checkpoint"), out1.NewState)
	j1, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "saved", j1.World.Vars["checkpoint_outcome"],
		"checkpoint must save; view: %q", out1.View)
	require.Equal(t, true, j1.World.Vars["checkpoint_dirty"],
		"checkpoint must capture the dirty tracked tree")
	// The refs exist and the base points at the feature commit.
	require.Equal(t, featureSHA, git("rev-parse", "refs/kitsoki/checkpoints/cp1.base"),
		"checkpoint base must be HEAD at checkpoint time")

	// ── advance the branch (a workflow "step" we'll want to abandon) ─────────
	git("add", "-A")
	git("commit", "--quiet", "-m", "step commit")           // HEAD moves past the checkpoint
	require.NotEqual(t, featureSHA, git("rev-parse", "HEAD")) // sanity: HEAD advanced
	write("dirty-B\n")                                        // and the tree is dirty again

	// ── restore ──────────────────────────────────────────────────────────────
	c2, cancel2 := context.WithTimeout(ctx, 30*time.Second)
	out2, err := orch.SubmitDirect(c2, sid, "restore", map[string]any{"label": "cp1"})
	cancel2()
	require.NoError(t, err)
	require.Equal(t, app.StatePath("restore"), out2.NewState)
	j2, err := orch.LoadJourney(sid)
	require.NoError(t, err)
	require.Equal(t, "restored", j2.World.Vars["restore_outcome"],
		"restore must succeed; view: %q", out2.View)

	// Committed history is back at the checkpoint base — the "step commit" is gone.
	require.Equal(t, featureSHA, git("rev-parse", "HEAD"),
		"restore must reset HEAD to the checkpoint base")
	// The dirty tracked content captured at checkpoint time is re-applied.
	body, err := os.ReadFile(filepath.Join(repoRoot, "file.txt"))
	require.NoError(t, err)
	require.Equal(t, "dirty-A\n", string(body),
		"restore must re-apply the checkpoint's dirty tracked tree")
	// Restore is reversible: the pre-restore state was auto-saved.
	require.NotEmpty(t, git("rev-parse", "refs/kitsoki/checkpoints/auto-pre-restore"),
		"restore must auto-save the pre-restore state")
}
