package studio_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/app/graph"
	studio "kitsoki/internal/mcp/studio"
)

// story_tools_test.go — verification for slice 6 (docs/proposals/
// mcp-authoring-tools.md §2). Every test is deterministic and LLM-free: it loads
// real on-disk stories (stories/bugfix) and a synthetic known-bad copy, and runs
// the flow harness in pure replay/cassette mode.

// repoRoot resolves the repository root from this test file's location
// (internal/mcp/studio → three dirs up).
func repoRoot(t *testing.T) string {
	t.Helper()
	_, here, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller")
	return filepath.Clean(filepath.Join(filepath.Dir(here), "..", "..", ".."))
}

// bugfixDir is the canonical known-good story used across these tests.
func bugfixDir(t *testing.T) string {
	return filepath.Join(repoRoot(t), "stories", "bugfix")
}

// newStudioWithWorkspace builds a studio server bound to the given workspace dir
// and returns an in-process client session.
func newStudioWithWorkspace(ctx context.Context, t *testing.T, dir string) *mcpsdk.ClientSession {
	t.Helper()
	sess := studio.NewStudioSession(stubBuilder())
	def, loadErr := app.Load(filepath.Join(dir, "app.yaml"))
	_, err := sess.OpenWorkspace(studio.OpenWorkspaceParams{Dir: dir, Def: def, LoadErr: loadErr})
	require.NoError(t, err, "open workspace")
	srv := studio.NewServer(sess)
	return connectInProcess(ctx, t, srv)
}

func newStudioWithWorkspaceAndResolver(ctx context.Context, t *testing.T, dir string, resolver app.ImportResolver) *mcpsdk.ClientSession {
	t.Helper()
	sess := studio.NewStudioSession(stubBuilder())
	def, loadErr := app.LoadWithResolver(filepath.Join(dir, "app.yaml"), nil, resolver)
	_, err := sess.OpenWorkspace(studio.OpenWorkspaceParams{Dir: dir, Def: def, LoadErr: loadErr})
	require.NoError(t, err, "open workspace")
	srv := studio.NewServer(sess, studio.WithImportResolver(resolver))
	return connectInProcess(ctx, t, srv)
}

// callStory issues a story.* tool call and decodes the JSON result into out. It
// fails the test if the call returned a structured tool error.
func callStory(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, name string, args map[string]any, out any) {
	t.Helper()
	res, err := callTool(ctx, cs, name, args)
	require.NoError(t, err, "%s call", name)
	require.False(t, res.IsError, "%s should not error: %s", name, contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), out), "decode %s result", name)
}

// ─── 2.2 validate a known-good story → {ok:true} ─────────────────────────────

// TestStoryValidateGood loads stories/bugfix through story.validate and asserts
// the structured result is clean — the same {ok:true} a human gets on a healthy
// `kitsoki run`.
func TestStoryValidateGood(t *testing.T) {
	ctx := context.Background()
	cs := newStudioWithWorkspace(ctx, t, bugfixDir(t))

	var got studio.StoryValidateOK
	callStory(ctx, t, cs, "story.validate", nil, &got)
	assert.True(t, got.OK, "bugfix should validate clean; errors=%+v", got.Errors)
	assert.Empty(t, got.Errors, "no validation errors expected")
}

// ─── 2.1 validate a known-bad story → exact File:Line:Message ─────────────────

// TestStoryValidateBad introduces an undeclared intent in a temp copy of a
// minimal story and asserts story.validate surfaces the exact structured error
// (File + Message) the loader emits — the agent gets the same problem a human
// would on `kitsoki run`, as data.
func TestStoryValidateBad(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	// Minimal story whose idle room references an intent that is never declared.
	const badYAML = `app:
  id: probe-bad
  version: "1"
root: idle
world: {}
states:
  idle:
    description: Idle
    on:
      go_fix:
        - target: done
  done:
    description: Done
    terminal: true
`
	require.NoError(t, os.WriteFile(appPath, []byte(badYAML), 0o644))

	cs := newStudioWithWorkspace(ctx, t, dir)

	var got studio.StoryValidateOK
	callStory(ctx, t, cs, "story.validate", nil, &got)
	require.False(t, got.OK, "undeclared intent must fail validation")
	require.NotEmpty(t, got.Errors, "expected at least one validation error")

	// Exact File + Message: the undeclared-intent invariant.
	found := false
	for _, e := range got.Errors {
		if strings.Contains(e.Message, `"go_fix"`) && strings.Contains(e.Message, "not declared") {
			found = true
			assert.Equal(t, appPath, e.File, "error should name the offending file")
		}
	}
	assert.True(t, found, "expected an undeclared-intent error for go_fix; got %+v", got.Errors)
}

