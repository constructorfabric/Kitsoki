package testrunner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
	"kitsoki/internal/testrunner"
)

func TestDevStoryImplementationVisualReplayUsesCapsule(t *testing.T) {
	appPath := repoStoriesDevStoryAppPath(t)
	flowPath, err := filepath.Abs("../../stories/dev-story/flows/design_to_implementation_visual_replay.yaml")
	require.NoError(t, err)

	workspace := capsuletest.Open(t, "dev-story-implementation-replay")
	t.Chdir(workspace)

	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	for _, r := range report.Results {
		if !r.Passed {
			for _, turn := range r.Turns {
				for _, failure := range turn.Failures {
					t.Logf("flow=%s turn=%d failure: %s", filepath.Base(r.File), turn.TurnIndex+1, failure)
				}
			}
		}
		require.True(t, r.Passed, "visual replay flow should pass")
	}
	require.Equal(t, 0, report.Failed)

	_, statErr := os.Stat(filepath.Join(workspace, ".artifacts"))
	require.True(t, os.IsNotExist(statErr), "flow stubs host.artifacts_dir; replay must not write real artifacts")
	capsuletest.Verify(t, workspace)
}
