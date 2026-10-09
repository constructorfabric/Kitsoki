package testrunner_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestRunRoutingFixtures_DevStoryCore5 exercises the real WS-C C2 fixture
// (the five core dev-story workflows: onboard / PRD / decompose->implement /
// fix-a-bug, plus the file-a-bug boundary) purely through the no-LLM
// routing tiers — no harness, no recording.
func TestRunRoutingFixtures_DevStoryCore5(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunRoutingFixtures(ctx, "../../stories/dev-story/app.yaml", testrunner.RoutingOptions{
		Glob: "../../stories/dev-story/intents/workflow_core5_routing.yaml",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.Fixtures)
	require.Zero(t, report.TotalFailed, "dev-story core-5 routing fixtures must resolve deterministically/semantically with no LLM")
}

// TestRunRoutingFixtures_Bugfix exercises the bugfix idle-room entry
// nuances (full pipeline vs the interactive shortcuts vs ticket-picker
// handoff) for the "fix a bug" workflow.
func TestRunRoutingFixtures_Bugfix(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunRoutingFixtures(ctx, "../../stories/bugfix/app.yaml", testrunner.RoutingOptions{
		Glob: "../../stories/bugfix/intents/workflow_entry_routing.yaml",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.Fixtures)
	require.Zero(t, report.TotalFailed)
}

// TestRunRoutingFixtures_Prd exercises the prd idle-room discovery-entry
// nuances (free-form discuss vs ready-to-draft vs reference capture) for the
// "PRD / proposal" workflow.
func TestRunRoutingFixtures_Prd(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunRoutingFixtures(ctx, "../../stories/prd/app.yaml", testrunner.RoutingOptions{
		Glob: "../../stories/prd/intents/workflow_entry_routing.yaml",
	})
	require.NoError(t, err)
	require.NotEmpty(t, report.Fixtures)
	require.Zero(t, report.TotalFailed)
}

// synthAppYAML is a minimal two-intent app used to prove RunRoutingFixtures
// actually ASSERTS an outcome rather than silently treating an unresolved
// phrase as a pass (the gap this whole runner exists to close — see
// routing.go's package doc). "go north" is a declared example (resolves via
// the deterministic tier); "wander off" has no example/synonym anywhere
// (never resolves).
const synthAppYAML = `
app:
  id: routing-fixture-fidelity
  version: 0.1.0

world: {}

intents:
  go_north:
    title: "Go north"
    examples: ["go north"]

root: start

states:
  start:
    view: "compass rose"
    on:
      go_north:
        - target: ended

  ended:
    terminal: true
    view: "done"
`

func writeSynthApp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	require.NoError(t, os.WriteFile(appPath, []byte(synthAppYAML), 0o644))
	return appPath
}

func writeSynthFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	fixturePath := filepath.Join(dir, "fixture.yaml")
	require.NoError(t, os.WriteFile(fixturePath, []byte(body), 0o644))
	return fixturePath
}

// TestRunRoutingFixtures_ResolvedIntentPasses proves a correctly-declared
// example resolves and the runner reports it as a pass.
func TestRunRoutingFixtures_ResolvedIntentPasses(t *testing.T) {
	appPath := writeSynthApp(t)
	fixturePath := writeSynthFixture(t, `
test_kind: intents
app: app.yaml
state: start
fixtures:
  - id: north-resolves
    intent: { name: go_north }
    inputs: ["go north"]
`)

	ctx := context.Background()
	report, err := testrunner.RunRoutingFixtures(ctx, appPath, testrunner.RoutingOptions{Glob: fixturePath})
	require.NoError(t, err)
	require.Equal(t, 1, report.TotalPassed)
	require.Zero(t, report.TotalFailed)
}

// TestRunRoutingFixtures_UnresolvedIntentFails is the regression that
// motivates this whole file: `test intents` (Mode 1, harness/recording
// driven) treats a phrase with no recording entry as a SKIP — counted as a
// pass. RunRoutingFixtures must not reproduce that trap: an unresolved
// phrase asserted against an expected intent name is a FAIL, not a skip.
func TestRunRoutingFixtures_UnresolvedIntentFails(t *testing.T) {
	appPath := writeSynthApp(t)
	fixturePath := writeSynthFixture(t, `
test_kind: intents
app: app.yaml
state: start
fixtures:
  - id: never-resolves
    intent: { name: go_north }
    inputs: ["wander off into the woods"]
`)

	ctx := context.Background()
	report, err := testrunner.RunRoutingFixtures(ctx, appPath, testrunner.RoutingOptions{Glob: fixturePath})
	require.NoError(t, err)
	require.Zero(t, report.TotalPassed)
	require.Equal(t, 1, report.TotalFailed)
}

// TestRunRoutingFixtures_DefersToInterpreter proves the defers_to_interpreter
// assertion: a phrase with no no-LLM resolution passes when the fixture
// EXPECTS a defer, and fails when a phrase the fixture expected to defer
// actually resolved (the inverse mistake).
func TestRunRoutingFixtures_DefersToInterpreter(t *testing.T) {
	appPath := writeSynthApp(t)

	t.Run("expected defer, actually deferred: pass", func(t *testing.T) {
		fixturePath := writeSynthFixture(t, `
test_kind: intents
app: app.yaml
state: start
fixtures:
  - id: novel-phrase-defers
    defers_to_interpreter: true
    inputs: ["wander off into the woods"]
`)
		ctx := context.Background()
		report, err := testrunner.RunRoutingFixtures(ctx, appPath, testrunner.RoutingOptions{Glob: fixturePath})
		require.NoError(t, err)
		require.Equal(t, 1, report.TotalPassed)
		require.Zero(t, report.TotalFailed)
	})

	t.Run("expected defer, actually resolved: fail", func(t *testing.T) {
		fixturePath := writeSynthFixture(t, `
test_kind: intents
app: app.yaml
state: start
fixtures:
  - id: resolved-phrase-wrongly-expected-defer
    defers_to_interpreter: true
    inputs: ["go north"]
`)
		ctx := context.Background()
		report, err := testrunner.RunRoutingFixtures(ctx, appPath, testrunner.RoutingOptions{Glob: fixturePath})
		require.NoError(t, err)
		require.Zero(t, report.TotalPassed)
		require.Equal(t, 1, report.TotalFailed)
	})
}

// TestRunRoutingFixtures_OnlyState filters fixture docs by state.
func TestRunRoutingFixtures_OnlyState(t *testing.T) {
	appPath := writeSynthApp(t)
	fixturePath := writeSynthFixture(t, `
test_kind: intents
app: app.yaml
state: start
fixtures:
  - id: north-resolves
    intent: { name: go_north }
    inputs: ["go north"]
---
test_kind: intents
app: app.yaml
state: ended
fixtures:
  - id: irrelevant
    defers_to_interpreter: true
    inputs: ["anything"]
`)

	ctx := context.Background()
	report, err := testrunner.RunRoutingFixtures(ctx, appPath, testrunner.RoutingOptions{
		Glob:      fixturePath,
		OnlyState: "start",
	})
	require.NoError(t, err)
	require.Len(t, report.Fixtures, 1)
	require.Equal(t, "north-resolves", report.Fixtures[0].ID)
}
