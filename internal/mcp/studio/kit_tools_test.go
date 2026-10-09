package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/kitgit"
	"kitsoki/internal/kitlock"
	"kitsoki/internal/kitstage"
	"kitsoki/internal/kittrial"
	"kitsoki/internal/kitver"
	studio "kitsoki/internal/mcp/studio"
)

// kit_tools_test.go — verification for the S7 kit lifecycle over MCP
// (docs/proposals/kit-lifecycle.md, PR5). Every test is deterministic and
// LLM-free: the widget v1→v2 fixture mirrors cmd/kitsoki/kit_trial_test.go
// (a renamed exported intent with a compat hint plus a new required host),
// and the trial gates replay no-LLM flow fixtures only.

const mcpWidgetV1 = `app: { id: widget, version: 1.2.0 }
hosts: [host.run]
world:
  count: { type: int, default: 0 }
intents:
  bump: { description: "bump count", examples: [bump] }
exports:
  intents: [bump]
root: idle
states:
  idle:
    view: "idle"
    on:
      bump:
        - target: .
          effects:
            - set: { count: 1 }
`

const mcpWidgetV2 = `app: { id: widget, version: 1.3.0 }
hosts: [host.run, host.local]
world:
  count: { type: int, default: 0 }
intents:
  nudge: { description: "bump count (renamed from bump)", examples: [nudge] }
exports:
  intents: [nudge]
root: idle
states:
  idle:
    view: "idle"
    on:
      nudge:
        - target: .
          effects:
            - set: { count: 1 }
`

const mcpWidgetV2KitYAML = `schema: kit/v1
kit: widget
namespace: kitsoki
version: 1.3.0
provides:
  stories: [widget]
  story_dirs:
    widget: "."
compat:
  renamed:
    intents:
      bump: nudge
`

const mcpConsumerV1 = `app: { id: proj-dev, version: 0.1.0 }
hosts:
  - host.run
imports:
  core:
    source: "@kitsoki/widget"
    entry: idle
    hosts: declared
    intents:
      import: [bump]
root: core
`

const mcpConsumerV2 = `app: { id: proj-dev, version: 0.1.0 }
hosts:
  - host.run
  - host.local
imports:
  core:
    source: "@kitsoki/widget"
    entry: idle
    hosts: declared
    intents:
      import: [nudge]
root: core
`

const mcpFixtureV1 = `test_kind: flow
app: ../app.yaml
initial_state: core.idle
turns:
  - intent: { name: bump }
    expect_state: core.idle
    expect_world: { core__count: 1 }
expect_no_errors: true
`

const mcpFixtureV2 = `test_kind: flow
app: ../app.yaml
initial_state: core.idle
turns:
  - intent: { name: nudge }
    expect_state: core.idle
    expect_world: { core__count: 1 }
expect_no_errors: true
`

// kitTestResolveEntry is the test twin of cmd/kitsoki's resolveKitEntry for
// non-git sources: resolve, load, gate on the constraint, hash the tree.
func kitTestResolveEntry(ctx context.Context, source, importerDir, constraint string) (*kitlock.Entry, string, error) {
	appPath, err := app.ResolveSource(source, importerDir, nil)
	if err != nil {
		return nil, "", err
	}
	def, err := app.LoadWithResolver(appPath, nil, nil)
	if err != nil {
		return nil, "", err
	}
	if constraint != "" {
		ok, err := kitver.Satisfies(def.App.Version, constraint)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			return nil, "", fmt.Errorf("resolved version %q does not satisfy constraint %q", def.App.Version, constraint)
		}
	}
	dir := filepath.Dir(appPath)
	hash, err := kitgit.DirTreeHash(dir)
	if err != nil {
		return nil, "", err
	}
	return &kitlock.Entry{Source: source, Version: def.App.Version, TreeHash: hash}, dir, nil
}