// ─── 2.3 graph: RoomList matches the web editor's dispatch ────────────────────

// TestStoryGraphMatchesEditor asserts story.graph (rooms mode) over bugfix
// returns exactly what the web editor's runstatus.editor.rooms dispatch returns:
// graph.RoomList over the same compiled app. They are the same computation, so
// the agent's graph view and the human's /editor view never disagree.
func TestStoryGraphMatchesEditor(t *testing.T) {
	ctx := context.Background()
	dir := bugfixDir(t)
	cs := newStudioWithWorkspace(ctx, t, dir)

	var got studio.StoryGraphOK
	callStory(ctx, t, cs, "story.graph", nil, &got)
	assert.Equal(t, "rooms", got.Mode)
	require.NotEmpty(t, got.Rooms, "bugfix has rooms")

	// Reproduce the editor's exact construction: app.Load → app.Compile →
	// graph.RoomList (cmd/kitsoki/registry.go EditorApp + editor.go dispatch).
	def, err := app.Load(filepath.Join(dir, "app.yaml"))
	require.NoError(t, err)
	editorRooms := graph.RoomList(app.Compile(def))

	// story.graph drops the UI-only Distance field (token diet) but otherwise
	// returns the editor's exact RoomList in the same order: ID/Label/HasAgent
	// agree element-for-element.
	want := make([]studio.RoomSummaryItem, 0, len(editorRooms))
	for _, r := range editorRooms {
		want = append(want, studio.RoomSummaryItem{ID: r.ID, Label: r.Label, HasAgent: r.HasAgent})
	}
	assert.Equal(t, want, got.Rooms, "story.graph rooms must equal the editor's RoomList (sans Distance)")

	// Sanity: the canonical bugfix rooms are present and idle is the entry (0).
	assert.Equal(t, "idle", got.Rooms[0].ID, "idle is the initial room")
	ids := map[string]bool{}
	for _, r := range got.Rooms {
		ids[r.ID] = true
	}
	for _, want := range []string{"idle", "reproducing", "proposing", "implementing", "testing", "reviewing", "validating", "done"} {
		assert.True(t, ids[want], "room %q present", want)
	}
}

// TestStoryGraphDetailAndAgents exercises the mode selection: a room id selects
// detail, the agents flag selects agent contracts.

func TestStoryGraphSuggestsStoryRootsForRepoDir(t *testing.T) {
	ctx := context.Background()
	cs := newStudioWithWorkspace(ctx, t, bugfixDir(t))

	res, err := callTool(ctx, cs, "story.graph", map[string]any{"dir": repoRoot(t)})
	require.NoError(t, err)
	require.True(t, res.IsError, "repo root without app.yaml should be an actionable structured error")

	var toolErr studio.ToolError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &toolErr))
	assert.Equal(t, studio.ErrBadRequest, toolErr.Code)
	assert.Contains(t, toolErr.Error, "no app.yaml")
	assert.Contains(t, toolErr.Error, "pass dir as a story root")
	assert.Contains(t, toolErr.Error, "stories/")
}

func TestStoryGraphDetailAndAgents(t *testing.T) {
	ctx := context.Background()
	dir := bugfixDir(t)
	cs := newStudioWithWorkspace(ctx, t, dir)

	var detail studio.StoryGraphOK
	callStory(ctx, t, cs, "story.graph", map[string]any{"room": "reproducing"}, &detail)
	assert.Equal(t, "detail", detail.Mode)
	require.NotNil(t, detail.Detail)
	assert.Equal(t, "reproducing", detail.Detail.ID)

	var agents studio.StoryGraphOK
	callStory(ctx, t, cs, "story.graph", map[string]any{"room": "reproducing", "agents": true}, &agents)
	assert.Equal(t, "agents", agents.Mode)

	// detail must equal the editor's graph.Detail for the same room.
	def, err := app.Load(filepath.Join(dir, "app.yaml"))
	require.NoError(t, err)
	wantDetail, ok := graph.Detail(app.Compile(def), "reproducing", filepath.Join(dir, "app.yaml"))
	require.True(t, ok)
	assert.Equal(t, wantDetail.ID, detail.Detail.ID)
	assert.Equal(t, wantDetail.Intents, detail.Detail.Intents)
}

