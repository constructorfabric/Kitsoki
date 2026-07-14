package ghagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/jobs"
)

// land_test.go — Gap 1 of docs/proposals/gh-agent-honest-issues.md (per
// .context/autonomous-product-journey-pipeline-howto.md): landFeatureBranch
// must produce a REAL git object on a REAL shared integration branch, not the
// old sha1("kitsoki-gh-agent-replay:"+slug) placeholder. These run real git
// against throwaway capsule checkouts — deterministic, local, free.

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func createJobWorkspaceForTest(t *testing.T, ctx context.Context, root string, job *jobs.GHJob) string {
	t.Helper()
	require.NoError(t, runDevWorkspace(ctx, root,
		"create",
		"--repo", root,
		"--id", jobWorkspaceID(job.JobID),
		"--branch", jobFeatureBranch(job.JobID),
		"--base", "main",
		"--no-bootstrap",
		"--json",
	))
	return filepath.Join(root, ".capsules", "workspaces", jobWorkspaceID(job.JobID))
}

func TestLandFeatureBranch_RealCommitOnSharedIntegrationBranch(t *testing.T) {
	ctx := context.Background()
	root := capsuletest.Open(t, "clean-repo")

	// Simulate what runRealDispatch's per-job managed workspace left behind: a
	// feature branch with a real commit beyond main.
	job1 := &jobs.GHJob{JobID: "job-one", OriginRef: "github:acme/widgets/issue/1"}
	job1Workspace := createJobWorkspaceForTest(t, ctx, root, job1)
	require.NoError(t, os.WriteFile(filepath.Join(job1Workspace, "fix-one.txt"), []byte("fix one\n"), 0o644))
	gitExec(t, job1Workspace, "add", "-A")
	gitExec(t, job1Workspace, "commit", "-q", "-m", "fix one")

	route := Route{World: map[string]any{}}
	commit1, err := landFeatureBranch(ctx, root, route, job1, "integration/qa-marathon1")
	require.NoError(t, err)
	assert.NotEmpty(t, commit1)
	assert.NotEqual(t, jobReplayCommitSHA(job1.JobID), commit1, "must be a real squash commit, not the synthetic placeholder")

	// A second job in the same marathon cycle lands onto the SAME shared
	// integration branch (the reused per-cycle workspace, not a per-job one).
	job2 := &jobs.GHJob{JobID: "job-two", OriginRef: "github:acme/widgets/issue/2"}
	job2Workspace := createJobWorkspaceForTest(t, ctx, root, job2)
	require.NoError(t, os.WriteFile(filepath.Join(job2Workspace, "fix-two.txt"), []byte("fix two\n"), 0o644))
	gitExec(t, job2Workspace, "add", "-A")
	gitExec(t, job2Workspace, "commit", "-q", "-m", "fix two")

	commit2, err := landFeatureBranch(ctx, root, route, job2, "integration/qa-marathon1")
	require.NoError(t, err)
	assert.NotEmpty(t, commit2)
	assert.NotEqual(t, commit1, commit2)

	// The shared integration branch carries BOTH fixes as real commits.
	log := gitExec(t, root, "log", "--format=%H", "integration/qa-marathon1")
	assert.Contains(t, log, commit1)
	assert.Contains(t, log, commit2)

	integrationWorkspace := filepath.Join(root, ".capsules", "workspaces", "integration-integration-qa-marathon1")
	assert.FileExists(t, filepath.Join(integrationWorkspace, "fix-one.txt"))
	assert.FileExists(t, filepath.Join(integrationWorkspace, "fix-two.txt"))
}

func TestLandFeatureBranch_FailsClosedWithNoRealCommits(t *testing.T) {
	ctx := context.Background()
	root := capsuletest.Open(t, "clean-repo")

	// A feature branch workspace that never diverged from main (no real fix
	// landed) — landFeatureBranch must refuse rather than fabricate a success.
	job := &jobs.GHJob{JobID: "job-empty", OriginRef: "github:acme/widgets/issue/3"}
	_ = createJobWorkspaceForTest(t, ctx, root, job)

	route := Route{World: map[string]any{}}
	_, err := landFeatureBranch(ctx, root, route, job, "integration/qa-marathon2")
	require.Error(t, err)
}
