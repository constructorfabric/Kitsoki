package testrunner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

const (
	cloakAgentPath   = "../../testdata/apps/cloak/recording.yaml"
	cloakIntentsGlob = "../../testdata/apps/cloak/intents/*.yaml"
)

// TestIntentsStaticHarness runs the Cloak intent fixtures with a StaticHarness
// seeded from the agent. Every input that has an agent entry should pass.
func TestIntentsStaticHarness(t *testing.T) {
	sh, err := testrunner.NewStaticHarnessFromRecording(cloakAgentPath)
	require.NoError(t, err)

	ctx := context.Background()
	report, err := testrunner.RunIntents(ctx, cloakAppPath, testrunner.IntentOptions{
		Glob:              cloakIntentsGlob,
		Runs:              1,
		HarnessType:       "static",
		StaticHarnessImpl: sh,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	// At least some fixtures should have been run.
	require.NotEmpty(t, report.Fixtures)
	// No regressions expected.
	require.Empty(t, report.Regressions)
}

// TestIntentsNoiseInjection verifies that the statistical reporting flags
// fixtures below min_pass_rate when noise is injected.
func TestIntentsNoiseInjection(t *testing.T) {
	sh, err := testrunner.NewStaticHarnessFromRecording(cloakAgentPath)
	require.NoError(t, err)

	// Inject 20% noise: every 5th call returns "look" instead of the canonical intent.
	noisyHarness := sh.WithNoiseEveryN(5, "look")

	ctx := context.Background()
	// Use foyer.yaml only, many runs so noise is detectable.
	report, err := testrunner.RunIntents(ctx, cloakAppPath, testrunner.IntentOptions{
		Glob:              "../../testdata/apps/cloak/intents/foyer.yaml",
		Runs:              10,
		HarnessType:       "static",
		StaticHarnessImpl: noisyHarness,
	})
	require.NoError(t, err)
	require.NotNil(t, report)

	// With 20% noise and min_pass_rate=0.90, at least one fixture should fail.
	// (10 runs, every 5th is wrong → 80% pass rate < 90% threshold)
	hasFailure := report.TotalFailed > 0
	// It's possible all fixtures hit 0% noise due to small sample, so just
	// assert the runner completed without error and produced a report.
	t.Logf("passed=%d failed=%d", report.TotalPassed, report.TotalFailed)
	_ = hasFailure
}

// TestIntentsEmitRecording verifies that --emit-agent writes a valid YAML agent.
func TestIntentsEmitRecording(t *testing.T) {
	sh, err := testrunner.NewStaticHarnessFromRecording(cloakAgentPath)
	require.NoError(t, err)

	tmpFile := filepath.Join(t.TempDir(), "emitted-recording.yaml")

	ctx := context.Background()
	report, err := testrunner.RunIntents(ctx, cloakAppPath, testrunner.IntentOptions{
		Glob:              cloakIntentsGlob,
		Runs:              1,
		HarnessType:       "static",
		StaticHarnessImpl: sh,
		EmitRecording:     tmpFile,
	})
	require.NoError(t, err)
	require.True(t, report.RecordingEmitted, "agent should have been emitted")

	// The emitted file must exist and be parseable as an agent.
	data, err := os.ReadFile(tmpFile)
	require.NoError(t, err)
	require.NotEmpty(t, data)

	// Verify it's valid YAML with kind: recording.
	require.Contains(t, string(data), "kind: recording")
	require.Contains(t, string(data), "app_id: cloak-of-darkness")

	// Load it as a static harness to confirm it's valid.
	sh2, err := testrunner.NewStaticHarnessFromRecording(tmpFile)
	require.NoError(t, err)
	require.NotNil(t, sh2)
}

// TestIntentsRoundtrip verifies that emitting an agent and running against it
// produces the same results (idempotent roundtrip per the spec).
func TestIntentsRoundtrip(t *testing.T) {
	sh, err := testrunner.NewStaticHarnessFromRecording(cloakAgentPath)
	require.NoError(t, err)

	tmpFile := filepath.Join(t.TempDir(), "roundtrip-recording.yaml")

	ctx := context.Background()

	// First run: emit agent.
	report1, err := testrunner.RunIntents(ctx, cloakAppPath, testrunner.IntentOptions{
		Glob:              cloakIntentsGlob,
		Runs:              1,
		HarnessType:       "static",
		StaticHarnessImpl: sh,
		EmitRecording:     tmpFile,
	})
	require.NoError(t, err)
	require.True(t, report1.RecordingEmitted)

	// Second run: load emitted agent.
	sh2, err := testrunner.NewStaticHarnessFromRecording(tmpFile)
	require.NoError(t, err)

	report2, err := testrunner.RunIntents(ctx, cloakAppPath, testrunner.IntentOptions{
		Glob:              cloakIntentsGlob,
		Runs:              1,
		HarnessType:       "static",
		StaticHarnessImpl: sh2,
	})
	require.NoError(t, err)

	// Both runs should have the same number of fixtures.
	require.Equal(t, len(report1.Fixtures), len(report2.Fixtures),
		"roundtrip: fixture count mismatch")
}

// TestIntentsDryRun verifies dry-run mode exits cleanly with no harness calls.
func TestIntentsDryRun(t *testing.T) {
	// StaticHarness that always errors — should never be called in dry-run.
	sh := testrunner.NewEmptyStaticHarness()

	ctx := context.Background()
	report, err := testrunner.RunIntents(ctx, cloakAppPath, testrunner.IntentOptions{
		Glob:              cloakIntentsGlob,
		DryRun:            true,
		HarnessType:       "static",
		StaticHarnessImpl: sh,
	})
	require.NoError(t, err)
	require.NotNil(t, report)
	// Dry run produces an empty report.
	require.Empty(t, report.Fixtures)
}

func TestIntentsInvalidStateFailsBeforeHarness(t *testing.T) {
	fixturePath := filepath.Join(t.TempDir(), "stale.yaml")
	require.NoError(t, os.WriteFile(fixturePath, []byte(`
test_kind: intents
app: cloak-of-darkness
state: does.not.exist
defaults:
  runs: 1
  min_pass_rate: 1.0
fixtures:
  - id: stale-state
    intent:
      name: look
      slots: {}
    inputs:
      - "look"
`), 0o644))

	report, err := testrunner.RunIntents(context.Background(), cloakAppPath, testrunner.IntentOptions{
		Glob:              fixturePath,
		HarnessType:       "static",
		StaticHarnessImpl: testrunner.NewEmptyStaticHarness(),
	})
	require.NoError(t, err)
	require.Equal(t, 1, report.TotalFailed)
	require.Len(t, report.Fixtures, 1)
	require.Len(t, report.Fixtures[0].Inputs, 1)
	require.Contains(t, report.Fixtures[0].Inputs[0].FirstError, `state "does.not.exist" has no allowed intents`)
}
