// Package testrunner — slice-1 IDE-integration verification (flow level).
//
// These tests prove host.ide.* works through the REAL orchestrator-backed flow
// runner with a STUBBED link — no editor, no socket:
//
//   - TestRunFlows_IDE_StubByInvokeID drives the ide_awareness demo app with
//     host.ide.get_diagnostics stubbed by per-invoke id (host_handlers: ...
//     by_call:, the feedback_agent_stub_by_id convention). Both the
//     editor-attached (connected:true → reviewed) and no-editor
//     (connected:false → offline) branches are asserted from one multi-doc
//     fixture.
//
//   - TestRunFlows_IDE_LegacyUnaffected re-runs the cloak-of-darkness fixtures
//     (a story with NO host.ide.* anywhere) to prove the host.ide.* additions
//     leave legacy stories byte-for-byte unchanged.
//
//   - TestRunFlows_IDE_CassetteReplay_NoSocket serves host.ide.get_diagnostics
//     from a recorded cassette while $HOME points at an EMPTY temp dir — so any
//     real lock-file discovery + ws dial would fail outright. The flow still
//     passes, proving replay never touches the socket path.
//
// All three run on virtual time with in-memory SQLite; none execs a real
// claude, opens a real socket, or sleeps.
package testrunner_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/testrunner"
)

// assertAllFlowsPassed fails the test if any non-skipped turn failed.
func assertAllFlowsPassed(t *testing.T, report *testrunner.FlowReport) {
	t.Helper()
	for _, r := range report.Results {
		if r.Skipped {
			t.Logf("SKIP %s", filepath.Base(r.File))
			continue
		}
		for _, tr := range r.Turns {
			if !tr.Passed {
				t.Errorf("flow %s turn %d failed: %v", filepath.Base(r.File), tr.TurnIndex+1, tr.Failures)
			}
		}
	}
	require.Equal(t, 0, report.Failed, "all flows should pass")
	require.Greater(t, report.Passed, 0, "at least one flow should pass")
}

// TestRunFlows_IDE_StubByInvokeID exercises both host.ide.* branches via a flow
// fixture that stubs the verb by per-invoke id. No live ide.Link is wired into
// the orchestrator (o.ideLink is nil in flow runs); the testrunner replaces the
// handler by name, so the verb resolves to the canned Result.Data shape — the
// same way every other host.* verb is stubbed.
func TestRunFlows_IDE_StubByInvokeID(t *testing.T) {
	const appPath = "../../testdata/apps/ide_awareness/app.yaml"
	const glob = "../../testdata/apps/ide_awareness/flows/diagnose_stub.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "RunFlows should not return a fatal error")
	require.NotEmpty(t, report.Results, "should have at least one result")
	// Multi-doc fixture: one connected-branch doc + one not-connected doc.
	require.GreaterOrEqual(t, len(report.Results), 2, "both branches should run")
	assertAllFlowsPassed(t, report)
}

// TestRunFlows_IDE_LegacyUnaffected proves a story with NO host.ide.* anywhere
// still passes unchanged after the host.ide.* additions. Cloak-of-darkness is
// the canonical legacy app (no host_handlers, no host.ide.*).
func TestRunFlows_IDE_LegacyUnaffected(t *testing.T) {
	const appPath = "../../testdata/apps/cloak/app.yaml"
	const glob = "../../testdata/apps/cloak/flows/*.yaml"

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "legacy-path RunFlows should not fail")
	require.NotEmpty(t, report.Results)
	assertAllFlowsPassed(t, report)
}

// TestRunFlows_IDE_CassetteReplay_NoSocket proves cassette replay of host.ide.*
// opens no socket. It points $HOME at an empty temp dir for the duration of the
// run, so the production discovery path (NewDiscoverer reads
// ~/.claude/ide/*.lock) would find no lock and any dial would fail. The flow
// still passes — the cassette dispatcher returns the recorded Result.Data
// directly, never constructing an ide.Link.
//
// The empty $HOME is the belt-and-suspenders: even if some future code path
// attempted a real discovery during a cassette replay, it would surface as a
// failure here rather than silently dialing a developer's live editor.
func TestRunFlows_IDE_CassetteReplay_NoSocket(t *testing.T) {
	const appPath = "../../testdata/apps/ide_awareness/app.yaml"
	const glob = "../../testdata/apps/ide_awareness/flows/diagnose_cassette.yaml"

	// Redirect HOME to an empty temp dir. t.Setenv restores the prior value at
	// test end. With no ~/.claude/ide/*.lock under this HOME, a real discovery
	// returns no candidates and a dial would error — but replay never tries.
	emptyHome := t.TempDir()
	t.Setenv("HOME", emptyHome)
	// Belt: also clear the integrated-terminal port seed so nothing can shortcut
	// discovery to a hard-coded port.
	t.Setenv("CLAUDE_CODE_SSE_PORT", "")

	// Sanity: the empty HOME truly has no IDE lock dir, so a real discovery is
	// guaranteed to find nothing (a real dial would therefore never succeed).
	_, statErr := os.Stat(filepath.Join(emptyHome, ".claude", "ide"))
	require.True(t, os.IsNotExist(statErr), "empty HOME must have no ~/.claude/ide so a real dial would fail")

	report, err := testrunner.RunFlows(t.Context(), appPath, glob, testrunner.FlowOptions{})
	require.NoError(t, err, "cassette replay should not return a fatal error")
	require.NotEmpty(t, report.Results)
	assertAllFlowsPassed(t, report)
}
