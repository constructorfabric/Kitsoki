package studio_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"kitsoki/internal/capsuletest"
	studio "kitsoki/internal/mcp/studio"
)

func TestVisualToolsListed(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := cs.ListTools(ctx, nil)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	assert.True(t, names["visual.open"])
	assert.True(t, names["visual.observe"])
	assert.True(t, names["visual.snapshot"])
	assert.True(t, names["visual.act"])
	assert.True(t, names["visual.diff"])
	assert.True(t, names["visual.git_diff"])
	assert.True(t, names["visual.tui_git_diff"])
	assert.True(t, names["visual.record"])
}

func TestVisualObserve_CompactStructuredState(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.observe", map[string]any{
		"visual_handle": visual,
		"cols":          80,
		"rows":          24,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.observe: %s", contentText(res))

	var got studio.VisualObserveOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	assert.Equal(t, visual, got.VisualHandle)
	assert.Equal(t, "web", got.Kind)
	assert.Equal(t, handle, got.Handle)
	assert.Equal(t, "foyer", got.State)
	assert.NotEmpty(t, got.Summary)
	require.NotNil(t, got.Frame)
	assert.Equal(t, 80, got.Frame.Width)
	assert.NotEmpty(t, got.Metadata.Route)
	assert.NotEmpty(t, got.Metadata.Title)
	assert.Equal(t, 1280, got.Metadata.Viewport.Width)
	assert.Equal(t, []string{"frame"}, got.Metadata.DirtyRegions)
	assert.NotContains(t, contentText(res), "\x1b[", "observe stays text/json only, not ANSI")
	assert.NotEmpty(t, got.Actions, "allowed intents become deterministic action handles")
	assert.NotEmpty(t, got.Regions)
	assert.Equal(t, "visual.act", got.Next.Preferred)
	assert.False(t, got.ImageAvailable, "web snapshots report unavailable when no webshot seam is wired")
}

func TestVisualObserve_WebUsesCompactSemanticSidecar(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{
			PNG: synthPNG(t),
			SemanticJSON: []byte(`{
				"ok": true,
				"route": "#/s/h1",
				"title": "Kitsoki Web",
				"viewport": {"width": 1440, "height": 900},
				"focused": "[data-testid=\"composer-input\"]",
				"dirty_regions": ["chat", "composer"],
				"regions": [{"id":"chat","label":"Chat","selector":"[data-testid=\"chat-section\"]","bbox":{"x":10,"y":20,"width":300,"height":400}}],
				"actions": [{"handle":"testid:intent-btn-go","label":"Go","disabled":false,"bbox":{"x":11,"y":22,"width":33,"height":44}}],
				"nodes": [{"html":"should not leak"}]
			}`),
		}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.observe", map[string]any{
		"visual_handle":    visual,
		"include_semantic": true,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.observe semantic: %s", contentText(res))
	require.NotContains(t, contentText(res), "image/png")

	var got studio.VisualObserveOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.Equal(t, "#/s/h1", got.Metadata.Route)
	assert.Equal(t, "Kitsoki Web", got.Metadata.Title)
	assert.Equal(t, studio.VisualViewport{Width: 1440, Height: 900}, got.Metadata.Viewport)
	assert.Equal(t, []string{"chat", "composer"}, got.Metadata.DirtyRegions)
	require.Len(t, got.Actions, 1)
	assert.Equal(t, "testid:intent-btn-go", got.Actions[0].Handle)
	assert.Equal(t, "go", got.Actions[0].Intent)
	assert.Equal(t, &studio.VisualBBox{X: 11, Y: 22, Width: 33, Height: 44}, got.Actions[0].BBox)
	require.Len(t, got.Regions, 1)
	assert.Equal(t, "chat", got.Regions[0].ID)
	assert.Equal(t, &studio.VisualBBox{X: 10, Y: 20, Width: 300, Height: 400}, got.Regions[0].BBox)
	assert.NotContains(t, contentText(res), "nodes", "observe must not leak raw DOM sidecars")
}

