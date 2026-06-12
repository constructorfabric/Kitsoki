package orchestrator_test

// Room-level config tests: connect the dev-story proposal room's YAML
// configuration to the AllowedIntents ordering and slot shapes that reach
// the browser's InputBar.
//
// Regression class guarded here:
//
//  1. Priority-ordering regression — capture_existing (priority 78) must
//     NEVER sort before discuss (priority 85). If someone bumps
//     capture_existing's priority above discuss, the dropdown in InputBar
//     defaults to the wrong intent and ideas silently land under
//     "Reference Docs" instead of "Discuss".
//
//  2. Slot-schema drift — discuss must expose exactly one string slot
//     ("message") and capture_existing exactly one string slot ("paths").
//     Any rename or type change here breaks the InputBar's text binding
//     without a compile error.
//
// No LLM calls are made: the oracle is stubbed; AllowedIntents + LookupIntent
// are pure machine queries against the loaded YAML.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/host"
	"kitsoki/internal/inbox"
	"kitsoki/internal/machine"
	"kitsoki/internal/orchestrator"
	"kitsoki/internal/store"
)

// setupDevStoryRepo copies the live stories/ tree into a temp dir (same
// approach as setupDogfoodRepo) and returns the absolute path to
// stories/dev-story/app.yaml inside that tree. Dev-story imports bugfix
// and pr-refinement via relative paths, so the whole stories/ sub-tree
// needs to be present.
//
// Unlike the dogfood smoke tests we do NOT need a real git repo here —
// no host.git_worktree calls happen in these query-only tests.
func setupDevStoryRepo(t *testing.T) string {
	t.Helper()

	repoRoot := t.TempDir()

	cwd, err := os.Getwd()
	require.NoError(t, err)
	kitsokiRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))

	src := filepath.Join(kitsokiRoot, "stories")
	dst := filepath.Join(repoRoot, "stories")
	require.NoError(t, copyTree(src, dst), "copy stories/ → %s", dst)

	return filepath.Join(repoRoot, "stories", "dev-story", "app.yaml")
}

// newDevStoryOrchestrator loads the dev-story app and returns a minimal
// orchestrator suitable for query-only tests (AllowedIntents, LookupIntent).
// The oracle is stubbed so no LLM costs are incurred.
func newDevStoryOrchestrator(t *testing.T, appPath string) (*orchestrator.Orchestrator, app.SessionID) {
	t.Helper()

	def, err := app.Load(appPath)
	require.NoError(t, err, "load dev-story app from %s", appPath)

	m, err := machine.New(def)
	require.NoError(t, err)

	s, err := store.OpenMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	// Stub every oracle verb — none are called in these tests, but the
	// registry panics on an unknown host if one is dispatched unexpectedly.
	reg := host.NewRegistry()
	oracleStub := func(_ context.Context, _ map[string]any) (host.Result, error) {
		return host.Result{Data: map[string]any{"ok": true}}, nil
	}
	reg.Register("host.oracle.ask_with_mcp", oracleStub)
	reg.Register("host.oracle.task", oracleStub)
	reg.Register("host.oracle.ask", oracleStub)
	reg.Register("host.oracle.decide", oracleStub)
	reg.Register("host.run", oracleStub)
	reg.Register("host.inbox.add", oracleStub)
	reg.Register("host.artifacts_dir", oracleStub)
	reg.Register("host.ide.open_file", oracleStub)

	orch := orchestrator.New(def, m, s, noopHarness{}, orchestrator.WithHostRegistry(reg))

	sid, err := orch.NewSession(context.Background())
	require.NoError(t, err)

	return orch, sid
}

// textSlotForIntent derives the single free-text slot name for a given
// intent definition, using the same logic as OrchestratorDriver.IntentInfo:
// exactly one string slot AND no required non-string slots ⇒ TextSlot is
// that string slot's name; otherwise empty.
func textSlotForIntent(def app.Intent) string {
	var stringSlots []string
	requiredNonString := false
	for name, slot := range def.Slots {
		if slot.Type == "string" {
			stringSlots = append(stringSlots, name)
		} else if slot.Required {
			requiredNonString = true
		}
	}
	if len(stringSlots) == 1 && !requiredNonString {
		return stringSlots[0]
	}
	return ""
}

