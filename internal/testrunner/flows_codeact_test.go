// RED gate for goal docs/goals/codeact/GOAL.md G2 ("Verb wiring"): a story
// flow fixture exercising a codeact room with a stubbed agent. See
// internal/testrunner/testdata/codeact_demo/{app.yaml,flows/happy_path.yaml}
// for the fixture itself and the assumptions baked into it (host.agent.codeact
// is not registered anywhere yet, so this fails at app-load, not at
// assertion time). Do not implement the codeact host handler / loader
// validation here — this file only pins the RED test.
package testrunner_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// TestFlowsCodeactDemo runs the codeact_demo fixture app end-to-end.
//
// MEASURED (not assumed): this test currently PASSES, and it is not a bug
// in the fixture. internal/app/loader.go's `hosts:` allow-list is
// self-declared (no global registry cross-check — see
// testdata/codeact_demo/flows/happy_path.yaml's header comment for the
// full trace), and this flow fixture stubs host.agent.codeact directly
// under host_handlers:, so RunFlows never needs a real
// internal/host-registered handler for it to succeed. That matches
// docs/goals/codeact/decomposition.yaml's s2 gate exactly: "a story flow
// fixture exercising a codeact room (stubbed agent) passes" is the target
// acceptance shape for G2, not a should-currently-fail RED probe. The
// actually-RED half of the s2 gate lives in
// internal/app/loader_codeact_test.go (TestLoad_CodeactUnknownCapability_Rejected
// and TestLoad_CodeactRejectsSandboxKnob), which fail today because no
// capability/sandbox cross-check exists yet for the codeact verb. Keep
// this flow test green as the regression fixture those two RED tests are
// steering toward; do not weaken its assertions to force a false failure.
func TestFlowsCodeactDemo(t *testing.T) {
	ctx := context.Background()
	report, err := testrunner.RunFlows(ctx,
		"testdata/codeact_demo/app.yaml",
		"testdata/codeact_demo/flows/*.yaml",
		testrunner.FlowOptions{})
	require.NoError(t, err, "codeact_demo app + flow should load and run cleanly once host.agent.codeact is wired")
	require.Equal(t, 1, report.Passed, "the happy_path fixture should pass once codeact verb wiring + a stubbed handler exist")
	require.Equal(t, 0, report.Failed)
}
