package testrunner_test

import (
	"context"
	"os"
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

// TestStarlarkFlow_HostStubCannotHideMissingCapabilities proves flow mocks do
// not bypass the opt-in capability contract. Every subtest stubs the whole
// host.starlark.run handler, so without load-time validation the mock response
// would satisfy the flow and the script would never touch the real sandbox
// surface. The correct behavior is an early RunFlows load error.
func TestStarlarkFlow_HostStubCannotHideMissingCapabilities(t *testing.T) {
	tests := []struct {
		name         string
		script       string
		capabilities string
		want         string
	}{
		{
			name: "http",
			script: `def main(ctx):
    resp = ctx.http.get("https://example.test/widget")
    return {"body": str(resp.status)}
`,
			want: "with.capabilities does not grant http",
		},
		{
			name: "fs_read",
			script: `def main(ctx):
    return {"body": ctx.fs.read("README.md")}
`,
			want: "with.capabilities.fs.read is empty",
		},
		{
			name: "fs_exists",
			script: `def main(ctx):
    return {"body": str(ctx.fs.exists("README.md"))}
`,
			want: "with.capabilities.fs.read is empty",
		},
		{
			name: "fs_glob",
			script: `def main(ctx):
    return {"body": str(ctx.fs.glob("*.md"))}
`,
			want: "with.capabilities.fs.read is empty",
		},
		{
			name: "fs_write",
			script: `def main(ctx):
    return {"body": ctx.fs.write(".artifacts/out.txt", "x")}
`,
			want: "with.capabilities.fs.write is empty",
		},
		{
			name: "probe",
			script: `def main(ctx):
    return {"body": ctx.probe("git.status")["out"]}
`,
			want: "with.capabilities does not grant probe/vcs/github",
		},
		{
			name: "github_issues",
			script: `def main(ctx):
    return {"body": ctx.probe("gh.issue.list", ["owner/repo"])["out"]}
`,
			capabilities: `
                capabilities:
                  vcs: read
`,
			want: "with.capabilities.github.issues is not read",
		},
		{
			name: "host_call",
			script: `def main(ctx):
    return {"body": str(ctx.host.call("host.graph.load"))}
`,
			want: "with.capabilities.host.verbs is empty",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "scripts"), 0o755))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "capability.star"), []byte(tc.script), 0o644))
			require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "capability.star.yaml"), []byte(`outputs:
  body: { type: string }
`), 0o644))

			appYAML := `
app:
  id: starlark-mock-capability-` + strings.ReplaceAll(tc.name, "_", "-") + `
  version: 0.1.0
hosts:
  - host.starlark.run
world:
  body: { type: string, default: "" }
root: idle
intents:
  go: {}
states:
  idle:
    on:
      go:
        - target: done
          effects:
            - invoke: host.starlark.run
              with:
                script: scripts/capability.star` + tc.capabilities + `
              bind:
                body: body
  done:
    terminal: true
`
			flowYAML := `
test_kind: flow
initial_state: idle
host_handlers:
  host.starlark.run:
    data: { body: "stubbed without reading fs" }
turns:
  - intent: { name: go }
    expect_state: done
    expect_world:
      body: "stubbed without reading fs"
`
			appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
			_, err := testrunner.RunFlows(t.Context(), appPath, flowPath, testrunner.FlowOptions{})
			require.Error(t, err)
			require.Contains(t, err.Error(), "load app")
			require.Contains(t, err.Error(), tc.want)
		})
	}
}
