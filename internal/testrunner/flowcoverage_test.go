package testrunner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

func TestRunFlowCoverage_BranchesRequireExpectedStateForAmbiguousIntent(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: coverage_branch_test
  version: 0.1.0
  title: "coverage branch test"
  author: a
  license: CC0
world:
  choice: { type: string, default: "" }
root: idle
intents:
  choose:
    slots:
      mode:
        type: enum
        required: true
        values: [fast, safe]
states:
  idle:
    on:
      choose:
        - when: "slots.mode == 'fast'"
          target: fast
        - when: "slots.mode == 'safe'"
          target: safe
  fast:
    terminal: true
  safe:
    terminal: true
`
	flowYAML := `
test_kind: flow
initial_state: idle
turns:
  - intent:
      name: choose
      slots: { mode: fast }
    expect_state: fast
---
test_kind: flow
initial_state: idle
turns:
  - intent:
      name: choose
      slots: { mode: safe }
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)

	report, err := testrunner.RunFlowCoverage(t.Context(), appPath, testrunner.FlowCoverageOptions{
		FlowsGlob:          flowPath,
		RequireAllBranches: true,
	})
	require.NoError(t, err)
	require.False(t, report.Passed)
	require.Equal(t, 1, report.BranchCoverage.Covered)
	require.Equal(t, 2, report.BranchCoverage.Total)

	var fast, safe testrunner.FlowBranchCoverage
	for _, branch := range report.Branches {
		switch branch.Target {
		case "fast":
			fast = branch
		case "safe":
			safe = branch
		}
	}
	require.True(t, fast.Covered)
	require.False(t, safe.Covered, "ambiguous guarded branch needs expect_state evidence")
}

func TestRunFlowCoverage_ReportsEnumParameterGaps(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: coverage_params_test
  version: 0.1.0
  title: "coverage params test"
  author: a
  license: CC0
root: idle
intents:
  deploy:
    slots:
      env:
        type: enum
        required: true
        values: [staging, prod]
      mode:
        type: enum
        required: true
        values: [auto, manual]
states:
  idle:
    on:
      deploy:
        - target: done
  done:
    terminal: true
`
	flowYAML := `
test_kind: flow
initial_state: idle
turns:
  - intent:
      name: deploy
      slots: { env: staging, mode: auto }
    expect_state: done
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)

	report, err := testrunner.RunFlowCoverage(t.Context(), appPath, testrunner.FlowCoverageOptions{
		FlowsGlob:       flowPath,
		MaxCombinations: 8,
	})
	require.NoError(t, err)
	require.True(t, report.Passed, "branch threshold is independent from parameter gaps for now")
	require.Len(t, report.ParameterChecks, 1)

	check := report.ParameterChecks[0]
	require.False(t, check.Passed)
	require.Equal(t, []string{"prod"}, check.MissingValues["env"])
	require.Equal(t, []string{"manual"}, check.MissingValues["mode"])
	require.Equal(t, 4, check.RequiredCombinations)
	require.Len(t, check.MissingCombinations, 3)
}