func TestVisualObserve_WebCaptureErrorDisablesImageAvailability(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{}, errors.New("webshot: capture: exit status 1")
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "vscode")

	res, err := callTool(ctx, cs, "visual.observe", map[string]any{
		"visual_handle":    visual,
		"include_semantic": true,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.observe semantic: %s", contentText(res))

	var got studio.VisualObserveOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.False(t, got.ImageAvailable, "failed web/vscode capture must not advertise an available image")
	require.NotEmpty(t, got.Errors)
	assert.Contains(t, got.Errors[0], "webshot: capture: exit status 1")
	assert.Contains(t, got.Next.Reasons, "web image snapshot capture failed; see errors")
	assert.Contains(t, got.Next.Reasons, "web image snapshots need a browser-capable webShot seam")
}

func TestVisualObserve_TUIIncludesTerminalWrapper(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "tui")

	res, err := callTool(ctx, cs, "visual.observe", map[string]any{
		"visual_handle": visual,
		"cols":          80,
		"rows":          24,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.observe tui: %s", contentText(res))

	var got studio.VisualObserveOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	require.NotNil(t, got.Frame)
	require.NotNil(t, got.Frame.Terminal)
	assert.Equal(t, "tui", got.Kind)
	assert.NotEmpty(t, got.Frame.Terminal.Rows, "terminal rows are the compact cell grid")
	assert.NotEmpty(t, got.Frame.Terminal.DirtyRows)
	assert.Equal(t, "prompt", got.Frame.Terminal.Focus)
	assert.Contains(t, got.Frame.Terminal.SlashCommands, "/help")
	require.NotEmpty(t, got.Frame.Terminal.Actions)
	assert.Equal(t, "intent:"+got.Frame.Terminal.Actions[0].Intent, got.Frame.Terminal.Actions[0].Handle)
	assert.GreaterOrEqual(t, got.Frame.Terminal.Actions[0].BBox.Width, 1)
	require.NotEmpty(t, got.Actions)
	require.NotNil(t, got.Actions[0].BBox, "top-level deterministic actions carry terminal cell bboxes")
	assert.GreaterOrEqual(t, got.Actions[0].BBox.Width, 1)
}

func TestVisualSnapshot_WebUsesStubWebShot(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	stubPNG := synthPNG(t)
	var gotSpec studio.WebRenderSpec
	srv.SetWebShot(func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		gotSpec = spec
		return stubPNG, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.snapshot", map[string]any{
		"visual_handle": visual,
		"region":        "chat",
		"overlay":       "action_ids",
		"scale":         "medium",
		"query":         map[string]string{"chat": "chat-123"},
		"assert_text":   []string{"Foyer"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.snapshot: %s", contentText(res))

	img := imageContent(t, res)
	assert.Equal(t, stubPNG, img.Data)
	assert.NotEmpty(t, gotSpec.SessionID, "visual snapshot maps the session handle to a live web session")
	assert.Equal(t, map[string]string{"chat": "chat-123"}, gotSpec.Query)
	assert.Equal(t, []string{"Foyer"}, gotSpec.AssertText)

	var info studio.VisualSnapshotInfo
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &info))
	assert.True(t, info.OK)
	assert.NotEmpty(t, info.ImageID)
	assert.NotEmpty(t, info.SHA256)
	assert.Equal(t, visual, info.VisualHandle)
	// Echoed request inputs (region/overlay) are no longer returned — token diet.
	assert.Equal(t, len(stubPNG), info.Bytes)
}

func TestVisualSnapshot_IncludesCompactSemanticObservation(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	stubPNG := synthPNG(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{
			PNG: stubPNG,
			SemanticJSON: []byte(`{
				"ok": true,
				"route": "#/s/h1",
				"title": "Kitsoki",
				"dirty_regions": ["chat"],
				"actions": [{"handle":"testid:intent-btn-go","label":"Go"}],
				"nodes": [{"html":"should not leak"}]
			}`),
		}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.snapshot", map[string]any{
		"visual_handle": visual,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.snapshot: %s", contentText(res))

	var info studio.VisualSnapshotInfo
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &info))
	require.NotNil(t, info.Semantic)
	assert.Equal(t, true, info.Semantic["ok"])
	assert.Equal(t, "#/s/h1", info.Semantic["route"])
	assert.Contains(t, info.Semantic, "actions")
	assert.NotContains(t, info.Semantic, "nodes", "snapshot metadata must keep the browser sidecar compact")
}

func TestVisualSnapshot_CropsWebPNGToSemanticRegion(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{
			PNG: pngOf(t, 4, 4, map[image.Point]color.RGBA{
				{X: 1, Y: 1}: {R: 200, A: 255},
			}),
			SemanticJSON: []byte(`{
				"ok": true,
				"regions": [{"id":"chat","bbox":{"x":1,"y":1,"width":2,"height":2}}],
				"actions": [{"handle":"testid:intent-btn-go","bbox":{"x":1,"y":1,"width":2,"height":2}}]
			}`),
		}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.snapshot", map[string]any{
		"visual_handle": visual,
		"region":        "chat",
		"overlay":       "action_ids",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.snapshot crop: %s", contentText(res))

	img := imageContent(t, res)
	decoded, err := png.Decode(bytes.NewReader(img.Data))
	require.NoError(t, err)
	assert.Equal(t, 2, decoded.Bounds().Dx())
	assert.Equal(t, 2, decoded.Bounds().Dy())
	red := color.RGBAModel.Convert(decoded.At(0, 0)).(color.RGBA)
	assert.Equal(t, color.RGBA{R: 255, G: 64, B: 64, A: 255}, red, "action overlay is translated into cropped image coordinates")
	var info studio.VisualSnapshotInfo
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &info))
	assert.Equal(t, len(img.Data), info.Bytes)
	assert.Equal(t, 2, info.Width)
	assert.Equal(t, 2, info.Height)
	assert.Equal(t, studio.VisualViewport{Width: 4, Height: 4}, info.Original)
	assert.Equal(t, &studio.VisualBBox{X: 1, Y: 1, Width: 2, Height: 2}, info.CropBBox)
	assert.Equal(t, []string{"testid:intent-btn-go"}, info.Visible)
}