func TestStoryGraphStructuredGraphMode(t *testing.T) {
	ctx := context.Background()
	dir := bugfixDir(t)
	cs := newStudioWithWorkspace(ctx, t, dir)

	var got studio.StoryGraphOK
	callStory(ctx, t, cs, "story.graph", map[string]any{"graph": true}, &got)
	assert.Equal(t, "graph", got.Mode)
	require.NotNil(t, got.Graph)
	assert.Equal(t, graph.SchemaV1, got.Graph.Schema)
	assert.Equal(t, "room-state-machine", got.Graph.Kind)
	assert.True(t, got.Graph.Directed)
	assert.NotEmpty(t, got.Graph.Nodes)
	assert.NotEmpty(t, got.Graph.Edges)

	var hasIdle, hasDone, hasForwardEdge bool
	for _, n := range got.Graph.Nodes {
		if n.ID == "state:idle" {
			hasIdle = true
		}
		if n.ID == "state:done" {
			hasDone = true
		}
	}
	for _, e := range got.Graph.Edges {
		if e.Source == "state:idle" && e.Target != "" && e.Label != "" {
			hasForwardEdge = true
		}
	}
	assert.True(t, hasIdle, "entry room node present")
	assert.True(t, hasDone, "terminal room node present")
	assert.True(t, hasForwardEdge, "entry room has at least one labelled outgoing edge")
}

// ─── 2.4 test: RunFlows over a fixture reproduces `kitsoki test flows` ──────

// TestStoryTestReproducesFlows runs story.test over one concrete flow fixture
// and asserts it passes with no LLM. The full all-story replay remains owned by
// scripts/run-tests.sh; this unit test only proves the MCP tool is wired through
// to the flow runner and returns structured per-fixture results.
func TestStoryTestReproducesFlows(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(repoRoot(t), "stories", "inbox-demo")
	cs := newStudioWithWorkspace(ctx, t, dir)

	var got studio.StoryTestOK
	callStory(ctx, t, cs, "story.test", map[string]any{
		"flows": filepath.Join(dir, "flows", "background_notifies.yaml"),
	}, &got)
	assert.True(t, got.OK, "inbox demo flow should pass; failed=%d", got.Failed)
	assert.Equal(t, 1, got.Passed)
	assert.Equal(t, 0, got.Failed, "no flow failures")
	require.NotEmpty(t, got.Results, "per-fixture results present")
	for _, r := range got.Results {
		assert.True(t, r.Passed || r.Skipped, "fixture %s passed or skipped; failure_count=%d failures=%v", r.File, r.FailureCount, r.Failures)
	}
}

func TestStoryTestResolvesRelativeFlowPathsAgainstStoryDir(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(repoRoot(t), "stories", "inbox-demo")
	cs := newStudioWithWorkspace(ctx, t, dir)

	var got studio.StoryTestOK
	callStory(ctx, t, cs, "story.test", map[string]any{
		"flows": "flows/background_notifies.yaml",
	}, &got)
	assert.True(t, got.OK, "relative flow path should resolve against story dir; failed=%d", got.Failed)
	assert.Equal(t, 1, got.Passed)
	assert.Equal(t, 0, got.Failed)
}

func TestStoryTestResolvesRelativeFlowGlobsAgainstStoryDir(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(repoRoot(t), "stories", "inbox-demo")
	cs := newStudioWithWorkspace(ctx, t, dir)

	var got studio.StoryTestOK
	callStory(ctx, t, cs, "story.test", map[string]any{
		"flows": "flows/background_*.yaml",
	}, &got)
	assert.True(t, got.OK, "relative flow glob should resolve against story dir; failed=%d", got.Failed)
	assert.GreaterOrEqual(t, got.Passed, 1)
	assert.Equal(t, 0, got.Failed)
}

func TestStoryTestExposesCLIFlowOptions(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(repoRoot(t), "stories", "inbox-demo")
	cs := newStudioWithWorkspace(ctx, t, dir)
	outDir := t.TempDir()
	jsonPath := filepath.Join(outDir, "report.json")
	tracePath := filepath.Join(outDir, "trace.jsonl")

	var got studio.StoryTestOK
	callStory(ctx, t, cs, "story.test", map[string]any{
		"flows":     filepath.Join(dir, "flows", "background_notifies.yaml"),
		"json":      jsonPath,
		"trace_out": tracePath,
		"fail_fast": true,
		"verbose":   true,
	}, &got)

	assert.True(t, got.OK, "inbox demo flow should pass; failed=%d", got.Failed)
	assert.Equal(t, 1, got.Passed)
	assert.FileExists(t, jsonPath, "story.test should expose CLI --json")
	assert.FileExists(t, tracePath, "story.test should expose CLI --trace-out")

	var report struct {
		Passed int `json:"passed"`
		Failed int `json:"failed"`
	}
	raw, err := os.ReadFile(jsonPath)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &report))
	assert.Equal(t, got.Passed, report.Passed)
	assert.Equal(t, got.Failed, report.Failed)
}