// TestDesignRoom_AllowedIntents_DiscussBeforeCaptureExisting asserts that
// the design room's AllowedIntents list, when sorted by priority (as the
// machine and OrchestratorDriver.IntentInfo both do), places "discuss" before
// "capture_existing". This is the direct regression guard for the priority
// bug fixed by raising discuss.priority from 40 → 85.
//
// If someone changes priorities in app.yaml so that capture_existing ≥
// discuss, this test will fail BEFORE a user can encounter the silent
// "ideas go to Reference Docs" regression in the browser.
//
// NOTE: the dev-story "proposal" pipeline was renamed to "design" (commit
// 5dc4123); the state and this test were updated to match — the discuss /
// capture_existing intents and their priority contract carried over unchanged.
func TestDesignRoom_AllowedIntents_DiscussBeforeCaptureExisting(t *testing.T) {
	appPath := setupDevStoryRepo(t)
	orch, sid := newDevStoryOrchestrator(t, appPath)

	ctx := context.Background()

	// Teleport to the design state with a minimal world (no seed idea
	// required — the room has no entry guard).
	_, err := orch.Teleport(ctx, sid, inbox.TeleportTarget{
		State: app.StatePath("design"),
		Slots: map[string]any{},
	})
	require.NoError(t, err, "teleport to design state")

	// CurrentView returns AllowedIntents sorted by priority desc (same
	// ordering that OrchestratorDriver.IntentInfo uses when building the
	// browser menu).
	view, err := orch.CurrentView(ctx, sid)
	require.NoError(t, err, "CurrentView at design state")
	require.NotEmpty(t, view.AllowedIntents, "design state must have allowed intents")

	discussIdx := slices.Index(view.AllowedIntents, "discuss")
	captureIdx := slices.Index(view.AllowedIntents, "capture_existing")

	require.GreaterOrEqual(t, discussIdx, 0,
		"'discuss' must appear in AllowedIntents for design state; got: %v", view.AllowedIntents)
	require.GreaterOrEqual(t, captureIdx, 0,
		"'capture_existing' must appear in AllowedIntents for design state; got: %v", view.AllowedIntents)
	require.Less(t, discussIdx, captureIdx,
		"'discuss' (priority 85) must sort BEFORE 'capture_existing' (priority 78) in AllowedIntents; "+
			"got discuss at index %d, capture_existing at index %d (full list: %v) — "+
			"this is the regression guard: if capture_existing ≥ discuss in priority, "+
			"the InputBar dropdown defaults to 'Reference Docs' and ideas go to the wrong intent",
		discussIdx, captureIdx, view.AllowedIntents)
}

// TestProposalRoom_IntentSlotBindings asserts that:
//   - discuss has exactly one string slot named "message" (the InputBar
//     binds its textarea to this).
//   - capture_existing has exactly one string slot named "paths" (the
//     InputBar binds its dropdown-selected textarea to this).
//
// Any rename or type change in app.yaml will break the InputBar's text
// binding silently — this test catches the drift before users see it.
func TestProposalRoom_IntentSlotBindings(t *testing.T) {
	appPath := setupDevStoryRepo(t)
	orch, sid := newDevStoryOrchestrator(t, appPath)

	ctx := context.Background()

	_, err := orch.Teleport(ctx, sid, inbox.TeleportTarget{
		State: app.StatePath("proposal"),
	})
	require.NoError(t, err)

	// ── discuss ──────────────────────────────────────────────────────────────

	discussDef, ok := orch.LookupIntent(app.StatePath("proposal"), "discuss")
	require.True(t, ok, "'discuss' must be a known intent at the proposal state")

	discussTextSlot := textSlotForIntent(discussDef)
	require.Equal(t, "message", discussTextSlot,
		"discuss must bind its free-text input to the 'message' slot; "+
			"OrchestratorDriver.IntentInfo's TextSlot would be %q; "+
			"if this changes, the InputBar sends user ideas to the wrong slot",
		discussTextSlot)

	// ── capture_existing ─────────────────────────────────────────────────────

	captureDef, ok := orch.LookupIntent(app.StatePath("proposal"), "capture_existing")
	require.True(t, ok, "'capture_existing' must be a known intent at the proposal state")

	captureTextSlot := textSlotForIntent(captureDef)
	require.Equal(t, "paths", captureTextSlot,
		"capture_existing must bind its free-text input to the 'paths' slot; "+
			"OrchestratorDriver.IntentInfo's TextSlot would be %q; "+
			"if this changes, the InputBar sends reference-doc paths to the wrong slot",
		captureTextSlot)
}
