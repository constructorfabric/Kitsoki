package testrunner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestStarlarkEnrichExample runs the author-facing example story under
// stories/starlark-enrich/ end-to-end through the orchestrator. It exercises
// the REAL host.starlark.run handler — reading scripts/enrich_user.star and its
// sidecar from disk, validating the typed inputs/outputs — while replaying the
// script's single ctx.http GET from cassettes/enrich_user.http.yaml via the
// starlark_http_cassette: seam. No LLM is involved and no socket is opened, so
// the example is CI-verified and cost-free.
//
// This is the doubles as the README's "Test it" command:
//
//	go test ./internal/testrunner/ -run TestStarlarkEnrichExample
func TestStarlarkEnrichExample(t *testing.T) {
	const (
		appPath  = "../../stories/starlark-enrich/app.yaml"
		flowGlob = "../../stories/starlark-enrich/flows/*.yaml"
	)

	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx, appPath, flowGlob, testrunner.FlowOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, report.Results, "expected at least one flow result")

	for _, r := range report.Results {
		if !r.Passed {
			logFlowFailures(t, r)
		}
		require.True(t, r.Passed, "example flow %q should pass", r.File)
	}
	require.Equal(t, 0, report.Failed, "all starlark-enrich example flows must pass")
}
