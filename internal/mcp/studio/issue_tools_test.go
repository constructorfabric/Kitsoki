package studio_test

// issue_tools_test.go — issue.create verification (no LLM, no network, no gh).
// The IssueFiler seam is faked, the webShot seam is stubbed, and assets land in
// a temp dir, so the whole tool runs offline:
//
//   - assets render to disk (tui_png / tui_text / web) and are referenced in the
//     body by their written path
//   - a handle's trace + inspect snapshot are bundled into the body
//   - source-autonomous is always applied (first), alongside caller labels
//   - the composed {repo, title, body, labels} reaches the filer verbatim
//   - local artifact filing works without a GitHub filer
//   - sink=github with no filer wired → structured ErrIssueUnavailable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/app"
	"kitsoki/internal/bugprivacy"
	studio "kitsoki/internal/mcp/studio"
	"kitsoki/internal/store"
)

// recordingFiler is a fake studio.IssueFiler: it records the request and returns
// a canned issue. No gh, no network.
type recordingFiler struct {
	got studio.IssueRequest
}

func (r *recordingFiler) file(_ context.Context, req studio.IssueRequest) (studio.IssueResult, error) {
	r.got = req
	return studio.IssueResult{URL: "https://github.com/constructorfabric/Kitsoki/issues/42", Number: 42}, nil
}

// newIssueServer builds a replay-backed studio with a fake filer, a temp
// artifacts dir, and a stub webShot — everything issue.create needs, offline.
func newIssueServer(t *testing.T) (*studio.Server, *recordingFiler, string) {
	t.Helper()
	filer := &recordingFiler{}
	dir := t.TempDir()
	sess := studio.NewStudioSession(replayBuilder())
	srv := studio.NewServer(sess,
		studio.WithIssueFiler(filer.file),
		studio.WithIssueSink(studio.IssueSinkGitHub),
		studio.WithArtifactsDir(dir),
	)
	srv.SetWebShot(func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		return synthPNG(t), nil
	})
	return srv, filer, dir
}

func issueResult(t *testing.T, res *mcpsdk.CallToolResult) studio.IssueCreateResult {
	t.Helper()
	require.False(t, res.IsError, "issue.create errored: %s", contentText(res))
	var out studio.IssueCreateResult
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &out))
	return out
}

func issueSidecarDir(t *testing.T, root, slug string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, slug+"*", "trace.redacted.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, matches, "trace sidecar for %q not found under %s", slug, root)
	return filepath.Dir(matches[0])
}