// setupKitProject builds a consumer project locked at widget v1 (tree cache
// pinned, like `kit add` does) with the upstream lib already mutated to v2,
// returning (kitDir, projectRoot, deps). Mirrors setupTrialProject in
// cmd/kitsoki/kit_trial_test.go.
func setupKitProject(t *testing.T) (string, string, studio.KitTrialDeps) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// Isolate from any real ~/.kitsoki state (kit-dev overrides, repo pin)
	// and from an ambient staged-resolution opt-in.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("KITSOKI_REPO", "")
	t.Setenv(kitstage.EnvStaged, "")

	lib := t.TempDir()
	kitDir := filepath.Join(lib, "widget")
	require.NoError(t, os.MkdirAll(kitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(kitDir, "app.yaml"), []byte(mcpWidgetV1), 0o644))

	target := t.TempDir()
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")
	require.NoError(t, os.MkdirAll(filepath.Join(instDir, "flows"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(instDir, "app.yaml"), []byte(mcpConsumerV1), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(instDir, "flows", "bump_route.yaml"), []byte(mcpFixtureV1), 0o644))

	// Lock at v1 with the recorded ^1.0.0 constraint — the same pinning
	// `kitsoki kit add` performs (tree-cache snapshot + lock entry).
	_, v1Hash, err := kitgit.MaterializeTree(kitDir)
	require.NoError(t, err, "pin v1 tree")
	lf := kitlock.New()
	lf.Kits["widget"] = &kitlock.Entry{Source: kitDir, Version: "1.2.0", TreeHash: v1Hash, Constraint: "^1.0.0"}
	require.NoError(t, kitlock.Save(kitlock.Path(target), lf))

	// Upstream ships v2: renamed intent (compat hint) + new required host.
	require.NoError(t, os.WriteFile(filepath.Join(kitDir, "app.yaml"), []byte(mcpWidgetV2), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(kitDir, "kit.yaml"), []byte(mcpWidgetV2KitYAML), 0o644))

	return kitDir, target, studio.KitTrialDeps{ResolveEntry: kitTestResolveEntry}
}

// newKitStudio binds a studio server to the consumer instance dir (kit tools
// walk up from the workspace to the project root holding kits.lock).
func newKitStudio(ctx context.Context, t *testing.T, workspaceDir string, deps studio.KitTrialDeps, opts ...studio.ServerOption) *mcpsdk.ClientSession {
	t.Helper()
	sess := studio.NewStudioSession(stubBuilder())
	def, loadErr := app.Load(filepath.Join(workspaceDir, "app.yaml"))
	_, err := sess.OpenWorkspace(studio.OpenWorkspaceParams{Dir: workspaceDir, Def: def, LoadErr: loadErr})
	require.NoError(t, err, "open workspace")
	srv := studio.NewServer(sess, append([]studio.ServerOption{studio.WithKitTrialDeps(deps)}, opts...)...)
	return connectInProcess(ctx, t, srv)
}

// callKit issues a kit.* tool call and decodes the JSON result into out,
// failing the test on a structured tool error.
func callKit(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	res, err := callTool(ctx, cs, name, args)
	require.NoError(t, err, "%s call", name)
	require.False(t, res.IsError, "%s should not error: %s", name, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), out), "decode %s result", name)
}

// ─── registration & schemas ───────────────────────────────────────────────────

// TestKitTools_Registration pins the registry contract: the five kit.* tools
// ride the default toolbox; a read-only server keeps only kit.status (every
// other verb writes project state); and the staged opt-in is present on
// story.validate / story.test as a proper object schema (the boolean-schema
// trap tool_schema_test.go guards is covered there for all kit tools too).
func TestKitTools_Registration(t *testing.T) {
	ctx := context.Background()

	toolProps := func(cs *mcpsdk.ClientSession) map[string]map[string]any {
		res, err := cs.ListTools(ctx, nil)
		require.NoError(t, err, "tools/list")
		out := map[string]map[string]any{}
		for _, tool := range res.Tools {
			raw, err := json.Marshal(tool.InputSchema)
			require.NoError(t, err)
			var m map[string]any
			require.NoError(t, json.Unmarshal(raw, &m))
			props, _ := m["properties"].(map[string]any)
			out[tool.Name] = props
		}
		return out
	}

	full := toolProps(newStudioHostRunner(ctx, t))
	for _, name := range []string{"kit.status", "kit.update", "kit.trial", "kit.accept", "kit.reject"} {
		assert.Contains(t, full, name, "default toolbox should register %s", name)
	}
	for _, name := range []string{"story.validate", "story.test"} {
		staged, ok := full[name]["staged"]
		require.Truef(t, ok, "%s should expose the staged opt-in", name)
		_, isObject := staged.(map[string]any)
		assert.Truef(t, isObject, "%s staged schema must be an object, got %T", name, staged)
	}
	assert.Contains(t, full["kit.update"], "check_only", "kit.update exposes check_only")
	assert.Contains(t, full["kit.accept"], "allow_partial", "kit.accept exposes allow_partial")

	sess := studio.NewStudioSession(stubBuilder())
	readOnly := toolProps(connectInProcess(ctx, t, studio.NewServer(sess, studio.ReadOnly())))
	assert.Contains(t, readOnly, "kit.status", "kit.status is a pure read — read-only keeps it")
	for _, name := range []string{"kit.update", "kit.trial", "kit.accept", "kit.reject"} {
		assert.NotContainsf(t, readOnly, name, "%s writes project state — read-only must omit it", name)
	}
}