func TestVisualSnapshot_DownscalesToMaxPixelsAndReportsBudget(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{
			PNG: pngOf(t, 100, 100, map[image.Point]color.RGBA{
				{X: 50, Y: 50}: {R: 255, A: 255},
			}),
			SemanticJSON: []byte(`{
				"ok": true,
				"actions": [{"handle":"testid:intent-btn-go","label":"Go","bbox":{"x":20,"y":20,"width":20,"height":20}}]
			}`),
		}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.snapshot", map[string]any{
		"visual_handle": visual,
		"max_pixels":    2500,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.snapshot budget: %s", contentText(res))

	img := imageContent(t, res)
	decoded, err := png.Decode(bytes.NewReader(img.Data))
	require.NoError(t, err)
	assert.LessOrEqual(t, decoded.Bounds().Dx()*decoded.Bounds().Dy(), 2500)
	var info studio.VisualSnapshotInfo
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &info))
	assert.Equal(t, studio.VisualViewport{Width: 100, Height: 100}, info.Original)
	assert.Equal(t, decoded.Bounds().Dx(), info.Width)
	assert.Equal(t, decoded.Bounds().Dy(), info.Height)
	assert.Equal(t, 2500, info.MaxPixels)
	assert.Less(t, info.ScaleFactor, 1.0)
	assert.Equal(t, []string{"testid:intent-btn-go"}, info.Visible)
}