func TestRunFlowCoverage_ReportsHostRunEffectCoverageAndAssertions(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: coverage_effect_test
  version: 0.1.0
  title: "coverage effect test"
  author: a
  license: CC0
hosts:
  - host.run
world:
  boot: { type: string, default: "" }
  ran:  { type: string, default: "" }
root: idle
intents:
  start: {}
  stop: {}
states:
  idle:
    on_enter:
      - invoke: host.run
        with:
          cmd: echo
          args: ["boot"]
          cwd: "/tmp"
        bind:
          boot: stdout
    on:
      start:
        - target: done
          effects:
            - set: { ran: "yes" }
            - invoke: host.run
              with:
                cmd: echo
                args: ["start"]
              bind:
                ran: stdout
      stop:
        - target: done
          effects:
            - invoke: host.run
              with:
                cmd: echo
                args: ["stop"]
  done:
    terminal: true
`
	flowYAML := `
test_kind: flow
initial_state: idle
turns:
  - intent: { name: start }
    expect_state: done
    expect_host_calls:
      - handler: host.run
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)

	report, err := testrunner.RunFlowCoverage(t.Context(), appPath, testrunner.FlowCoverageOptions{
		FlowsGlob:             flowPath,
		RequireAllEffects:     true,
		RequireHostAssertions: true,
	})
	require.NoError(t, err)
	require.False(t, report.Passed, "uncovered stop effect and unasserted on_enter host.run should fail strict gates")
	require.Equal(t, 3, report.EffectCoverage.Covered)
	require.Equal(t, 4, report.EffectCoverage.Total)

	var onEnterRun, startRun, stopRun testrunner.FlowEffectCoverage
	for _, effect := range report.Effects {
		if effect.Invoke != "host.run" {
			continue
		}
		switch {
		case effect.Origin == "on_enter":
			onEnterRun = effect
		case effect.Intent == "start":
			startRun = effect
		case effect.Intent == "stop":
			stopRun = effect
		}
	}
	require.True(t, onEnterRun.Covered)
	require.False(t, onEnterRun.HostAsserted)
	require.NotNil(t, onEnterRun.HostRun)
	require.Equal(t, "echo", onEnterRun.HostRun.Cmd)
	require.True(t, startRun.Covered)
	require.True(t, startRun.HostAsserted)
	require.False(t, stopRun.Covered)
}

func TestRunFlowCoverage_CassetteBacksOnEnterHostAssertion(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: coverage_cassette_effect_test
  version: 0.1.0
  title: "coverage cassette effect test"
  author: a
  license: CC0
hosts:
  - host.run
root: idle
intents:
  start: {}
states:
  idle:
    on_enter:
      - invoke: host.run
        with: { cmd: echo, args: ["boot"] }
    on:
      start:
        - target: done
  done:
    terminal: true
`
	flowYAML := `
test_kind: flow
initial_state: idle
host_cassette: boot.cassette.yaml
turns:
  - intent: { name: start }
    expect_state: done
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	cassetteYAML := `
kind: host_cassette
app_id: coverage_cassette_effect_test
match_on: [handler]
episodes:
  - id: boot
    match: { handler: host.run }
    response:
      data: { ok: true }
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "boot.cassette.yaml"), []byte(cassetteYAML), 0o644))

	report, err := testrunner.RunFlowCoverage(t.Context(), appPath, testrunner.FlowCoverageOptions{
		FlowsGlob:             flowPath,
		RequireAllEffects:     true,
		RequireHostAssertions: true,
	})
	require.NoError(t, err)
	require.True(t, report.Passed)
	require.Len(t, report.Effects, 1)
	require.True(t, report.Effects[0].Covered)
	require.True(t, report.Effects[0].HostAsserted)
}

func TestRunFlowCoverage_ReportsStarlarkStatementAndBranchCoverage(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: coverage_starlark_test
  version: 0.1.0
  title: "coverage starlark test"
  author: a
  license: CC0
hosts:
  - host.starlark.run
world:
  name: { type: string, default: "" }
root: idle
intents:
  enrich: {}
states:
  idle:
    on:
      enrich:
        - target: done
          effects:
            - invoke: host.starlark.run
              with:
                script: scripts/enrich.star
                capabilities:
                  http:
                    methods: [GET]
              bind:
                name: name
  done:
    terminal: true
`
	flowYAML := `
test_kind: flow
initial_state: idle
starlark_http_cassette: ok.http.yaml
turns:
  - intent: { name: enrich }
    expect_state: done
---
test_kind: flow
initial_state: idle
starlark_http_cassette: missing.http.yaml
turns:
  - intent: { name: enrich }
    expect_state: done
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "scripts"), 0o755))
	script := `
def main(ctx):
    resp = ctx.http.get("https://api.example.test/user")
    if resp:
        body = resp.json()
        return {"name": body["name"]}
    return {"name": "missing"}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "enrich.star"), []byte(script), 0o644))
	sidecar := `
outputs:
  name: { type: string, required: true }
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "enrich.star.yaml"), []byte(sidecar), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok.http.yaml"), []byte(`
kind: http_cassette
exchanges:
  - match: { method: GET, url: "https://api.example.test/user" }
    response:
      status: 200
      headers: { Content-Type: application/json }
      body: '{"name":"Ada"}'
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "missing.http.yaml"), []byte(`
kind: http_cassette
exchanges:
  - match: { method: GET, url: "https://api.example.test/user" }
    response:
      status: 404
      body: '{}'
`), 0o644))

	report, err := testrunner.RunFlowCoverage(t.Context(), appPath, testrunner.FlowCoverageOptions{
		FlowsGlob:                    flowPath,
		StarlarkCoverage:             true,
		MinStarlarkStatementCoverage: 100,
		MinStarlarkBranchCoverage:    100,
		RequireAllStarlarkBranches:   true,
		RequireRealStarlark:          true,
	})
	require.NoError(t, err)
	require.True(t, report.Passed, "both fixtures should cover success and error branches")
	require.Equal(t, report.StarlarkStatementCoverage.Total, report.StarlarkStatementCoverage.Covered)
	require.Equal(t, 2, report.StarlarkBranchCoverage.Total)
	require.Equal(t, 2, report.StarlarkBranchCoverage.Covered)
	require.Len(t, report.StarlarkScripts, 1)
	require.Equal(t, []string{flowPath}, report.StarlarkScripts[0].FlowFiles)
}