// TestIssueCreate_BundlesEvidenceAndFiles is the main path: render three asset
// kinds, bundle a handle's trace + inspect, and file via the fake — asserting
// the assets hit disk, the body carries the evidence, and the filer saw the
// composed issue with source-autonomous applied.
func TestIssueCreate_BundlesEvidenceAndFiles(t *testing.T) {
	ctx := context.Background()
	srv, filer, dir := newIssueServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	// Drive a turn so the trace + inspect have something to bundle.
	res, err := callTool(ctx, cs, "session.drive", map[string]any{"handle": handle, "input": "go west"})
	require.NoError(t, err)
	require.Equal(t, "cloakroom", driveResult(t, res).Outcome.State)

	res, err = callTool(ctx, cs, "issue.create", map[string]any{
		"title":           "[MCP gap] session.drive cannot do X",
		"body":            "## Goal\nDrive a thing.\n",
		"labels":          []string{"enhancement"},
		"handle":          handle,
		"include_trace":   true,
		"include_inspect": true,
		"assets": []map[string]any{
			{"kind": "tui_png", "caption": "the cloakroom", "handle": handle},
			{"kind": "tui_text", "name": "screen", "handle": handle},
			{"kind": "web", "handle": handle},
		},
	})
	require.NoError(t, err)
	out := issueResult(t, res)

	// Result surfaces the filed issue.
	assert.True(t, out.OK)
	assert.Equal(t, "https://github.com/constructorfabric/Kitsoki/issues/42", out.URL)
	assert.Equal(t, 42, out.Number)

	// source-autonomous is applied first, caller labels preserved.
	require.GreaterOrEqual(t, len(out.Labels), 2)
	assert.Equal(t, "source-autonomous", out.Labels[0], "autonomous label leads")
	assert.Contains(t, out.Labels, "enhancement")

	// Three assets written to disk under the temp artifacts dir.
	require.Len(t, out.Assets, 3)
	for _, p := range out.Assets {
		assert.FileExists(t, p)
		assert.Contains(t, p, dir, "asset lands under the configured artifacts dir")
	}
	// The PNG asset is a valid PNG; the text asset is non-empty.
	pngBytes, err := os.ReadFile(out.Assets[0])
	require.NoError(t, err)
	_, err = png.Decode(bytes.NewReader(pngBytes))
	assert.NoError(t, err, "tui_png asset decodes as PNG")
	txtBytes, err := os.ReadFile(out.Assets[1])
	require.NoError(t, err)
	assert.NotEmpty(t, txtBytes, "tui_text asset has the frame text")

	// The filer saw the composed body: narrative + context + trace + assets.
	body := filer.got.Body
	assert.Contains(t, body, "## Goal", "agent narrative preserved")
	assert.Contains(t, body, "## Context — session", "inspect snapshot bundled")
	assert.Contains(t, body, "## Trace", "trace bundled")
	assert.Contains(t, body, "## Assets", "assets section present")
	assert.Contains(t, body, "stopgap", "upload-pending stopgap noted")
	assert.Contains(t, body, "```kitsoki", "runtime metadata block present")
	assert.Contains(t, body, "engine_version: 0.0.1-scaffold")
	assert.Contains(t, body, "engine_checksum_sha256: sha256:")
	assert.Contains(t, body, "story_app_id: cloak-of-darkness")
	assert.Contains(t, body, "story_checksum_sha256: sha256:")
	for _, p := range out.Assets {
		assert.Contains(t, body, p, "each asset referenced by its path")
	}
	assert.Equal(t, out.Labels, filer.got.Labels, "filer got the final labels")
	assert.Equal(t, "[MCP gap] session.drive cannot do X", filer.got.Title)

	// Token-diet: bulky machine context (full world + full pretty trace) is spilled
	// to sidecar files under the issue's artifacts dir, linked from the body, not
	// inlined wholesale.
	slugDir := filepath.Dir(out.Assets[0])
	assert.FileExists(t, filepath.Join(slugDir, "trace.redacted.jsonl"), "full redacted trace sidecar written")
	assert.FileExists(t, filepath.Join(slugDir, "world.redacted.json"), "full redacted world sidecar written")
	assert.Contains(t, body, "trace.redacted.jsonl", "body links the trace sidecar")
	assert.Contains(t, body, "world.redacted.json", "body links the world sidecar")
	// The inlined trace stays compact (one JSON object per line, not indented).
	assert.NotContains(t, body, "\n    \"", "inlined trace is compact, not pretty-printed")
}

func TestIssueCreate_DefaultsToCurrentSessionEvidenceAndRedacts(t *testing.T) {
	ctx := context.Background()
	srv, filer, dir := newIssueServer(t)
	cs := connectInProcess(ctx, t, srv)
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)
	diagnosticText := "email alice@example.com and read " + home + "/secret.txt"

	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"trace":      t.TempDir() + "/trace.jsonl",
		"initial_world": map[string]any{
			"diagnostic": diagnosticText,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new errored: %s", contentText(res))

	res, err = callTool(ctx, cs, "issue.create", map[string]any{
		"title": "MCP default evidence",
		"body":  "The agent should not have to assemble session evidence.",
	})
	require.NoError(t, err)
	out := issueResult(t, res)

	assert.True(t, out.OK)
	assert.Empty(t, out.Assets, "default session context is sidecar/body evidence, not rendered assets")
	body := filer.got.Body
	assert.Contains(t, body, "## Context — session `s1`", "current session inspect snapshot inferred")
	assert.Contains(t, body, "## Trace (last", "current session trace inferred")
	assert.Contains(t, body, "redacted", "evidence is explicitly marked redacted")
	assert.Contains(t, body, "trace.redacted.jsonl")
	assert.Contains(t, body, "world.redacted.json")
	assert.NotContains(t, body, "alice@example.com")
	assert.NotContains(t, body, home+"/secret.txt")

	slugDir := issueSidecarDir(t, dir, "mcp-default-evidence")
	traceData, err := os.ReadFile(filepath.Join(slugDir, "trace.redacted.jsonl"))
	require.NoError(t, err)
	worldData, err := os.ReadFile(filepath.Join(slugDir, "world.redacted.json"))
	require.NoError(t, err)
	for _, evidence := range []string{string(traceData), string(worldData)} {
		assert.NotContains(t, evidence, "alice@example.com")
		assert.NotContains(t, evidence, home+"/secret.txt")
	}

	res, err = callTool(ctx, cs, "issue.create", map[string]any{
		"title":           "MCP text only",
		"body":            "The caller deliberately disabled session evidence.",
		"include_trace":   false,
		"include_inspect": false,
	})
	require.NoError(t, err)
	_ = issueResult(t, res)
	assert.NotContains(t, filer.got.Body, "## Context")
	assert.NotContains(t, filer.got.Body, "## Trace")
}