// ─── kit.status / kit.update / kit.reject ─────────────────────────────────────

// TestKitUpdateStatusReject_OverMCP drives the staging half of the lifecycle:
// check_only plans without staging, kit.update stages the semver-gated
// candidate (kits.lock untouched), kit.status joins lock + staged, and
// kit.reject drops the candidate residue-free.
func TestKitUpdateStatusReject_OverMCP(t *testing.T) {
	ctx := context.Background()
	_, target, deps := setupKitProject(t)
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")
	cs := newKitStudio(ctx, t, instDir, deps)

	// Baseline status: locked at v1, nothing staged.
	var st studio.KitStatusOK
	callKit(ctx, t, cs, "kit.status", nil, &st)
	require.Len(t, st.Kits, 1)
	assert.Equal(t, target, st.Target, "target should resolve to the project root above the workspace")
	assert.Equal(t, "widget", st.Kits[0].Name)
	assert.Equal(t, "1.2.0", st.Kits[0].Version)
	assert.Equal(t, "^1.0.0", st.Kits[0].Constraint)
	assert.Nil(t, st.Kits[0].Staged, "nothing staged yet")

	// check_only: a plan, no staging.
	var chk studio.KitUpdateOK
	callKit(ctx, t, cs, "kit.update", map[string]any{"name": "widget", "check_only": true}, &chk)
	assert.False(t, chk.Staged)
	assert.Equal(t, "1.3.0", chk.To.Version)
	assert.False(t, kitstage.Exists(kitstage.Path(target)), "check_only must stage nothing")

	// Real staging, detailed: the compat rename hint is annotated with the
	// consumer reference it touches.
	var up studio.KitUpdateOK
	callKit(ctx, t, cs, "kit.update", map[string]any{"name": "widget", "detailed": true}, &up)
	assert.True(t, up.Staged)
	assert.Equal(t, "1.2.0", up.From.Version)
	assert.Equal(t, "1.3.0", up.To.Version)
	assert.Greater(t, up.ChangedFilesCount, 0, "v2 changed app.yaml and added kit.yaml")
	require.GreaterOrEqual(t, up.RenameHintsCount, 1)
	require.NotEmpty(t, up.RenameHints, "detailed=true returns the hints")
	hint := up.RenameHints[0]
	assert.Equal(t, "bump", hint.Old)
	assert.Equal(t, "nudge", hint.New)
	assert.NotEmpty(t, hint.Detected, "the consumer imports the renamed intent — hint must be detected")
	assert.True(t, kitstage.Exists(kitstage.Path(target)), "candidate staged")

	// The accepted lock is untouched by staging.
	lf, err := kitlock.Load(kitlock.Path(target))
	require.NoError(t, err)
	assert.Equal(t, "1.2.0", lf.Kits["widget"].Version, "kits.lock must never move on update")

	// Status now shows the staged candidate beside the lock.
	callKit(ctx, t, cs, "kit.status", nil, &st)
	require.NotNil(t, st.Kits[0].Staged)
	assert.Equal(t, "1.3.0", st.Kits[0].Staged.Version)

	// Reject: residue-free by construction.
	var rej studio.KitRejectOK
	callKit(ctx, t, cs, "kit.reject", map[string]any{"name": "widget"}, &rej)
	assert.Equal(t, "1.3.0", rej.Version)
	assert.False(t, kitstage.Exists(kitstage.Path(target)), "staged lockfile gone after reject")
	_, statErr := os.Stat(kitstage.WorkDir(target, "widget"))
	assert.True(t, os.IsNotExist(statErr), "kit-update workdir gone after reject")

	// Rejecting again is a structured error, not a panic or a silent ok.
	res, err := callTool(ctx, cs, "kit.reject", map[string]any{"name": "widget"})
	require.NoError(t, err)
	assert.True(t, res.IsError, "rejecting with nothing staged must be a structured error")
}

