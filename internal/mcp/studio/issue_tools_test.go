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
//   - no filer wired → structured ErrIssueUnavailable

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	studio "kitsoki/internal/mcp/studio"
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
	for _, p := range out.Assets {
		assert.Contains(t, body, p, "each asset referenced by its path")
	}
	assert.Equal(t, out.Labels, filer.got.Labels, "filer got the final labels")
	assert.Equal(t, "[MCP gap] session.drive cannot do X", filer.got.Title)

	// Token-diet: bulky machine context (full world + full pretty trace) is spilled
	// to sidecar files under the issue's artifacts dir, linked from the body, not
	// inlined wholesale.
	slugDir := filepath.Join(dir, "mcp-gap-session-drive-cannot-do-x")
	assert.FileExists(t, filepath.Join(slugDir, "trace.json"), "full trace sidecar written")
	assert.FileExists(t, filepath.Join(slugDir, "world.json"), "full world sidecar written")
	assert.Contains(t, body, "trace.json", "body links the trace sidecar")
	assert.Contains(t, body, "world.json", "body links the world sidecar")
	// The inlined trace stays compact (one JSON object per line, not indented).
	assert.NotContains(t, body, "\n    \"", "inlined trace is compact, not pretty-printed")
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

// TestIssueCreate_NoFilerUnavailable proves a studio started without a filer
// rejects issue.create with the structured ErrIssueUnavailable rather than
// panicking or silently no-op'ing.
func TestIssueCreate_NoFilerUnavailable(t *testing.T) {
	ctx := context.Background()
	sess := studio.NewStudioSession(replayBuilder())
	srv := studio.NewServer(sess) // no WithIssueFiler
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{"title": "x"})
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
// and returns OK with LocalPath + FilingError set, rather than a hard error.
func TestIssueCreate_FallbackToArtifactsOnFilingError(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sess := studio.NewStudioSession(replayBuilder())
	failing := func(_ context.Context, _ studio.IssueRequest) (studio.IssueResult, error) {
		return studio.IssueResult{}, errors.New("gh issue create: HTTP 403: Resource not accessible by integration")
	}
	srv := studio.NewServer(sess,
		studio.WithIssueFiler(failing),
		studio.WithArtifactsDir(dir),
	)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "issue.create", map[string]any{
		"title": "MCP gap: no standalone gate-runner",
		"body":  "The studio cannot run go test against a worktree outside a session.",
	})
	require.NoError(t, err)
	out := issueResult(t, res) // must NOT be a tool error — fallback keeps it OK
	assert.True(t, out.OK)
	assert.Empty(t, out.URL, "no GitHub URL when filing failed")
	assert.Contains(t, out.FilingError, "403", "surfaces why GitHub filing failed")
	require.NotEmpty(t, out.LocalPath, "fallback file path must be returned")

	body, rerr := os.ReadFile(out.LocalPath)
	require.NoError(t, rerr, "fallback file must exist on disk")
	assert.Contains(t, string(body), "MCP gap: no standalone gate-runner", "title preserved")
	assert.Contains(t, string(body), "go test against a worktree", "body preserved")
	assert.Contains(t, string(body), "Unfiled", "marked for human recovery")
}