func TestStoryValidateUsesInjectedImportResolver(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	childDir := filepath.Join(root, "child")
	parentDir := filepath.Join(root, "parent")
	require.NoError(t, os.MkdirAll(childDir, 0o755))
	require.NoError(t, os.MkdirAll(parentDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(childDir, "app.yaml"), []byte(`app:
  id: child
  version: "1"
root: idle
world: {}
states:
  idle:
    description: Child idle
    terminal: true
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(parentDir, "app.yaml"), []byte(`app:
  id: parent
  version: "1"
root: main
world: {}
imports:
  c:
    source: "@kitsoki/child"
    entry: idle
states:
  main:
    description: Main
    terminal: true
`), 0o644))
	resolver := func(name, _ string, override bool) (string, error) {
		if !override || name != "child" {
			return "", nil
		}
		return filepath.Join(childDir, "app.yaml"), nil
	}
	cs := newStudioWithWorkspaceAndResolver(ctx, t, parentDir, resolver)

	var got studio.StoryValidateOK
	callStory(ctx, t, cs, "story.validate", nil, &got)
	assert.True(t, got.OK, "resolver should make @kitsoki/child loadable: %+v", got.Errors)
}

func TestStoryValidateReportsResolverFailure(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte(`app:
  id: parent
  version: "1"
root: main
world: {}
imports:
  c:
    source: "@kitsoki/missing"
    entry: idle
states:
  main:
    description: Main
    terminal: true
`), 0o644))
	resolver := func(name, _ string, override bool) (string, error) {
		if override {
			return "", fmt.Errorf("missing story %s", name)
		}
		return "", nil
	}
	cs := newStudioWithWorkspaceAndResolver(ctx, t, dir, resolver)

	var got studio.StoryValidateOK
	callStory(ctx, t, cs, "story.validate", nil, &got)
	require.False(t, got.OK)
	require.NotEmpty(t, got.Errors)
	assert.Contains(t, got.Errors[0].Message, "missing story missing")
}

// ─── 2.5 path-escape: story.write ../../etc/x is rejected ─────────────────────

// TestStoryWritePathEscape proves an authoring tool cannot write outside the
// story it is authoring: an escaping relative path and an absolute path are both
// rejected with a structured BAD_REQUEST, and no file is created on disk.
func TestStoryWritePathEscape(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.yaml"), []byte("app:\n  id: x\n  version: \"1\"\nroot: idle\nworld: {}\nstates:\n  idle:\n    description: Idle\n    terminal: true\n"), 0o644))
	cs := newStudioWithWorkspace(ctx, t, dir)

	// Sentinel file the escape would clobber if the guard failed.
	escapeTarget := filepath.Join(dir, "..", "ESCAPED.txt")
	_ = os.Remove(escapeTarget)

	res, err := callTool(ctx, cs, "story.write", map[string]any{
		"path":    filepath.Join("..", "ESCAPED.txt"),
		"content": "pwned",
	})
	require.NoError(t, err, "transport ok even for a rejected write")
	require.True(t, res.IsError, "escaping write must be a structured error")

	var toolErr studio.ToolError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &toolErr))
	assert.Equal(t, studio.ErrBadRequest, toolErr.Code)
	assert.Contains(t, toolErr.Error, "escapes the workspace")

	_, statErr := os.Stat(escapeTarget)
	assert.True(t, os.IsNotExist(statErr), "no file should be written outside the workspace")

	// An absolute path is likewise rejected.
	resAbs, err := callTool(ctx, cs, "story.write", map[string]any{
		"path":    filepath.Join(t.TempDir(), "abs.txt"),
		"content": "pwned",
	})
	require.NoError(t, err)
	require.True(t, resAbs.IsError, "absolute write must be rejected")
	var absErr studio.ToolError
	require.NoError(t, json.Unmarshal([]byte(contentText(resAbs)), &absErr))
	assert.Equal(t, studio.ErrBadRequest, absErr.Code)
}

// ─── 1.5 story.write auto-validates in one round-trip ─────────────────────────

// TestStoryWriteAutoValidates writes a malformed room into a temp story and
// asserts the single round-trip returns both the write confirmation AND the
// validation errors — a malformed edit is caught immediately, not on the next
// session.
func TestStoryWriteAutoValidates(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	appPath := filepath.Join(dir, "app.yaml")
	// Start from a clean story.
	const goodYAML = `app:
  id: probe-rw
  version: "1"
root: idle
intents:
  go_done:
    title: Done
world: {}
states:
  idle:
    description: Idle
    intents: [go_done]
    on:
      go_done:
        - target: done
  done:
    description: Done
    terminal: true
`
	require.NoError(t, os.WriteFile(appPath, []byte(goodYAML), 0o644))
	cs := newStudioWithWorkspace(ctx, t, dir)

	// A clean read round-trips the content.
	var read studio.StoryReadOK
	callStory(ctx, t, cs, "story.read", map[string]any{"path": "app.yaml"}, &read)
	assert.Contains(t, read.Content, "probe-rw")

	// Writing an app.yaml that references an undeclared intent must come back
	// with the validation error in the SAME response.
	const badYAML = `app:
  id: probe-rw
  version: "1"
root: idle
world: {}
states:
  idle:
    description: Idle
    on:
      undeclared_intent:
        - target: done
  done:
    description: Done
    terminal: true
`
	var wrote studio.StoryWriteOK
	callStory(ctx, t, cs, "story.write", map[string]any{"path": "app.yaml", "content": badYAML}, &wrote)
	assert.True(t, wrote.OK, "the write itself succeeded")
	assert.Equal(t, "app.yaml", wrote.Written)
	assert.True(t, wrote.Validated, "story package writes should auto-validate")
	require.NotNil(t, wrote.Validation, "validation result returned for story package writes")
	assert.False(t, wrote.Validation.OK, "the written story is invalid")
	require.NotEmpty(t, wrote.Validation.Errors, "validation errors returned in the write round-trip")
	joined := ""
	for _, e := range wrote.Validation.Errors {
		joined += e.Message + "\n"
	}
	assert.Contains(t, joined, "undeclared_intent")
}

func TestStoryWriteSkipsValidationForPlainWorkspace(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cs := newStudioWithWorkspace(ctx, t, dir)

	var wrote studio.StoryWriteOK
	callStory(ctx, t, cs, "story.write", map[string]any{
		"path":    filepath.Join(".context", "note.md"),
		"content": "# Note\n\nPlain workspace file.\n",
	}, &wrote)
	assert.True(t, wrote.OK)
	assert.Equal(t, filepath.Join(".context", "note.md"), wrote.Written)
	assert.False(t, wrote.Validated, "plain workspace writes should not try to load <workspace>/app.yaml")
	assert.Nil(t, wrote.Validation)
	assert.FileExists(t, filepath.Join(dir, ".context", "note.md"))

	forceValidate := true
	res, err := callTool(ctx, cs, "story.write", map[string]any{
		"path":     filepath.Join(".context", "force.md"),
		"content":  "# Force\n",
		"validate": forceValidate,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "forced validation returns structured validation data, not a tool error: %s", contentText(res))
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &wrote))
	assert.True(t, wrote.Validated)
	require.NotNil(t, wrote.Validation)
	assert.False(t, wrote.Validation.OK)
	require.NotEmpty(t, wrote.Validation.Errors)
	assert.Contains(t, wrote.Validation.Errors[0].Message, "app.yaml")
}

// ─── no-workspace guard ───────────────────────────────────────────────────────

// TestStoryToolsNoWorkspace proves a story.* tool with no workspace bound and no
// dir override returns a structured NO_WORKSPACE error (principle of least
// surprise — never a panic or a silent empty result).
func TestStoryToolsNoWorkspace(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()))
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "story.validate", nil)
	require.NoError(t, err)
	require.True(t, res.IsError, "no workspace → structured error")
	var toolErr studio.ToolError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &toolErr))
	assert.Equal(t, studio.ErrNoWorkspace, toolErr.Code)
}

// TestStoryToolsListed confirms the five story.* tools register under their
// dotted names alongside the server-core tools.
func TestStoryToolsListed(t *testing.T) {
	ctx := context.Background()
	srv := studio.NewServer(studio.NewStudioSession(stubBuilder()))
	cs := connectInProcess(ctx, t, srv)

	res, err := cs.ListTools(ctx, &mcpsdk.ListToolsParams{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"story.read", "story.write", "story.validate", "story.graph", "story.test"} {
		assert.True(t, names[want], "%s registered", want)
	}
}