// ─── kit.trial / kit.accept ───────────────────────────────────────────────────

// TestKitLifecycle_OverMCP is the S7 acceptance loop driven entirely over
// the studio MCP: stage, trial to blocked (the worklist names both required
// edits), prove accept fails closed, apply exactly the worklist edits,
// re-trial to ready, accept — lock promoted, constraint kept, staging gone.
func TestKitLifecycle_OverMCP(t *testing.T) {
	ctx := context.Background()
	_, target, deps := setupKitProject(t)
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")
	cs := newKitStudio(ctx, t, instDir, deps)

	var up studio.KitUpdateOK
	callKit(ctx, t, cs, "kit.update", map[string]any{"name": "widget"}, &up)
	require.True(t, up.Staged)

	// Trial #1: blocked, as DATA (not a tool error), with the open worklist
	// items naming both edits under detailed=true.
	var tr studio.KitTrialOK
	callKit(ctx, t, cs, "kit.trial", map[string]any{"name": "widget", "detailed": true}, &tr)
	assert.Equal(t, kittrial.ResultBlocked, tr.Result, "consumer still imports the old intent — blocked")
	assert.Greater(t, tr.Worklist.Open, 0)
	require.NotEmpty(t, tr.OpenItems, "detailed=true returns the open items")
	joined := ""
	for _, it := range tr.OpenItems {
		joined += it.Detail + " | " + it.SuggestedAction + "\n"
	}
	assert.Contains(t, joined, "host.local", "worklist should name the missing host declaration")
	assert.Contains(t, joined, "nudge", "worklist should carry the bump->nudge rename")
	require.NotEmpty(t, tr.Gates)
	for _, g := range tr.Gates {
		assert.Zerof(t, g.CostUSD, "gate %s must measure $0 live spend", g.ID)
	}

	// Accept must refuse a blocked trial, structurally.
	res, err := callTool(ctx, cs, "kit.accept", map[string]any{"name": "widget"})
	require.NoError(t, err)
	require.True(t, res.IsError, "accept must fail closed on a blocked trial")
	assert.Contains(t, contentText(res), "blocked")

	// Apply exactly the worklist edits (rename import + declare the host;
	// the fixture rename is the same mechanical edit applied to evidence).
	require.NoError(t, os.WriteFile(filepath.Join(instDir, "app.yaml"), []byte(mcpConsumerV2), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(instDir, "flows", "bump_route.yaml"), []byte(mcpFixtureV2), 0o644))

	// Trial #2: ready. The fixture now fails at the LOCKED version — an
	// advisory stale-baseline warning, never an upgrade failure.
	callKit(ctx, t, cs, "kit.trial", map[string]any{"name": "widget", "detailed": true}, &tr)
	assert.Equal(t, kittrial.ResultReady, tr.Result, "worklist edits applied — ready; items=%v", tr.OpenItems)
	assert.NotEmpty(t, tr.ReceiptPath)

	// Accept: lock promoted, constraint kept, receipt written, staging gone.
	var acc studio.KitAcceptOK
	callKit(ctx, t, cs, "kit.accept", map[string]any{"name": "widget"}, &acc)
	assert.Equal(t, "1.3.0", acc.Version)
	assert.Empty(t, acc.AcceptedWith, "a ready trial accepts cleanly")

	lf, err := kitlock.Load(kitlock.Path(target))
	require.NoError(t, err)
	entry := lf.Kits["widget"]
	require.NotNil(t, entry)
	assert.Equal(t, "1.3.0", entry.Version)
	assert.Equal(t, "^1.0.0", entry.Constraint, "accept keeps the recorded constraint")
	assert.False(t, kitstage.Exists(kitstage.Path(target)), "staged lockfile gone after accept")
	receipt, err := kittrial.LoadReceipt(kittrial.AcceptReceiptPath(target, "widget", "1.3.0"))
	require.NoError(t, err)
	require.NotNil(t, receipt, "durable acceptance receipt written")
	assert.Equal(t, kittrial.ResultAccepted, receipt.Result)
	assert.Zero(t, receipt.Spend.CostUSD, "the whole lifecycle must measure $0 live spend")
}