func TestIssueCreate_DuplicateTitlesUseDistinctArtifactDirs(t *testing.T) {
	ctx := context.Background()
	srv, _, dir := newIssueServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)

	for i := 0; i < 2; i++ {
		res, err := callTool(ctx, cs, "issue.create", map[string]any{
			"title":           "Repeated MCP evidence",
			"body":            "same title should not overwrite sidecars",
			"handle":          handle,
			"include_trace":   true,
			"include_inspect": true,
		})
		require.NoError(t, err)
		out := issueResult(t, res)
		require.True(t, out.OK)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "repeated-mcp-evidence*", "trace.redacted.jsonl"))
	require.NoError(t, err)
	require.Len(t, matches, 2)
	assert.NotEqual(t, filepath.Dir(matches[0]), filepath.Dir(matches[1]), "same-title reports need isolated evidence dirs")
}

func TestIssueCreate_BundlesExplicitTracePathWithoutCurrentHandle(t *testing.T) {
	ctx := context.Background()
	srv, filer, dir := newIssueServer(t)
	cs := connectInProcess(ctx, t, srv)
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home)

	// Keep a current MCP session open to prove trace_path suppresses the old
	// "current session" inference. This is a different session than the TUI
	// trace we want to report.
	res, err := callTool(ctx, cs, "session.new", map[string]any{
		"story_path": cloakApp,
		"harness":    "replay",
		"cassette":   cloakCassette,
		"trace":      filepath.Join(t.TempDir(), "mcp-current.jsonl"),
		"initial_world": map[string]any{
			"diagnostic": "current-session-only",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "session.new errored: %s", contentText(res))

	tracePath := writeIssueTrace(t, filepath.Join(home, ".kitsoki", "sessions", "kitsoki-dev", "abc123-tui.jsonl"), map[string]any{
		"ticket_id":           "TUI-7",
		"diagnostic":          "email alice@example.com and read " + home + "/secret.txt",
		"last_error":          "operator could not file a bug from the TUI",
		"session_cost_usd":    0.25,
		"kitsoki_dev__status": "needs-human",
	})

	res, err = callTool(ctx, cs, "issue.create", map[string]any{
		"title":       "TUI trace report",
		"body":        "The bug happened in a TUI session, not this MCP session.",
		"trace_path":  tracePath,
		"trace_limit": 10,
	})
	require.NoError(t, err)
	out := issueResult(t, res)
	assert.True(t, out.OK)

	body := filer.got.Body
	assert.Contains(t, body, "## Context - trace", "trace context bundled")
	assert.Contains(t, body, "abc123-tui.jsonl", "source trace named")
	assert.Contains(t, body, "## Trace", "trace tail bundled")
	assert.Contains(t, body, "reconstructed world", "world reconstructed from world.update events")
	assert.NotContains(t, body, "## Context — session", "current MCP handle must not be inferred when trace_path is supplied")
	assert.NotContains(t, body, "current-session-only")
	assert.NotContains(t, body, "alice@example.com")
	assert.NotContains(t, body, home+"/secret.txt")

	slugDir := issueSidecarDir(t, dir, "tui-trace-report")
	traceData, err := os.ReadFile(filepath.Join(slugDir, "trace.redacted.jsonl"))
	require.NoError(t, err)
	worldData, err := os.ReadFile(filepath.Join(slugDir, "world.redacted.json"))
	require.NoError(t, err)
	for _, evidence := range []string{body, string(traceData), string(worldData)} {
		assert.NotContains(t, evidence, "alice@example.com")
		assert.NotContains(t, evidence, home+"/secret.txt")
	}
	assert.Contains(t, string(traceData), "world.update")
}

func TestIssueCreate_TraceRefResolvesRepoLocalTraceByAppAndTicket(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", filepath.Join(t.TempDir(), "home"))
	srv, filer, _ := newIssueServer(t)
	cs := connectInProcess(ctx, t, srv)

	tracePath := writeIssueTrace(t, filepath.Join(root, ".kitsoki", "sessions", "20260707T120000Z-kitsoki-dev-xyz987.jsonl"), map[string]any{
		"ticket_id":          "BUG-123",
		"diagnostic":         "repo-local TUI trace",
		"needs_human_reason": "TUI bug surface was unavailable",
	})

	res, err := callTool(ctx, cs, "issue.create", map[string]any{
		"title":        "TUI trace by ref",
		"body":         "Resolved from the repo-local .kitsoki/sessions trace cache.",
		"trace_ref":    "xyz987",
		"trace_app":    "kitsoki-dev",
		"trace_ticket": "BUG-123",
	})
	require.NoError(t, err)
	_ = issueResult(t, res)

	body := filer.got.Body
	assert.Contains(t, body, filepath.Base(tracePath))
	assert.Contains(t, body, "## Context - trace")
	assert.Contains(t, body, "## Trace")
	assert.Contains(t, body, "story_app_id: kitsoki-dev", "trace_app is preserved in runtime metadata when no live app def is available")
}

func TestIssueCreate_BundlesStoppedVisualRecording(t *testing.T) {
	ctx := context.Background()
	srv, filer, dir := newIssueServer(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{
			PNG:          synthPNG(t),
			SemanticJSON: []byte(`{"ok":true,"actions":[{"handle":"testid:intent-btn-go","bbox":{"x":1,"y":1,"width":2,"height":2}}]}`),
			RRWebJSON:    []byte(`{"schemaVersion":1,"source":"kitsoki-visual-record","events":[{"type":4}]}`),
		}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.record", map[string]any{
		"action":        "start",
		"visual_handle": visual,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.record start: %s", contentText(res))
	var started studio.VisualRecordOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &started))
	_ = snapshotInfo(ctx, t, cs, visual, "full")
	res, err = callTool(ctx, cs, "visual.record", map[string]any{
		"action":       "stop",
		"recording_id": started.RecordingID,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.record stop: %s", contentText(res))

	res, err = callTool(ctx, cs, "issue.create", map[string]any{
		"title":                     "Visual regression evidence",
		"body":                      "The visual loop failed.",
		"include_visual_recordings": []string{started.RecordingID},
	})
	require.NoError(t, err)
	out := issueResult(t, res)
	require.Len(t, out.Assets, 3)
	for _, p := range out.Assets {
		assert.FileExists(t, p)
		assert.Contains(t, p, dir)
	}
	assert.Contains(t, filer.got.Body, "Visual recording `"+started.RecordingID+"`")
	assert.Contains(t, filer.got.Body, "timeline.json")
	assert.Contains(t, filer.got.Body, "capture.semantic.json")
	assert.Contains(t, filer.got.Body, "session.rrweb.json")
}

// TestIssueCreate_AddsAutonomousLabelByDefault proves source-autonomous is
// applied even when the caller passes no labels at all.
func TestIssueCreate_AddsAutonomousLabelByDefault(t *testing.T) {
	ctx := context.Background()
	srv, filer, _ := newIssueServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{
		"title": "a gap with no caller labels",
		"body":  "body",
	})
	require.NoError(t, err)
	out := issueResult(t, res)

	assert.Equal(t, []string{"source-autonomous"}, out.Labels)
	assert.Equal(t, []string{"source-autonomous"}, filer.got.Labels)
	assert.Empty(t, out.Assets, "no assets requested → none written")
}

func TestIssueCreate_LocalArtifactSinkWritesTicket(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv := studio.NewServer(studio.NewStudioSession(replayBuilder()),
		studio.WithArtifactsDir(dir),
	)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{
		"title":  "Local artifact capture",
		"body":   "Captured locally without filing a GitHub issue.",
		"labels": []string{"mcp"},
	})
	require.NoError(t, err)
	out := issueResult(t, res)

	assert.True(t, out.OK)
	assert.Empty(t, out.URL)
	assert.Zero(t, out.Number)
	require.NotEmpty(t, out.LocalPath)
	assert.Contains(t, out.LocalPath, filepath.Join(dir, "issues", "bugs"))
	assert.FileExists(t, out.LocalPath)
	assert.Equal(t, []string{"source-autonomous", "mcp"}, out.Labels)

	body, rerr := os.ReadFile(out.LocalPath)
	require.NoError(t, rerr)
	assert.Contains(t, string(body), "Local artifact capture")
	assert.Contains(t, string(body), "Captured locally without filing a GitHub issue.")
	assert.Contains(t, string(body), "studio-mcp")
	assert.Contains(t, string(body), "labels:\n  - \"source-autonomous\"\n  - \"mcp\"")
}

