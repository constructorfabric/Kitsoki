package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
)

// gitops_batch_review_test.go — Gap 3 of .context/autonomous-product-journey-
// pipeline-howto.md: the batch human review surface. gitopsBatchReviewSummary
// must render ONE deterministic diff of the shared integration branch vs main,
// not require walking every fix job's diff individually.

func gitopsExecGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

// withGoMod stamps a go.mod marker into root and COMMITS it on main so it
// survives every later branch checkout — gitopsIntegrationRoot only accepts
// KITSOKI_REPO when go.mod is present (the same marker internal/ghagent's
// repoRoot() requires). An untracked go.mod would get swept into the next
// `git add -A` on a feature branch and then vanish on `git checkout main`
// (main's tree never had it), so it must be a real tracked file on main.
func withGoMod(t *testing.T, root string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module capsuletest\n\ngo 1.21\n"), 0o644))
	gitopsExecGit(t, root, "add", "go.mod")
	gitopsExecGit(t, root, "commit", "-q", "-m", "add go.mod marker")
	return root
}

func TestGitopsBatchReviewSummary_NotApplicableBeforeAnyLanding(t *testing.T) {
	root := withGoMod(t, capsuletest.Open(t, "clean-repo"))
	t.Setenv("KITSOKI_REPO", root)

	result := gitopsBatchReviewSummary(context.Background(), t.TempDir(), "integration/qa-unborn", "main")
	assert.Equal(t, "not_applicable", result["batch_review_status"])
	assert.NotContains(t, result, "batch_review_path")
}

func TestGitopsBatchReviewSummary_RendersRealDiffAgainstMain(t *testing.T) {
	root := withGoMod(t, capsuletest.Open(t, "clean-repo"))
	t.Setenv("KITSOKI_REPO", root)

	gitopsExecGit(t, root, "checkout", "-b", "integration/qa-cycle1")
	require.NoError(t, os.WriteFile(filepath.Join(root, "fix-one.txt"), []byte("landed fix\n"), 0o644))
	gitopsExecGit(t, root, "add", "fix-one.txt")
	gitopsExecGit(t, root, "commit", "-q", "-m", "land job-one's fix")
	gitopsExecGit(t, root, "checkout", "main")

	runDir := t.TempDir()
	result := gitopsBatchReviewSummary(context.Background(), runDir, "integration/qa-cycle1", "main")
	require.Equal(t, "ready", result["batch_review_status"])
	assert.Equal(t, "1", result["batch_review_commits"])

	path, _ := result["batch_review_path"].(string)
	require.NotEmpty(t, path)
	assert.Equal(t, filepath.Join(runDir, "batch-review.md"), path)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	body := string(data)
	assert.Contains(t, body, "integration/qa-cycle1")
	assert.Contains(t, body, "land job-one's fix")
	assert.Contains(t, body, "fix-one.txt")
}