func TestVisualAct_PixelClickUsesRetainedImageAction(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{
			PNG: pngOf(t, 10, 10, nil),
			SemanticJSON: []byte(`{
				"ok": true,
				"actions": [{"handle":"testid:intent-btn-go","label":"Go","bbox":{"x":1,"y":1,"width":4,"height":4}}]
			}`),
		}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")
	snap := snapshotInfo(ctx, t, cs, visual, "full")

	res, err := callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action":        "pixel_click",
		"image_id":      snap.ImageID,
		"point":         map[string]any{"x": 2, "y": 2},
		"slots":         map[string]any{"direction": "west"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act pixel_click: %s", contentText(res))
	var got studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	require.NotNil(t, got.Outcome)
	assert.Equal(t, "cloakroom", got.Outcome.State)
	assert.Equal(t, "click", got.Action)
}

func TestVisualDiff_ComparesRetainedSnapshots(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	firstPNG := pngOf(t, 4, 4, nil)
	secondPNG := pngOf(t, 4, 4, map[image.Point]color.RGBA{{X: 2, Y: 1}: {R: 255, A: 255}})
	var calls int
	srv.SetWebShot(func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		calls++
		if calls == 1 {
			return firstPNG, nil
		}
		return secondPNG, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	first := snapshotInfo(ctx, t, cs, visual, "chat")
	second := snapshotInfo(ctx, t, cs, visual, "chat")
	require.NotEqual(t, first.ImageID, second.ImageID)

	res, err := callTool(ctx, cs, "visual.diff", map[string]any{
		"from_image_id": first.ImageID,
		"to_image_id":   second.ImageID,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.diff: %s", contentText(res))
	var diff studio.VisualDiffOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &diff))
	assert.True(t, diff.OK)
	assert.True(t, diff.Changed)
	assert.False(t, diff.Same)
	assert.Contains(t, diff.Reasons, "sha256 changed")
	assert.Contains(t, diff.Reasons, "pixels changed")
	require.NotNil(t, diff.ChangedBBox)
	assert.Equal(t, &studio.VisualBBox{X: 2, Y: 1, Width: 1, Height: 1}, diff.ChangedBBox)
	assert.Equal(t, []string{"bbox:2,1,1,1"}, diff.ChangedRegions)
}

func TestVisualGitDiff_CapturesTwoRevisionsAndDiffsScreenshots(t *testing.T) {
	ctx := context.Background()
	repo := capsuletest.Open(t, "visual-story-two-revisions")
	storyRel := filepath.Join("stories", "demo", "app.yaml")
	from := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD~1"))
	to := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))

	srv, _ := newReplayServer(t)
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		data, err := os.ReadFile(spec.StoryPath)
		require.NoError(t, err)
		if strings.Contains(string(data), "scene: one") {
			return studio.WebShotResult{PNG: pngOf(t, 2, 2, map[image.Point]color.RGBA{
				{X: 0, Y: 0}: {R: 1, G: 2, B: 3, A: 255},
			})}, nil
		}
		return studio.WebShotResult{PNG: pngOf(t, 2, 2, map[image.Point]color.RGBA{
			{X: 1, Y: 1}: {R: 9, G: 8, B: 7, A: 255},
		})}, nil
	})
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "visual.git_diff", map[string]any{
		"dir":            repo,
		"from":           from,
		"to":             to,
		"story_path":     storyRel,
		"state":          "idle",
		"include_images": "both",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.git_diff: %s", contentText(res))
	require.Len(t, res.Content, 3, "text plus two requested image blocks")
	var got studio.VisualGitDiffOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	assert.Equal(t, filepath.ToSlash(storyRel), got.StoryPath)
	assert.Equal(t, from, got.From)
	assert.Equal(t, to, got.To)
	assert.NotEmpty(t, got.FromImageID)
	assert.NotEmpty(t, got.ToImageID)
	assert.True(t, got.Changed)
	assert.False(t, got.Same)
	assert.NotNil(t, got.ChangedBBox)
	assert.Contains(t, got.Reasons, "pixels changed")

	res, err = callTool(ctx, cs, "visual.diff", map[string]any{
		"from_image_id": got.FromImageID,
		"to_image_id":   got.ToImageID,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.diff after git diff: %s", contentText(res))
	var diff studio.VisualDiffOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &diff))
	assert.True(t, diff.Changed)
	assert.Equal(t, got.ChangedBBox, diff.ChangedBBox)
}