// TestIssueCreate_NoFilerUnavailable proves a studio started without a filer
// rejects sink=github with the structured ErrIssueUnavailable rather than
// panicking or silently no-op'ing.
func TestIssueCreate_NoFilerUnavailable(t *testing.T) {
	ctx := context.Background()
	sess := studio.NewStudioSession(replayBuilder())
	srv := studio.NewServer(sess) // no WithIssueFiler
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{"title": "x", "sink": "github"})
	require.NoError(t, err)
	require.True(t, res.IsError)
	var te studio.ToolError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &te))
	assert.Equal(t, studio.ErrIssueUnavailable, te.Code)
}

// TestIssueCreate_TitleRequired rejects an empty title before any filing.
func TestIssueCreate_TitleRequired(t *testing.T) {
	ctx := context.Background()
	srv, filer, _ := newIssueServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{"title": "  "})
	require.NoError(t, err)
	require.True(t, res.IsError)
	var te studio.ToolError
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &te))
	assert.Equal(t, studio.ErrBadRequest, te.Code)
	assert.Empty(t, filer.got.Title, "filer never called on a bad request")
}

// TestIssueCreate_FallbackToArtifactsOnFilingError proves the finding is never
// lost: when the wired filer fails (the dogfood loop's worst case — a label/auth
// wall on GitHub), issue.create writes the composed issue to the artifacts dir
// and returns ok:false with LocalPath + FilingError set, rather than a hard
// tool error that would hide the recoverable markdown path.
func TestIssueCreate_FallbackToArtifactsOnFilingError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sess := studio.NewStudioSession(replayBuilder())
	failing := func(_ context.Context, _ studio.IssueRequest) (studio.IssueResult, error) {
		return studio.IssueResult{}, errors.New("github issue create: HTTP 403: Resource not accessible by integration")
	}
	srv := studio.NewServer(sess,
		studio.WithIssueFiler(failing),
		studio.WithIssueSink(studio.IssueSinkGitHub),
		studio.WithArtifactsDir(dir),
	)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{
		"title": "MCP gap: no standalone gate-runner",
		"body":  "The studio cannot run go test against a worktree outside a session.",
	})
	require.NoError(t, err)
	out := issueResult(t, res) // must NOT be a tool error — fallback keeps the markdown recoverable
	assert.False(t, out.OK, "remote filing did not happen")
	assert.Empty(t, out.URL, "no GitHub URL when filing failed")
	assert.Contains(t, out.FilingError, "403", "surfaces why GitHub filing failed")
	require.NotEmpty(t, out.LocalPath, "fallback file path must be returned")

	body, rerr := os.ReadFile(out.LocalPath)
	require.NoError(t, rerr, "fallback file must exist on disk")
	assert.Contains(t, string(body), "MCP gap: no standalone gate-runner", "title preserved")
	assert.Contains(t, string(body), "go test against a worktree", "body preserved")
	assert.Contains(t, string(body), "Unfiled", "marked for human recovery")
}

