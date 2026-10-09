package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
)

func TestProjectToolsUpgrade_NotOnboardedInvitesOnboarding(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	rep, err := checkProjectUpgrade(context.Background(), projectUpgradeOptions{Target: root})
	require.NoError(t, err)
	require.False(t, rep.Onboarded)
	require.True(t, rep.NeedsUpgrade)
	require.Equal(t, "missing", projectUpgradeCheckStatus(rep, "config"))
	require.Contains(t, projectUpgradeNoticeForRoot(root), "not onboarded")
}

func TestProjectToolsUpgradeDetectsOldDevStoryCapsule(t *testing.T) {
	useCurrentKitsokiRepo(t)
	root := capsuletest.Open(t, "old-dev-story-project")

	rep, err := checkProjectUpgrade(context.Background(), projectUpgradeOptions{Target: root})
	require.NoError(t, err)
	require.True(t, rep.Onboarded)
	require.True(t, rep.NeedsUpgrade)
	require.Equal(t, "ok", projectUpgradeCheckStatus(rep, "implicit-root"))
	require.Equal(t, "error", projectUpgradeCheckStatus(rep, "project-instance"))
	require.Equal(t, "missing", projectUpgradeCheckStatus(rep, "mcp"))

	notice := projectUpgradeNoticeForRoot(root)
	require.Contains(t, notice, "project files may need refresh")
	require.Contains(t, notice, "project-instance")
}

func TestProjectOnboardedForRootRequiresConfigAndProfile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.False(t, projectOnboardedForRoot(root))

	require.NoError(t, os.WriteFile(filepath.Join(root, ".kitsoki.yaml"), []byte("project_profile: .kitsoki/project-profile.yaml\n"), 0o644))
	require.False(t, projectOnboardedForRoot(root))

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".kitsoki"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".kitsoki", "project-profile.yaml"), []byte("schema: project-profile/v1\n"), 0o644))
	require.True(t, projectOnboardedForRoot(root))
}

func projectUpgradeCheckStatus(rep projectUpgradeReport, id string) string {
	for _, check := range rep.Checks {
		if check.ID == id {
			return check.Status
		}
	}
	return ""
}