// TestVisualTUIGitDiff_CapturesTwoRevisionsAndDiffsScreenshots mirrors
// TestVisualGitDiff_CapturesTwoRevisionsAndDiffsScreenshots for the TUI path:
// two git revisions of a real story (a one-line prose edit at the same
// state), rasterised via the in-process ANSI renderer — no webShot seam
// wired at all, proving visual.tui_git_diff never needs one.
func TestVisualTUIGitDiff_CapturesTwoRevisionsAndDiffsScreenshots(t *testing.T) {
	ctx := context.Background()
	repo := t.TempDir()
	storyDir := filepath.Join(repo, "story")
	// The cloak view extends views/base.pongo (resolved relative to the app's
	// own directory), so the whole app dir — not just app.yaml — must be
	// archived at each revision for the spec render to find its templates.
	require.NoError(t, copyTree(filepath.Dir(cloakApp), storyDir))
	storyRel := filepath.Join("story", "app.yaml")
	storyPath := filepath.Join(repo, storyRel)

	base, err := os.ReadFile(storyPath)
	require.NoError(t, err)
	before := string(base)
	require.Contains(t, before, "only one remains")
	after := strings.Replace(before, "only one remains", "only one hook remains, freshly painted", 1)
	require.NotEqual(t, before, after, "the fixture edit must actually change the rendered prose")

	require.NoError(t, os.WriteFile(storyPath, []byte(before), 0o644))
	gitRun(t, repo, "init")
	gitRun(t, repo, "config", "user.email", "test@example.com")
	gitRun(t, repo, "config", "user.name", "Test User")
	gitRun(t, repo, "add", ".")
	gitRun(t, repo, "commit", "-m", "before")
	from := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))
	require.NoError(t, os.WriteFile(storyPath, []byte(after), 0o644))
	gitRun(t, repo, "commit", "-am", "after")
	to := strings.TrimSpace(gitRun(t, repo, "rev-parse", "HEAD"))

	// No SetWebShot/SetWebShotResult wired anywhere — the point of the tool.
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)

	res, err := callTool(ctx, cs, "visual.tui_git_diff", map[string]any{
		"dir":        repo,
		"from":       from,
		"to":         to,
		"story_path": storyRel,
		"state":      "cloakroom",
		"cols":       80, "rows": 24,
		"include_images": "both",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.tui_git_diff: %s", contentText(res))
	require.Len(t, res.Content, 3, "text plus two requested image blocks")

	var got studio.VisualTUIGitDiffOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	assert.Equal(t, filepath.ToSlash(storyRel), got.StoryPath)
	assert.Equal(t, from, got.From)
	assert.Equal(t, to, got.To)
	assert.Equal(t, "cloakroom", got.State)
	assert.NotEmpty(t, got.FromImageID)
	assert.NotEmpty(t, got.ToImageID)
	assert.True(t, got.Changed, "a prose edit must move visible pixels: %+v", got)
	assert.False(t, got.Same)
	assert.NotNil(t, got.ChangedBBox)
	assert.Contains(t, got.Reasons, "pixels changed")

	for _, c := range res.Content {
		if ic, ok := c.(*mcpsdk.ImageContent); ok {
			_, decErr := png.Decode(bytes.NewReader(ic.Data))
			require.NoError(t, decErr, "visual.tui_git_diff image block must be a decodable PNG")
		}
	}

	// The retained image ids stay usable with visual.diff, same as the web path.
	res, err = callTool(ctx, cs, "visual.diff", map[string]any{
		"from_image_id": got.FromImageID,
		"to_image_id":   got.ToImageID,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.diff after tui git diff: %s", contentText(res))
	var diff studio.VisualDiffOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &diff))
	assert.True(t, diff.Changed)
	assert.Equal(t, got.ChangedBBox, diff.ChangedBBox)
}

func TestVisualRecord_WritesSemanticSidecars(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv, _ := newReplayServer(t)
	srv = studio.NewServer(srv.Session(), studio.WithArtifactsDir(dir))
	srv.SetWebShot(func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		return synthPNG(t), nil
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
	var start studio.VisualRecordOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &start))
	require.Equal(t, "recording", start.Status)

	_, err = callTool(ctx, cs, "visual.observe", map[string]any{"visual_handle": visual})
	require.NoError(t, err)
	_ = snapshotInfo(ctx, t, cs, visual, "full")
	_, err = callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action_handle": "intent:go",
		"slots":         map[string]any{"direction": "west"},
	})
	require.NoError(t, err)

	res, err = callTool(ctx, cs, "visual.record", map[string]any{
		"action":       "stop",
		"recording_id": start.RecordingID,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.record stop: %s", contentText(res))
	var stop studio.VisualRecordOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &stop))
	assert.Equal(t, "stopped", stop.Status)
	assert.GreaterOrEqual(t, stop.Events, 4)
	require.Len(t, stop.Artifacts, 2)
	for _, path := range stop.Artifacts {
		assert.FileExists(t, path)
		assert.Contains(t, path, dir)
	}
}

func TestVisualRecord_WritesRRWebSidecarFromWebSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	srv, _ := newReplayServer(t)
	srv = studio.NewServer(srv.Session(), studio.WithArtifactsDir(dir))
	srv.SetWebShotResult(func(ctx context.Context, spec studio.WebRenderSpec) (studio.WebShotResult, error) {
		return studio.WebShotResult{
			PNG:       synthPNG(t),
			RRWebJSON: []byte(`{"schemaVersion":1,"source":"kitsoki-visual-record","events":[{"type":4},{"type":2}]}`),
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
	var start studio.VisualRecordOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &start))

	_ = snapshotInfo(ctx, t, cs, visual, "full")

	res, err = callTool(ctx, cs, "visual.record", map[string]any{
		"action":       "stop",
		"recording_id": start.RecordingID,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.record stop: %s", contentText(res))
	var stop studio.VisualRecordOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &stop))
	require.Len(t, stop.Artifacts, 3)
	var rrwebPath string
	for _, path := range stop.Artifacts {
		if strings.HasSuffix(path, "session.rrweb.json") {
			rrwebPath = path
		}
	}
	require.NotEmpty(t, rrwebPath)
	data, err := os.ReadFile(rrwebPath)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schemaVersion":1,"source":"kitsoki-visual-record","events":[{"type":4},{"type":2}]}`, string(data))
}