func TestIssueCreatePassesWorkspaceRootForRepoInference(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	var got studio.IssueRequest
	filer := func(_ context.Context, req studio.IssueRequest) (studio.IssueResult, error) {
		got = req
		return studio.IssueResult{URL: "https://github.com/o/r/issues/7", Number: 7}, nil
	}
	sess := studio.NewStudioSession(replayBuilder())
	_, err := sess.OpenWorkspace(studio.OpenWorkspaceParams{Dir: root})
	require.NoError(t, err)
	srv := studio.NewServer(sess,
		studio.WithIssueFiler(filer),
		studio.WithIssueSink(studio.IssueSinkGitHub),
		studio.WithArtifactsDir(t.TempDir()),
	)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{
		"title": "infer repo from workspace",
		"body":  "repo omitted on purpose",
	})
	require.NoError(t, err)
	out := issueResult(t, res)
	assert.True(t, out.OK)
	assert.Equal(t, root, got.Root, "issue.create should give the filer a workspace root for repo inference")
	assert.Empty(t, got.Repo)
}

func TestIssueCreate_PrivacyFailureBlocksFiler(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	t.Chdir(root)
	filer := &recordingFiler{}
	checker := bugprivacy.CheckFunc(func(context.Context, bugprivacy.Report, []bugprivacy.Finding) (bugprivacy.Decision, error) {
		return bugprivacy.Decision{Pass: false, Categories: []string{"confidential_business_data"}, Reason: "contains confidential business data"}, nil
	})
	srv := studio.NewServer(studio.NewStudioSession(replayBuilder()),
		studio.WithIssueFiler(filer.file),
		studio.WithIssueSink(studio.IssueSinkGitHub),
		studio.WithArtifactsDir(filepath.Join(root, ".artifacts")),
		studio.WithBugPrivacyChecker(checker),
	)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{
		"title": "private business report",
		"body":  "do not file this",
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "privacy failure should be a tool error")
	assert.Empty(t, filer.got.Title, "filer must not be called")

	followUpDir := filepath.Join(root, ".artifacts", "privacy-followups", "issues", "bugs")
	entries, rerr := os.ReadDir(followUpDir)
	require.NoError(t, rerr)
	require.Len(t, entries, 1)
	body, rerr := os.ReadFile(filepath.Join(followUpDir, entries[0].Name()))
	require.NoError(t, rerr)
	assert.NotContains(t, string(body), "private business report")
	assert.Contains(t, string(body), "confidential_business_data")
}

