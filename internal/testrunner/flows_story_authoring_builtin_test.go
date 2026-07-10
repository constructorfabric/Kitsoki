package testrunner_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

func TestRunFlows_StoryAuthoringBuiltin(t *testing.T) {
	const appPath = "../../testdata/apps/story_authoring_builtin/app.yaml"
	const flowPath = "../../testdata/apps/story_authoring_builtin/flows/happy_path.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.Equal(t, 0, report.Failed, "all story-authoring builtin flows should pass")
	require.Greater(t, report.Passed, 0, "at least one story-authoring builtin flow should pass")
}