func TestVisualSnapshot_TUIRasterisesFrame(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "tui")

	res, err := callTool(ctx, cs, "visual.snapshot", map[string]any{
		"visual_handle": visual,
		"region":        "tui",
		"cols":          80,
		"rows":          24,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.snapshot tui: %s", contentText(res))

	img := imageContent(t, res)
	require.NotEmpty(t, img.Data)
	var info studio.VisualSnapshotInfo
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &info))
	assert.True(t, info.OK)
	assert.Equal(t, "tui", info.Kind)
}

func TestVisualAct_SubmitAdvancesDeterministically(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action_handle": "intent:go",
		"slots":         map[string]any{"direction": "west"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act: %s", contentText(res))

	var got studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	require.NotNil(t, got.Outcome)
	assert.Equal(t, "cloakroom", got.Outcome.State)
	assert.Equal(t, "cloakroom", got.Frame.Metadata.State)
	assert.False(t, got.NeedsSnapshot, "deterministic submit does not require a screenshot by default")

	after := inspectSnapshot(ctx, t, cs, handle)
	assert.Equal(t, "cloakroom", after.State)
}

func TestVisualAct_WebSemanticClickUsesBrowserActionWithModifiers(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	var gotSpec studio.WebRenderSpec
	var gotAction studio.WebActionSpec
	srv.SetWebAct(func(ctx context.Context, spec studio.WebRenderSpec, action studio.WebActionSpec) (studio.WebShotResult, error) {
		gotSpec = spec
		gotAction = action
		return studio.WebShotResult{
			PNG:          synthPNG(t),
			SemanticJSON: []byte(`{"ok":true,"dirty_regions":["modal"],"focused":"[data-testid=\"bug-modal\"]"}`),
		}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action":        "click",
		"action_handle": "testid:edit-story-btn",
		"modifiers":     []any{"Alt"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act web semantic click: %s", contentText(res))

	var got studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	assert.Equal(t, []string{"modal"}, got.ChangedRegions)
	assert.False(t, got.NeedsSnapshot)
	assert.NotEmpty(t, got.ImageID)
	assert.Equal(t, `[data-testid="bug-modal"]`, got.Semantic["focused"])
	assert.NotEmpty(t, gotSpec.SessionID)
	assert.Equal(t, "click", gotAction.Kind)
	assert.Equal(t, "testid:edit-story-btn", gotAction.ActionHandle)
	assert.Equal(t, []string{"Alt"}, gotAction.Modifiers)
}

func TestVisualAct_WebContextMenuUsesRightButton(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	var gotAction studio.WebActionSpec
	srv.SetWebAct(func(ctx context.Context, spec studio.WebRenderSpec, action studio.WebActionSpec) (studio.WebShotResult, error) {
		gotAction = action
		return studio.WebShotResult{PNG: synthPNG(t), SemanticJSON: []byte(`{"ok":true}`)}, nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action":        "contextmenu",
		"point":         map[string]any{"x": 24, "y": 48},
		"modifiers":     []any{"option"},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act web contextmenu: %s", contentText(res))

	assert.Equal(t, "contextmenu", gotAction.Kind)
	assert.Equal(t, "right", gotAction.Button)
	require.NotNil(t, gotAction.Point)
	assert.Equal(t, 24, gotAction.Point.X)
	assert.Equal(t, 48, gotAction.Point.Y)
	assert.Equal(t, []string{"Alt"}, gotAction.Modifiers)

	var got studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.False(t, got.NeedsSnapshot)
	assert.NotEmpty(t, got.ImageID)
}

func TestVisualAct_TypeThenPressEnterRoutesBufferedText(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action":        "type",
		"text":          "go west",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act type: %s", contentText(res))
	var typed studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &typed))
	assert.True(t, typed.OK)
	assert.Nil(t, typed.Outcome, "typing buffers locally and must not advance the story")
	before := inspectSnapshot(ctx, t, cs, handle)
	assert.Equal(t, "foyer", before.State)

	res, err = callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action":        "press",
		"key":           "Enter",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act press enter: %s", contentText(res))
	var pressed studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &pressed))
	assert.True(t, pressed.OK)
	require.NotNil(t, pressed.Outcome)
	assert.Equal(t, "cloakroom", pressed.Outcome.State)
}

func TestVisualAct_SelectSubmitsIntentWithValueSlot(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	res, err := callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action":        "select",
		"intent":        "go",
		"slots":         map[string]any{"direction": "west"},
		"value":         "west",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act select: %s", contentText(res))
	var got studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	require.NotNil(t, got.Outcome)
	assert.Equal(t, "cloakroom", got.Outcome.State)
	assert.Equal(t, "select", got.Action)
}

func TestVisualAct_ScrollIsReadOnlyAndRequestsSnapshot(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")
	before := traceLastTurn(ctx, t, cs, handle)

	res, err := callTool(ctx, cs, "visual.act", map[string]any{
		"visual_handle": visual,
		"action":        "scroll",
		"region":        "chat",
		"delta":         400,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.act scroll: %s", contentText(res))
	var got studio.VisualActOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &got))
	assert.True(t, got.OK)
	assert.True(t, got.NeedsSnapshot)
	assert.Equal(t, []string{"chat"}, got.ChangedRegions)
	after := traceLastTurn(ctx, t, cs, handle)
	assert.Equal(t, before, after, "scroll is a viewport hint and must not advance the session")
}

func TestVisualObserveAndSnapshotAreReadOnly(t *testing.T) {
	ctx := context.Background()
	srv, _ := newReplayServer(t)
	srv.SetWebShot(func(ctx context.Context, spec studio.WebRenderSpec) ([]byte, error) {
		return synthPNG(t), nil
	})
	cs := connectInProcess(ctx, t, srv)
	handle := openCloak(ctx, t, cs)
	visual := openVisual(ctx, t, cs, handle, "web")

	before := traceLastTurn(ctx, t, cs, handle)
	for _, tool := range []string{"visual.observe", "visual.snapshot"} {
		res, err := callTool(ctx, cs, tool, map[string]any{"visual_handle": visual})
		require.NoError(t, err)
		require.False(t, res.IsError, "%s: %s", tool, contentText(res))
	}
	after := traceLastTurn(ctx, t, cs, handle)
	assert.Equal(t, before, after, "observing and screenshotting must not advance the session")
}

func openVisual(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, handle, kind string) string {
	t.Helper()
	res, err := callTool(ctx, cs, "visual.open", map[string]any{
		"kind":   kind,
		"handle": handle,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.open: %s", contentText(res))
	var ok studio.VisualOpenOK
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &ok))
	require.True(t, ok.OK)
	require.NotEmpty(t, ok.VisualHandle)
	require.Equal(t, kind, ok.Kind)
	require.Equal(t, handle, ok.Handle)
	return ok.VisualHandle
}

func snapshotInfo(ctx context.Context, t *testing.T, cs *mcpsdk.ClientSession, visual, region string) studio.VisualSnapshotInfo {
	t.Helper()
	res, err := callTool(ctx, cs, "visual.snapshot", map[string]any{
		"visual_handle": visual,
		"region":        region,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "visual.snapshot: %s", contentText(res))
	var info studio.VisualSnapshotInfo
	require.NoError(t, json.Unmarshal([]byte(contentText(res)), &info))
	require.NotEmpty(t, info.ImageID)
	return info
}

func pngOf(t *testing.T, width, height int, pixels map[image.Point]color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{A: 255})
		}
	}
	for p, c := range pixels {
		img.Set(p.X, p.Y, c)
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// copyTree recursively copies src onto dst, used to stage a whole app
// directory (app.yaml plus its views/flows/intents siblings) into a scratch
// git repo for a git_diff test — a git-archived scene needs every file its
// templates resolve relative to the app dir, not just the manifest.
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if mkErr := os.MkdirAll(filepath.Dir(target), 0o755); mkErr != nil {
			return mkErr
		}
		return os.WriteFile(target, data, info.Mode().Perm()|0o200)
	})
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	return string(out)
}