// TestKitTrial_NoStagedCandidateIsStructuredError pins the empty-staging
// posture: trialing a kit that was never staged is a structured tool error
// telling the agent to run kit.update first.
func TestKitTrial_NoStagedCandidateIsStructuredError(t *testing.T) {
	ctx := context.Background()
	_, target, deps := setupKitProject(t)
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")
	cs := newKitStudio(ctx, t, instDir, deps)

	res, err := callTool(ctx, cs, "kit.trial", map[string]any{"name": "widget"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	assert.Contains(t, contentText(res), "kit update")
}

// ─── staged resolution on story.validate / story.test ─────────────────────────

// TestStoryTools_StagedResolvesCandidate proves the per-call staged opt-in:
// with the server's resolver pinned to the ACCEPTED v1 bytes, a consumer
// already migrated to v2 fails validate/test normally and passes with
// staged:true — and the wrap is per-call (a following unstaged call still
// resolves v1).
func TestStoryTools_StagedResolvesCandidate(t *testing.T) {
	ctx := context.Background()
	kitDir, target, _ := setupKitProject(t)
	instDir := filepath.Join(target, ".kitsoki", "stories", "proj-dev")

	// Preserve the accepted v1 bytes in a live dir for the server resolver.
	v1Dir := filepath.Join(t.TempDir(), "widget-v1")
	require.NoError(t, os.MkdirAll(v1Dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(v1Dir, "app.yaml"), []byte(mcpWidgetV1), 0o644))
	acceptedResolver := func(name, importerDir string, override bool) (string, error) {
		if override && name == "widget" {
			return filepath.Join(v1Dir, "app.yaml"), nil
		}
		return "", nil
	}

	// Stage the v2 candidate (pinned in the tree cache, like kit.update).
	_, v2Hash, err := kitgit.MaterializeTree(kitDir)
	require.NoError(t, err)
	require.NoError(t, kitstage.Stage(target, "widget", &kitstage.Entry{
		Source: kitDir, Version: "1.3.0", TreeHash: v2Hash,
	}))

	// The consumer has already migrated to v2 (imports the renamed intent).
	require.NoError(t, os.WriteFile(filepath.Join(instDir, "app.yaml"), []byte(mcpConsumerV2), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(instDir, "flows", "bump_route.yaml"), []byte(mcpFixtureV2), 0o644))

	sess := studio.NewStudioSession(stubBuilder())
	srv := studio.NewServer(sess, studio.WithImportResolver(acceptedResolver))
	cs := connectInProcess(ctx, t, srv)

	// Unstaged: the accepted v1 kit does not export `nudge` — validate fails.
	var plain studio.StoryValidateOK
	callStory(ctx, t, cs, "story.validate", map[string]any{"dir": instDir}, &plain)
	require.False(t, plain.OK, "v2 consumer against accepted v1 must fail validation")
	joined := ""
	for _, e := range plain.Errors {
		joined += e.Message + "\n"
	}
	assert.True(t, strings.Contains(joined, "nudge") || strings.Contains(joined, "host.local"),
		"failure should implicate the v2 surface: %s", joined)

	// staged:true resolves the candidate for this call only.
	var stagedOK studio.StoryValidateOK
	callStory(ctx, t, cs, "story.validate", map[string]any{"dir": instDir, "staged": true}, &stagedOK)
	assert.True(t, stagedOK.OK, "v2 consumer against the staged v2 candidate validates clean: %+v", stagedOK.Errors)

	// story.test with staged:true replays the migrated fixture green.
	var testOK studio.StoryTestOK
	callStory(ctx, t, cs, "story.test", map[string]any{"dir": instDir, "staged": true}, &testOK)
	assert.True(t, testOK.OK, "staged flow replay should pass: %+v", testOK.Results)
	assert.Equal(t, 1, testOK.Passed)

	// The wrap was per-call: an unstaged call afterwards still sees v1.
	callStory(ctx, t, cs, "story.validate", map[string]any{"dir": instDir}, &plain)
	assert.False(t, plain.OK, "the long-lived server's resolver must not have been mutated")
}