func writeIssueTrace(t *testing.T, path string, world map[string]any) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	f, err := os.Create(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, f.Close()) }()
	_, err = f.WriteString(`{"kind":"session.header","schema_version":1}` + "\n")
	require.NoError(t, err)
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	events := []store.Event{
		{
			Turn:      app.TurnNumber(1),
			Ts:        now,
			Kind:      store.TurnStarted,
			StatePath: app.StatePath("idle"),
			Payload:   issueTracePayload(t, map[string]any{"input": "start"}),
		},
		{
			Turn:      app.TurnNumber(1),
			Ts:        now.Add(time.Second),
			Kind:      store.EffectApplied,
			StatePath: app.StatePath("idle"),
			Payload:   issueTracePayload(t, map[string]any{"set": world}),
		},
		{
			Turn:      app.TurnNumber(1),
			Ts:        now.Add(2 * time.Second),
			Kind:      store.AgentCalled,
			StatePath: app.StatePath("idle"),
			CallID:    "call-1",
			Payload: issueTracePayload(t, map[string]any{
				"verb":   "debug",
				"prompt": "diagnose " + filepath.Join(os.Getenv("HOME"), "secret.txt") + " for alice@example.com",
			}),
		},
		{
			Turn:      app.TurnNumber(1),
			Ts:        now.Add(3 * time.Second),
			Kind:      store.TransitionApplied,
			StatePath: app.StatePath("needs_human"),
			Payload:   issueTracePayload(t, map[string]any{"from": "idle", "to": "needs_human"}),
		},
	}
	for _, ev := range events {
		line, mErr := store.MarshalEventLine(ev)
		require.NoError(t, mErr)
		_, err = f.Write(append(line, '\n'))
		require.NoError(t, err)
	}
	return path
}

func issueTracePayload(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
