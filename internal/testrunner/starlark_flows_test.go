package testrunner_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/testrunner"
)

const (
	starlarkAppPath   = "../../testdata/apps/starlark_min/app.yaml"
	starlarkHappyFlow = "../../testdata/apps/starlark_min/flows/happy.yaml"
	starlarkErrorFlow = "../../testdata/apps/starlark_min/flows/http_error.yaml"
	starlarkNoSidecar = "../../testdata/apps/starlark_nosidecar/app.yaml"
)

// logFlowFailures dumps every turn failure for a flow result so a regression
// shows exactly which assertion broke.
func logFlowFailures(t *testing.T, r testrunner.FlowResult) {
	t.Helper()
	for _, turn := range r.Turns {
		for _, f := range turn.Failures {
			t.Logf("flow=%s turn=%d failure: %s", filepath.Base(r.File), turn.TurnIndex+1, f)
		}
	}
}

// TestStarlarkFlow_HappyPath runs the REAL host.starlark.run handler in a flow
// fixture with its HTTP replayed from a cassette: the script fetches a widget,
// binds widget_name into world, and the post-bind gate advances to reviewed.
// No socket is opened; no LLM is involved.
func TestStarlarkFlow_HappyPath(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, starlarkAppPath, starlarkHappyFlow, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	if !r.Passed {
		logFlowFailures(t, r)
	}
	require.True(t, r.Passed, "happy-path flow should pass")
	require.Equal(t, 1, report.Passed)
	require.Equal(t, 0, report.Failed)
}

// TestStarlarkFlow_HTTPError runs the same handler against a 404 cassette: the
// script fail()s, which surfaces as a domain error so the effect's on_error:
// arc fires and the session lands in `failed` with world.last_error set.
func TestStarlarkFlow_HTTPError(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, starlarkAppPath, starlarkErrorFlow, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.Len(t, report.Results, 1)
	r := report.Results[0]
	if !r.Passed {
		logFlowFailures(t, r)
	}
	require.True(t, r.Passed, "http-error flow should route to the on_error arc")
}

// TestStarlarkLoad_MissingSidecar asserts that an app whose host.starlark.run
// script has no sidecar fails at LOAD time (not at runtime) with an actionable
// message naming the expected sidecar path.
func TestStarlarkLoad_MissingSidecar(t *testing.T) {
	_, err := app.Load(starlarkNoSidecar)
	require.Error(t, err, "an app with a sidecar-less host.starlark.run script must fail to load")
	msg := err.Error()
	require.True(t, strings.Contains(msg, "no sidecar") || strings.Contains(msg, "sidecar"),
		"load error should mention the missing sidecar, got: %s", msg)
	require.Contains(t, msg, "orphan.star.yaml",
		"load error should name the expected sidecar path, got: %s", msg)
}
