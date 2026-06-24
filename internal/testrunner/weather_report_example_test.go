package testrunner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestWeatherReportExample runs the author-facing example story under
// stories/weather-report/ end-to-end through the orchestrator. It exercises the
// REAL host.starlark.run handler — reading scripts/weather_report.star and its
// sidecar from disk, validating the typed inputs/outputs — while replaying the
// script's ctx.http GETs (geocode + forecast/archive) from the cassettes under
// cassettes/ via the starlark_http_cassette: seam. No LLM is involved and no
// socket is opened, so the example is CI-verified and cost-free.
//
// This doubles as the README's "Test it" command:
//
//	go test ./internal/testrunner/ -run TestWeatherReportExample
func TestWeatherReportExample(t *testing.T) {
	const (
		appPath  = "../../stories/weather-report/app.yaml"
		flowGlob = "../../stories/weather-report/flows/*.yaml"
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
	require.Equal(t, 0, report.Failed, "all weather-report example flows must pass")
}