func TestRunFlowCoverage_RequireRealStarlarkRejectsHostStub(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: coverage_starlark_stub_test
  version: 0.1.0
  title: "coverage starlark stub test"
  author: a
  license: CC0
hosts:
  - host.starlark.run
world:
  name: { type: string, default: "" }
root: idle
intents:
  enrich: {}
states:
  idle:
    on:
      enrich:
        - target: done
          effects:
            - invoke: host.starlark.run
              with:
                script: scripts/enrich.star
              bind:
                name: name
  done:
    terminal: true
`
	flowYAML := `
test_kind: flow
initial_state: idle
host_cassette: starlark.cassette.yaml
turns:
  - intent: { name: enrich }
    expect_state: done
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "enrich.star"), []byte("def main(ctx):\n    return {\"name\": \"real\"}\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "scripts", "enrich.star.yaml"), []byte("outputs:\n  name: { type: string }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "starlark.cassette.yaml"), []byte(`
kind: host_cassette
app_id: coverage_starlark_stub_test
match_on: [handler]
episodes:
  - id: starlark
    match: { handler: host.starlark.run }
    response:
      data: { name: stubbed }
`), 0o644))

	report, err := testrunner.RunFlowCoverage(t.Context(), appPath, testrunner.FlowCoverageOptions{
		FlowsGlob:           flowPath,
		StarlarkCoverage:    true,
		RequireRealStarlark: true,
	})
	require.NoError(t, err)
	require.False(t, report.Passed, "host_cassette stubs must not satisfy real Starlark coverage")
	require.Equal(t, 1, report.StarlarkStatementCoverage.Total)
	require.Equal(t, 0, report.StarlarkStatementCoverage.Covered)
}

func TestRunFlowCoverage_WritesJSONReport(t *testing.T) {
	dir := t.TempDir()
	appYAML := `
app:
  id: coverage_json_test
  version: 0.1.0
  title: "coverage json test"
  author: a
  license: CC0
root: idle
intents:
  finish: {}
states:
  idle:
    on:
      finish:
        - target: done
  done:
    terminal: true
`
	flowYAML := `
test_kind: flow
initial_state: idle
turns:
  - intent: { name: finish }
    expect_state: done
`
	appPath, flowPath := writeFixture(t, dir, appYAML, flowYAML)
	out := filepath.Join(dir, "coverage.json")

	report, err := testrunner.RunFlowCoverage(t.Context(), appPath, testrunner.FlowCoverageOptions{
		FlowsGlob:          flowPath,
		JSONOut:            out,
		RequireAllBranches: true,
	})
	require.NoError(t, err)
	require.True(t, report.Passed)
	require.FileExists(t, out)
}
