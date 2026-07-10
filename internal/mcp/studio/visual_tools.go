package studio

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/orchestrator"
	"kitsoki/internal/tui"
	"kitsoki/internal/tui/blocks"
	"kitsoki/internal/tui/shot"
)

// VisualKind names a UI surface family. The first implementation binds web and
// TUI surfaces to an existing driving handle; vscode is accepted as the embedded
// webview shape so the MCP contract can stay stable when the extension attaches
// the same runstatus surface later.
type VisualKind string

const (
	VisualWeb    VisualKind = "web"
	VisualTUI    VisualKind = "tui"
	VisualVSCode VisualKind = "vscode"
)

// VisualHandle is a lightweight logical surface opened by visual.open. It does
// not own browser state in this slice; it records the caller's intended surface
// and delegates to the existing session/render seams.
type VisualHandle struct {
	ID       string
	Kind     VisualKind
	Handle   string
	Viewport VisualViewport
	Mode     string
	Created  time.Time
	Input    string
}

// VisualImage is retained server-side metadata for one visual.snapshot result.
// The image bytes are sent only by the snapshot call; follow-on tools use IDs.
type VisualImage struct {
	ID           string
	VisualHandle string
	Kind         VisualKind
	Handle       string
	Region       string
	Overlay      string
	Scale        string
	Bytes        int
	SHA256       string
	Created      time.Time
	PNG          []byte
	Width        int
	Height       int
	Original     VisualViewport
	CropBBox     *VisualBBox
	ScaleFactor  float64
	Actions      []VisualImageAction
}

// VisualImageAction is a semantic action retained with a snapshot. Its bbox is
// in the returned image's pixel coordinate space, after crop/downscale.
type VisualImageAction struct {
	Handle   string     `json:"handle"`
	Label    string     `json:"label,omitempty"`
	Intent   string     `json:"intent,omitempty"`
	Disabled bool       `json:"disabled,omitempty"`
	BBox     VisualBBox `json:"bbox"`
}

// VisualRecording is a semantic evidence ledger for one visual handle. It stores
// observation/action/snapshot metadata, not raw image streams.
type VisualRecording struct {
	ID           string              `json:"id"`
	VisualHandle string              `json:"visual_handle"`
	Kind         string              `json:"kind"`
	Handle       string              `json:"handle"`
	Mode         string              `json:"mode"`
	Status       string              `json:"status"`
	StartedAt    string              `json:"started_at"`
	StoppedAt    string              `json:"stopped_at,omitempty"`
	Events       []VisualRecordEvent `json:"events"`
	RRWebJSON    []byte              `json:"-"`
	RRWebEvents  int                 `json:"rrweb_events,omitempty"`
}

// VisualRecordEvent is one event in a visual recording sidecar.
type VisualRecordEvent struct {
	At      string         `json:"at"`
	Type    string         `json:"type"`
	State   string         `json:"state,omitempty"`
	Action  string         `json:"action,omitempty"`
	ImageID string         `json:"image_id,omitempty"`
	Region  string         `json:"region,omitempty"`
	Data    map[string]any `json:"data,omitempty"`
}

// VisualViewport is the requested observation/snapshot geometry.
type VisualViewport struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// VisualOpenArgs is the input to visual.open.
type VisualOpenArgs struct {
	Kind     string         `json:"kind"`
	Handle   string         `json:"handle"`
	Viewport VisualViewport `json:"viewport,omitempty"`
	Mode     string         `json:"mode,omitempty"`
}

// VisualOpenOK is the output of visual.open.
type VisualOpenOK struct {
	OK           bool           `json:"ok"`
	VisualHandle string         `json:"visual_handle"`
	Kind         string         `json:"kind"`
	Handle       string         `json:"handle"`
	Viewport     VisualViewport `json:"viewport"`
	Mode         string         `json:"mode"`
}

// VisualObserveArgs is the input to visual.observe.
type VisualObserveArgs struct {
	VisualHandle string `json:"visual_handle"`
	Cols         int    `json:"cols,omitempty"`
	Rows         int    `json:"rows,omitempty"`
	// IncludeSemantic opts into the web/vscode semantic enrichment (route/title/
	// focused/actions/regions harvested from a live render). It costs an extra
	// renderer call and inflates the payload, so it is OFF by default; the cheap
	// structured observation (state, frame preview, allowed-intent actions) is
	// always returned.
	IncludeSemantic bool `json:"include_semantic,omitempty"`
}

// VisualObserveOK is the compact, JSON-only observation intended to be the
// default per-step context for a coding agent. It stays much smaller than a DOM,
// accessibility tree, or screenshot.
type VisualObserveOK struct {
	OK             bool                `json:"ok"`
	VisualHandle   string              `json:"visual_handle"`
	Kind           string              `json:"kind"`
	Handle         string              `json:"handle"`
	State          string              `json:"state"`
	Summary        string              `json:"summary"`
	Frame          *VisualFrameSummary `json:"frame,omitempty"`
	Actions        []VisualAction      `json:"actions,omitempty"`
	Regions        []VisualRegion      `json:"regions,omitempty"`
	Errors         []string            `json:"errors,omitempty"`
	ImageAvailable bool                `json:"image_available"`
	Next           VisualNextHint      `json:"next"`
	Metadata       VisualMetadata      `json:"metadata"`
}

// VisualMetadata gives stable surface facts without a screenshot.
type VisualMetadata struct {
	Route        string         `json:"route,omitempty"`
	Title        string         `json:"title,omitempty"`
	Viewport     VisualViewport `json:"viewport"`
	Focused      string         `json:"focused,omitempty"`
	DirtyRegions []string       `json:"dirty_regions,omitempty"`
	ObservedAt   string         `json:"observed_at"`
}

// VisualFrameSummary is a trimmed frame projection. Full ANSI/text remains
// available through render.tui; observe only gives enough screen text to orient.
type VisualFrameSummary struct {
	Width       int                    `json:"width"`
	Height      int                    `json:"height"`
	TextPreview string                 `json:"text_preview,omitempty"`
	Terminal    *VisualTerminalSummary `json:"terminal,omitempty"`
}

// VisualTerminalSummary is the compact xterm-like wrapper for TUI surfaces.
// Rows are the cell grid in terminal character coordinates; action bboxes use
// the same coordinate space so a vision client can crop or click conceptually
// without receiving a giant per-cell object list.
type VisualTerminalSummary struct {
	Rows          []string             `json:"rows"`
	DirtyRows     []int                `json:"dirty_rows,omitempty"`
	Focus         string               `json:"focus,omitempty"`
	Actions       []VisualTerminalItem `json:"actions,omitempty"`
	SlashCommands []string             `json:"slash_commands,omitempty"`
}

// VisualTerminalItem is one terminal affordance located in cell coordinates.
type VisualTerminalItem struct {
	Handle string     `json:"handle"`
	Label  string     `json:"label"`
	Intent string     `json:"intent,omitempty"`
	BBox   VisualBBox `json:"bbox"`
}

// VisualBBox is a rectangle in terminal cells for TUI, or CSS pixels for web
// semantic sidecars.
type VisualBBox struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// VisualAction is one deterministic act candidate.
type VisualAction struct {
	Handle      string         `json:"handle"`
	Kind        string         `json:"kind"`
	Label       string         `json:"label"`
	Intent      string         `json:"intent,omitempty"`
	Disabled    bool           `json:"disabled,omitempty"`
	Description string         `json:"description,omitempty"`
	Slots       map[string]any `json:"slots,omitempty"`
	BBox        *VisualBBox    `json:"bbox,omitempty"`
}

// VisualRegion is a named area that can be requested from visual.snapshot.
type VisualRegion struct {
	ID          string      `json:"id"`
	Label       string      `json:"label"`
	Description string      `json:"description,omitempty"`
	Selector    string      `json:"selector,omitempty"`
	BBox        *VisualBBox `json:"bbox,omitempty"`
}

// VisualNextHint tells the client which cheap call to make next.
type VisualNextHint struct {
	Preferred string   `json:"preferred"`
	Reasons   []string `json:"reasons,omitempty"`
}

// VisualSnapshotArgs is the input to visual.snapshot.
type VisualSnapshotArgs struct {
	VisualHandle string            `json:"visual_handle"`
	Region       string            `json:"region,omitempty"`
	Overlay      string            `json:"overlay,omitempty"`
	Scale        string            `json:"scale,omitempty"`
	Format       string            `json:"format,omitempty"`
	MaxPixels    int               `json:"max_pixels,omitempty"`
	Query        map[string]string `json:"query,omitempty"`
	AssertText   []string          `json:"assert_text,omitempty"`
	Cols         int               `json:"cols,omitempty"`
	Rows         int               `json:"rows,omitempty"`
	Theme        string            `json:"theme,omitempty"`
}

// VisualSnapshotInfo is serialized into the text block that accompanies the
// optional MCP image block.
//
// The request inputs (region/overlay/scale/query) are intentionally NOT echoed
// back here — the caller already holds them in its own request, so echoing them
// only inflates the MCP payload. Result-only metadata (image_id, geometry,
// sha256, bytes) is kept so a follow-on visual.diff/act can address the image.
type VisualSnapshotInfo struct {
	OK           bool           `json:"ok"`
	VisualHandle string         `json:"visual_handle"`
	ImageID      string         `json:"image_id,omitempty"`
	Kind         string         `json:"kind"`
	Handle       string         `json:"handle"`
	Format       string         `json:"format,omitempty"`
	Width        int            `json:"width,omitempty"`
	Height       int            `json:"height,omitempty"`
	Original     VisualViewport `json:"original,omitempty"`
	CropBBox     *VisualBBox    `json:"crop_bbox,omitempty"`
	ScaleFactor  float64        `json:"scale_factor,omitempty"`
	MaxPixels    int            `json:"max_pixels,omitempty"`
	Visible      []string       `json:"visible_action_handles,omitempty"`
	Bytes        int            `json:"bytes,omitempty"`
	SHA256       string         `json:"sha256,omitempty"`
	Semantic     map[string]any `json:"semantic,omitempty"`
}

// VisualActArgs is the input to visual.act.
type VisualActArgs struct {
	VisualHandle string         `json:"visual_handle"`
	Action       string         `json:"action,omitempty"`
	ActionHandle string         `json:"action_handle,omitempty"`
	ImageID      string         `json:"image_id,omitempty"`
	Point        *VisualPoint   `json:"point,omitempty"`
	Text         string         `json:"text,omitempty"`
	Key          string         `json:"key,omitempty"`
	Button       string         `json:"button,omitempty"`
	Modifiers    []string       `json:"modifiers,omitempty"`
	Value        any            `json:"value,omitempty"`
	Region       string         `json:"region,omitempty"`
	Delta        int            `json:"delta,omitempty"`
	Intent       string         `json:"intent,omitempty"`
	Slots        map[string]any `json:"slots,omitempty"`
	Command      string         `json:"command,omitempty"`
	Cols         int            `json:"cols,omitempty"`
	Rows         int            `json:"rows,omitempty"`
}

// VisualPoint is a pixel coordinate in a retained snapshot image.
type VisualPoint struct {
	X int `json:"x"`
	Y int `json:"y"`
}

// VisualActOK is the output of visual.act.
type VisualActOK struct {
	OK             bool           `json:"ok"`
	VisualHandle   string         `json:"visual_handle"`
	Kind           string         `json:"kind"`
	Handle         string         `json:"handle"`
	Action         string         `json:"action"`
	ImageID        string         `json:"image_id,omitempty"`
	Outcome        *TurnResult    `json:"outcome,omitempty"`
	Frame          FrameResult    `json:"frame"`
	ChangedRegions []string       `json:"changed_regions,omitempty"`
	Semantic       map[string]any `json:"semantic,omitempty"`
	NeedsSnapshot  bool           `json:"needs_snapshot"`
}

// VisualDiffArgs is the input to visual.diff.
type VisualDiffArgs struct {
	FromImageID string `json:"from_image_id"`
	ToImageID   string `json:"to_image_id"`
}

// VisualGitDiffArgs captures the same story scene at two git revisions and
// compares the retained screenshots.
type VisualGitDiffArgs struct {
	Dir           string            `json:"dir"`
	From          string            `json:"from"`
	To            string            `json:"to"`
	StoryPath     string            `json:"story_path"`
	State         string            `json:"state"`
	World         map[string]any    `json:"world,omitempty"`
	Query         map[string]string `json:"query,omitempty"`
	AssertText    []string          `json:"assert_text,omitempty"`
	Region        string            `json:"region,omitempty"`
	Overlay       string            `json:"overlay,omitempty"`
	Scale         string            `json:"scale,omitempty"`
	MaxPixels     int               `json:"max_pixels,omitempty"`
	IncludeImages string            `json:"include_images,omitempty"`
}

// VisualDiffOK is a compact delta between retained snapshot metadata.
type VisualDiffOK struct {
	OK             bool        `json:"ok"`
	FromImageID    string      `json:"from_image_id"`
	ToImageID      string      `json:"to_image_id"`
	Same           bool        `json:"same"`
	Changed        bool        `json:"changed"`
	Reasons        []string    `json:"reasons,omitempty"`
	ChangedRegions []string    `json:"changed_regions,omitempty"`
	ChangedBBox    *VisualBBox `json:"changed_bbox,omitempty"`
}

// VisualGitDiffOK is the compact visual regression result. The screenshots stay
// retained server-side and can be compared again with visual.diff via image IDs.
type VisualGitDiffOK struct {
	OK             bool               `json:"ok"`
	RepoRoot       string             `json:"repo_root"`
	From           string             `json:"from"`
	To             string             `json:"to"`
	StoryPath      string             `json:"story_path"`
	State          string             `json:"state"`
	FromImageID    string             `json:"from_image_id"`
	ToImageID      string             `json:"to_image_id"`
	Same           bool               `json:"same"`
	Changed        bool               `json:"changed"`
	Reasons        []string           `json:"reasons,omitempty"`
	ChangedRegions []string           `json:"changed_regions,omitempty"`
	ChangedBBox    *VisualBBox        `json:"changed_bbox,omitempty"`
	FromSnapshot   VisualSnapshotInfo `json:"from_snapshot"`
	ToSnapshot     VisualSnapshotInfo `json:"to_snapshot"`
}

// VisualTUIGitDiffArgs captures the same story scene rendered by the TUI
// rasteriser at two git revisions and compares the resulting screenshots. It
// mirrors VisualGitDiffArgs but drops the web-only DOM query/assert-text
// fields and adds terminal geometry — TUI rendering needs no browser seam, so
// there is no query-selector concept to thread through.
type VisualTUIGitDiffArgs struct {
	Dir           string         `json:"dir"`
	From          string         `json:"from"`
	To            string         `json:"to"`
	StoryPath     string         `json:"story_path"`
	State         string         `json:"state"`
	World         map[string]any `json:"world,omitempty"`
	Cols          int            `json:"cols,omitempty"`
	Rows          int            `json:"rows,omitempty"`
	Theme         string         `json:"theme,omitempty"`
	Region        string         `json:"region,omitempty"`
	Overlay       string         `json:"overlay,omitempty"`
	Scale         string         `json:"scale,omitempty"`
	MaxPixels     int            `json:"max_pixels,omitempty"`
	IncludeImages string         `json:"include_images,omitempty"`
}

// VisualTUIGitDiffOK is the TUI counterpart of VisualGitDiffOK: the same
// compact pixel-regression result, captured via the in-process ANSI
// rasteriser (render.tui_png's path) instead of a browser.
type VisualTUIGitDiffOK struct {
	OK             bool               `json:"ok"`
	RepoRoot       string             `json:"repo_root"`
	From           string             `json:"from"`
	To             string             `json:"to"`
	StoryPath      string             `json:"story_path"`
	State          string             `json:"state"`
	FromImageID    string             `json:"from_image_id"`
	ToImageID      string             `json:"to_image_id"`
	Same           bool               `json:"same"`
	Changed        bool               `json:"changed"`
	Reasons        []string           `json:"reasons,omitempty"`
	ChangedRegions []string           `json:"changed_regions,omitempty"`
	ChangedBBox    *VisualBBox        `json:"changed_bbox,omitempty"`
	FromSnapshot   VisualSnapshotInfo `json:"from_snapshot"`
	ToSnapshot     VisualSnapshotInfo `json:"to_snapshot"`
}

// VisualRecordArgs starts or stops a semantic visual recording.
type VisualRecordArgs struct {
	VisualHandle string `json:"visual_handle,omitempty"`
	Action       string `json:"action"`
	RecordingID  string `json:"recording_id,omitempty"`
	Mode         string `json:"mode,omitempty"`
}

// VisualRecordOK is the output of visual.record.
type VisualRecordOK struct {
	OK           bool     `json:"ok"`
	RecordingID  string   `json:"recording_id"`
	VisualHandle string   `json:"visual_handle"`
	Status       string   `json:"status"`
	Mode         string   `json:"mode"`
	Events       int      `json:"events"`
	Artifacts    []string `json:"artifacts,omitempty"`
}

func (srv *Server) registerVisualTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.open",
		Description: "Open a token-efficient visual surface over an existing driving handle. {kind:web|tui|vscode, handle, viewport?, mode?} -> {visual_handle}. The handle is logical; browser/TUI rendering stays behind visual.observe/snapshot/act.",
	}, srv.handleVisualOpen)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.observe",
		Description: "Return the cheap structured observation for a visual handle: state, compact frame preview, candidate deterministic actions, named regions, and whether a PNG snapshot is available. JSON only; use visual.snapshot for pixels. {visual_handle, cols?, rows?, include_semantic?}: include_semantic=true opts into the web/vscode semantic enrichment (extra render call; default false).",
	}, srv.handleVisualObserve)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.snapshot",
		Description: "Return one targeted PNG for a visual handle, plus JSON metadata. Web/vscode snapshots use the real web renderer; tui snapshots use the terminal rasterizer. Prefer observe first and cropped/region snapshots only when vision is needed.",
	}, srv.handleVisualSnapshot)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.act",
		Description: "Perform a deterministic UI action against a visual handle. Supports submit/click/type/press/select/scroll/continue/command/contextmenu and anchored pixel_click via image_id. Browser-backed actions return retained post-action image_id plus semantic metadata so transient UI can be inspected without a fresh snapshot.",
		InputSchema: visualActInputSchema(),
	}, srv.handleVisualAct)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.diff",
		Description: "Compare two retained visual.snapshot image_ids and return compact metadata/changed-region hints without sending another image.",
	}, srv.handleVisualDiff)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.git_diff",
		Description: "Render the same web scene from two git revisions, retain both screenshots, and return compact visual diff metadata. {dir, from, to, story_path, state, world?, query?, assert_text?, region?, overlay?, scale?, max_pixels?, include_images?:none|from|to|both}. Uses git archive temp trees; no LLM.",
	}, srv.handleVisualGitDiff)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.tui_git_diff",
		Description: "Render the same TUI scene from two git revisions via the in-process ANSI rasteriser (no browser/webShot seam needed) and return compact visual diff metadata. {dir, from, to, story_path, state, world?, cols?, rows?, theme?, region?, overlay?, scale?, max_pixels?, include_images?:none|from|to|both}. Uses git archive temp trees; no LLM.",
	}, srv.handleVisualTUIGitDiff)
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "visual.record",
		Description: "Start or stop a bounded semantic visual recording for evidence. Writes JSON sidecars under .artifacts/visual (or the injected artifacts dir) without invoking an LLM.",
	}, srv.handleVisualRecord)
}

func visualActInputSchema() *jsonschema.Schema {
	schema, err := jsonschema.For[VisualActArgs](nil)
	if err != nil {
		panic(fmt.Errorf("visual.act: build input schema: %w", err))
	}
	schema.Properties["value"] = &jsonschema.Schema{
		Description: "Optional selection value for select-like actions. May be a string, number, boolean, object, or array.",
	}
	return schema
}

func (srv *Server) handleVisualOpen(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualOpenArgs,
) (*mcpsdk.CallToolResult, any, error) {
	kind := normalizeVisualKind(args.Kind)
	if kind == "" {
		return buildToolError(ErrBadRequest, "visual.open: kind must be web, tui, or vscode"), nil, nil
	}
	if args.Handle == "" {
		return buildToolError(ErrBadRequest, "visual.open: handle is required"), nil, nil
	}
	if _, rerr := srv.resolveRuntime(args.Handle); rerr != nil {
		return rerr, nil, nil
	}
	vp := args.Viewport
	if vp.Width <= 0 {
		vp.Width = 1280
	}
	if vp.Height <= 0 {
		vp.Height = 800
	}
	mode := strings.TrimSpace(args.Mode)
	if mode == "" {
		mode = "interactive"
	}
	srv.visualMu.Lock()
	srv.nextVisualID++
	id := fmt.Sprintf("v%d", srv.nextVisualID)
	srv.visualHandles[id] = &VisualHandle{
		ID:       id,
		Kind:     kind,
		Handle:   args.Handle,
		Viewport: vp,
		Mode:     mode,
		Created:  time.Now().UTC(),
	}
	srv.visualMu.Unlock()
	return nil, VisualOpenOK{
		OK:           true,
		VisualHandle: id,
		Kind:         string(kind),
		Handle:       args.Handle,
		Viewport:     vp,
		Mode:         mode,
	}, nil
}

func (srv *Server) handleVisualObserve(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualObserveArgs,
) (*mcpsdk.CallToolResult, any, error) {
	vh, rerr := srv.resolveVisual(args.VisualHandle)
	if rerr != nil {
		return rerr, nil, nil
	}
	rt, rr := srv.resolveRuntime(vh.Handle)
	if rr != nil {
		return rr, nil, nil
	}
	status, err := rt.status(ctx, "")
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	frame := rt.frame(cols, rows)
	frameSummary := &VisualFrameSummary{Width: frame.Width, Height: frame.Height, TextPreview: previewText(frame.Text, 1200)}
	if vh.Kind == VisualTUI {
		frameSummary.Terminal = terminalSummary(frame)
	}
	out := VisualObserveOK{
		OK:             true,
		VisualHandle:   vh.ID,
		Kind:           string(vh.Kind),
		Handle:         vh.Handle,
		State:          status.State,
		Summary:        visualSummary(vh.Kind, status.State, len(status.AllowedIntents)),
		Frame:          frameSummary,
		Actions:        visualActions(vh.Kind, status.AllowedIntents, frame),
		Regions:        defaultVisualRegions(vh.Kind),
		ImageAvailable: visualImageAvailable(vh.Kind, srv.webShot != nil),
		Metadata: VisualMetadata{
			Route:        visualRoute(vh),
			Title:        fmt.Sprintf("Kitsoki %s %s", vh.Kind, status.State),
			Viewport:     vh.Viewport,
			Focused:      "frame",
			DirtyRegions: []string{"frame"},
			ObservedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		},
		Next: VisualNextHint{
			Preferred: "visual.act",
			Reasons:   []string{"structured state and deterministic actions are available; request visual.snapshot only for layout or ambiguity"},
		},
	}
	if args.IncludeSemantic && (vh.Kind == VisualWeb || vh.Kind == VisualVSCode) {
		if semantic, errs, imageOK := srv.visualWebSemantic(ctx, vh); semantic != nil || len(errs) > 0 {
			applyVisualSemantic(&out, semantic, errs)
			if !imageOK {
				out.ImageAvailable = false
				out.Next.Reasons = append(out.Next.Reasons, "web image snapshot capture failed; see errors")
			}
		}
	}
	if !out.ImageAvailable {
		out.Next.Reasons = append(out.Next.Reasons, "web image snapshots need a browser-capable webShot seam")
	}
	srv.recordVisualEvent(vh.ID, VisualRecordEvent{
		At:    time.Now().UTC().Format(time.RFC3339Nano),
		Type:  "observe",
		State: status.State,
		Data:  map[string]any{"actions": len(out.Actions), "image_available": out.ImageAvailable},
	})
	return nil, out, nil
}

func (srv *Server) handleVisualSnapshot(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualSnapshotArgs,
) (*mcpsdk.CallToolResult, any, error) {
	vh, rerr := srv.resolveVisual(args.VisualHandle)
	if rerr != nil {
		return rerr, nil, nil
	}
	if args.Format != "" && strings.ToLower(strings.TrimSpace(args.Format)) != "png" {
		return buildToolError(ErrBadRequest, "visual.snapshot: format must be png"), nil, nil
	}
	switch vh.Kind {
	case VisualTUI:
		return srv.visualTUISnapshot(ctx, req, vh, args)
	case VisualWeb, VisualVSCode:
		return srv.visualWebSnapshot(ctx, req, vh, args)
	default:
		return buildToolError(ErrBadRequest, "visual.snapshot: unsupported visual kind"), nil, nil
	}
}

func (srv *Server) handleVisualAct(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualActArgs,
) (*mcpsdk.CallToolResult, any, error) {
	vh, rerr := srv.resolveVisual(args.VisualHandle)
	if rerr != nil {
		return rerr, nil, nil
	}
	rt, rr := srv.resolveRuntime(vh.Handle)
	if rr != nil {
		return rr, nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	action := strings.TrimSpace(args.Action)
	if action == "" && args.ActionHandle != "" {
		action = "submit"
	}
	if action == "pixel_click" {
		image, rerr := srv.resolveVisualImage(args.ImageID)
		if rerr != nil {
			return rerr, nil, nil
		}
		if image.VisualHandle != vh.ID {
			return buildToolError(ErrBadRequest, "visual.act: image_id belongs to a different visual_handle"), nil, nil
		}
		if args.Point == nil {
			return buildToolError(ErrBadRequest, "visual.act: pixel_click requires point and image_id"), nil, nil
		}
		imgAction, ok := visualActionAtPoint(image.Actions, *args.Point)
		if !ok {
			return buildToolError(ErrBadRequest, "visual.act: no retained action at point in image_id"), nil, nil
		}
		if imgAction.Disabled {
			return buildToolError(ErrBadRequest, fmt.Sprintf("visual.act: action %q is disabled", imgAction.Handle)), nil, nil
		}
		args.ActionHandle = imgAction.Handle
		if args.Intent == "" {
			args.Intent = imgAction.Intent
		}
		action = "click"
	}
	switch action {
	case "type":
		if args.Text == "" {
			return buildToolError(ErrBadRequest, "visual.act: type requires text"), nil, nil
		}
		buffer := srv.appendVisualInput(vh.ID, args.Text)
		frame := rt.frame(cols, rows)
		srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"chars": len([]rune(args.Text)), "buffer_chars": len([]rune(buffer))}})
		return nil, VisualActOK{
			OK:             true,
			VisualHandle:   vh.ID,
			Kind:           string(vh.Kind),
			Handle:         vh.Handle,
			Action:         action,
			Frame:          frameResult(frame),
			ChangedRegions: []string{nonEmpty(args.Region, "composer")},
			NeedsSnapshot:  false,
		}, nil
	case "press":
		key := strings.TrimSpace(args.Key)
		if key == "" {
			return buildToolError(ErrBadRequest, "visual.act: press requires key"), nil, nil
		}
		switch strings.ToLower(key) {
		case "enter", "return":
			input := srv.consumeVisualInput(vh.ID)
			if strings.TrimSpace(input) == "" {
				return buildToolError(ErrBadRequest, "visual.act: press Enter requires buffered text from visual.act type"), nil, nil
			}
			out, frame := rt.drive(ctx, input, cols, rows)
			srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"key": key, "submitted_chars": len([]rune(input))}})
			return nil, visualActResult(vh, action, out, frame, rt.lastTurnErr), nil
		case "escape":
			srv.setVisualInput(vh.ID, "")
			frame := rt.frame(cols, rows)
			srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"key": key, "cleared": true}})
			return nil, VisualActOK{OK: true, VisualHandle: vh.ID, Kind: string(vh.Kind), Handle: vh.Handle, Action: action, Frame: frameResult(frame), ChangedRegions: []string{nonEmpty(args.Region, "composer")}, NeedsSnapshot: false}, nil
		case "backspace":
			buffer := srv.backspaceVisualInput(vh.ID)
			frame := rt.frame(cols, rows)
			srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"key": key, "buffer_chars": len([]rune(buffer))}})
			return nil, VisualActOK{OK: true, VisualHandle: vh.ID, Kind: string(vh.Kind), Handle: vh.Handle, Action: action, Frame: frameResult(frame), ChangedRegions: []string{nonEmpty(args.Region, "composer")}, NeedsSnapshot: false}, nil
		default:
			if len([]rune(key)) == 1 {
				buffer := srv.appendVisualInput(vh.ID, key)
				frame := rt.frame(cols, rows)
				srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"key": key, "buffer_chars": len([]rune(buffer))}})
				return nil, VisualActOK{OK: true, VisualHandle: vh.ID, Kind: string(vh.Kind), Handle: vh.Handle, Action: action, Frame: frameResult(frame), ChangedRegions: []string{nonEmpty(args.Region, "composer")}, NeedsSnapshot: false}, nil
			}
			return buildToolError(ErrBadRequest, "visual.act: unsupported key; use Enter, Escape, Backspace, or a single character"), nil, nil
		}
	case "submit", "click":
		intent := args.Intent
		if intent == "" {
			intent = intentFromActionHandle(args.ActionHandle)
		}
		if intent == "" && (vh.Kind == VisualWeb || vh.Kind == VisualVSCode) {
			return srv.visualWebAct(ctx, vh, args, action, cols, rows)
		}
		if intent == "" {
			return buildToolError(ErrBadRequest, "visual.act: submit/click requires intent or action_handle intent:<name>"), nil, nil
		}
		out, frame := rt.submit(ctx, intent, args.Slots, cols, rows)
		srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"intent": intent}})
		return nil, visualActResult(vh, action, out, frame, rt.lastTurnErr), nil
	case "contextmenu", "right_click":
		if vh.Kind != VisualWeb && vh.Kind != VisualVSCode {
			return buildToolError(ErrBadRequest, "visual.act: contextmenu is only supported for web/vscode visual handles"), nil, nil
		}
		return srv.visualWebAct(ctx, vh, args, action, cols, rows)
	case "select":
		intent := args.Intent
		if intent == "" {
			intent = intentFromActionHandle(args.ActionHandle)
		}
		if intent == "" {
			return buildToolError(ErrBadRequest, "visual.act: select requires intent or action_handle intent:<name>"), nil, nil
		}
		slots := cloneSlots(args.Slots)
		if args.Value != nil {
			slots["value"] = args.Value
		}
		out, frame := rt.submit(ctx, intent, slots, cols, rows)
		srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"intent": intent, "slots": len(slots)}})
		return nil, visualActResult(vh, action, out, frame, rt.lastTurnErr), nil
	case "scroll":
		frame := rt.frame(cols, rows)
		srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"region": args.Region, "delta": args.Delta}})
		return nil, VisualActOK{
			OK:             true,
			VisualHandle:   vh.ID,
			Kind:           string(vh.Kind),
			Handle:         vh.Handle,
			Action:         action,
			Frame:          frameResult(frame),
			ChangedRegions: []string{nonEmpty(args.Region, "viewport")},
			NeedsSnapshot:  true,
		}, nil
	case "continue":
		out, frame := rt.cont(ctx, args.Slots, cols, rows)
		srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action})
		return nil, visualActResult(vh, action, out, frame, rt.lastTurnErr), nil
	case "command":
		if args.Command == "" {
			return buildToolError(ErrBadRequest, "visual.act: command is required"), nil, nil
		}
		frame, err := rt.slash(args.Command, cols, rows)
		if err != nil {
			return buildToolError(ErrBadRequest, err.Error()), nil, nil
		}
		srv.recordVisualEvent(vh.ID, VisualRecordEvent{At: time.Now().UTC().Format(time.RFC3339Nano), Type: "act", Action: action, Data: map[string]any{"command": args.Command}})
		return nil, VisualActOK{
			OK:             true,
			VisualHandle:   vh.ID,
			Kind:           string(vh.Kind),
			Handle:         vh.Handle,
			Action:         action,
			Frame:          frameResult(frame),
			ChangedRegions: []string{"tui"},
			NeedsSnapshot:  false,
		}, nil
	default:
		return buildToolError(ErrBadRequest, "visual.act: action must be submit, click, contextmenu, right_click, pixel_click, type, press, select, scroll, continue, or command"), nil, nil
	}
}

func (srv *Server) handleVisualDiff(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualDiffArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if args.FromImageID == "" || args.ToImageID == "" {
		return buildToolError(ErrBadRequest, "visual.diff: from_image_id and to_image_id are required"), nil, nil
	}
	from, to, rerr := srv.resolveVisualImages(args.FromImageID, args.ToImageID)
	if rerr != nil {
		return rerr, nil, nil
	}
	return nil, visualDiffImages(from, to), nil
}

func visualDiffImages(from, to *VisualImage) VisualDiffOK {
	var reasons []string
	if from.SHA256 != to.SHA256 {
		reasons = append(reasons, "sha256 changed")
	}
	if from.Bytes != to.Bytes {
		reasons = append(reasons, "byte size changed")
	}
	if from.Region != to.Region {
		reasons = append(reasons, "region changed")
	}
	if from.VisualHandle != to.VisualHandle {
		reasons = append(reasons, "visual handle changed")
	}
	bbox := changedImageBBox(from.PNG, to.PNG)
	if bbox != nil {
		reasons = append(reasons, "pixels changed")
	}
	changed := len(reasons) > 0
	changedRegions := []string{nonEmpty(to.Region, "full")}
	if bbox != nil {
		changedRegions = []string{fmt.Sprintf("bbox:%d,%d,%d,%d", bbox.X, bbox.Y, bbox.Width, bbox.Height)}
	}
	return VisualDiffOK{
		OK:             true,
		FromImageID:    from.ID,
		ToImageID:      to.ID,
		Same:           !changed,
		Changed:        changed,
		Reasons:        reasons,
		ChangedRegions: changedRegions,
		ChangedBBox:    bbox,
	}
}

func (srv *Server) handleVisualGitDiff(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualGitDiffArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.Dir) == "" {
		return buildToolError(ErrBadRequest, "visual.git_diff: dir is required"), nil, nil
	}
	if strings.TrimSpace(args.From) == "" || strings.TrimSpace(args.To) == "" {
		return buildToolError(ErrBadRequest, "visual.git_diff: from and to are required"), nil, nil
	}
	if strings.TrimSpace(args.StoryPath) == "" || strings.TrimSpace(args.State) == "" {
		return buildToolError(ErrBadRequest, "visual.git_diff: story_path and state are required"), nil, nil
	}
	includeImages, ok := normalizeGitDiffIncludeImages(args.IncludeImages)
	if !ok {
		return buildToolError(ErrBadRequest, "visual.git_diff: include_images must be none, from, to, or both"), nil, nil
	}
	shotResult := srv.webShotResult
	if shotResult == nil && srv.webShot != nil {
		shotResult = func(ctx context.Context, spec WebRenderSpec) (WebShotResult, error) {
			png, err := srv.webShot(ctx, spec)
			return WebShotResult{PNG: png}, err
		}
	}
	if shotResult == nil {
		return buildToolError(ErrBadRequest, "visual.git_diff: web image rendering needs a browser-capable host"), nil, nil
	}
	repoRoot, err := gitRepoRoot(ctx, args.Dir)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: %v", err)), nil, nil
	}
	storyRel, err := gitSceneStoryRel(repoRoot, args.StoryPath)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: %v", err)), nil, nil
	}
	fromRev, err := gitCommit(ctx, repoRoot, args.From)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: from: %v", err)), nil, nil
	}
	toRev, err := gitCommit(ctx, repoRoot, args.To)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: to: %v", err)), nil, nil
	}
	tmpRoot, err := os.MkdirTemp("", "kitsoki-visual-gitdiff-*")
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: temp dir: %v", err)), nil, nil
	}
	defer os.RemoveAll(tmpRoot)
	fromDir := filepath.Join(tmpRoot, "from")
	toDir := filepath.Join(tmpRoot, "to")
	if err := gitArchiveToDir(ctx, repoRoot, fromRev, fromDir); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: archive from: %v", err)), nil, nil
	}
	if err := gitArchiveToDir(ctx, repoRoot, toRev, toDir); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: archive to: %v", err)), nil, nil
	}
	fromPath := filepath.Join(fromDir, storyRel)
	toPath := filepath.Join(toDir, storyRel)
	if _, err := os.Stat(fromPath); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: from story_path %q: %v", storyRel, err)), nil, nil
	}
	if _, err := os.Stat(toPath); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: to story_path %q: %v", storyRel, err)), nil, nil
	}
	fromImage, fromInfo, fromPNG, err := srv.captureGitScene(ctx, shotResult, fromPath, fromRev, args)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: capture from: %v", err)), nil, nil
	}
	toImage, toInfo, toPNG, err := srv.captureGitScene(ctx, shotResult, toPath, toRev, args)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.git_diff: capture to: %v", err)), nil, nil
	}
	diff := visualDiffImages(fromImage, toImage)
	out := VisualGitDiffOK{
		OK:             true,
		RepoRoot:       repoRoot,
		From:           fromRev,
		To:             toRev,
		StoryPath:      storyRel,
		State:          args.State,
		FromImageID:    fromImage.ID,
		ToImageID:      toImage.ID,
		Same:           diff.Same,
		Changed:        diff.Changed,
		Reasons:        diff.Reasons,
		ChangedRegions: diff.ChangedRegions,
		ChangedBBox:    diff.ChangedBBox,
		FromSnapshot:   fromInfo,
		ToSnapshot:     toInfo,
	}
	return visualGitDiffResult(req, out, fromPNG, toPNG, includeImages), nil, nil
}

// handleVisualTUIGitDiff mirrors handleVisualGitDiff but captures via the
// in-process ANSI rasteriser (specFrame + shot.RenderPNG, the same headless
// path render.tui_png uses) instead of a browser. There is no webShot-style
// seam to preflight: TUI rendering is pure Go and always available, so this
// tool never degrades for want of a browser-capable host.
func (srv *Server) handleVisualTUIGitDiff(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualTUIGitDiffArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.Dir) == "" {
		return buildToolError(ErrBadRequest, "visual.tui_git_diff: dir is required"), nil, nil
	}
	if strings.TrimSpace(args.From) == "" || strings.TrimSpace(args.To) == "" {
		return buildToolError(ErrBadRequest, "visual.tui_git_diff: from and to are required"), nil, nil
	}
	if strings.TrimSpace(args.StoryPath) == "" || strings.TrimSpace(args.State) == "" {
		return buildToolError(ErrBadRequest, "visual.tui_git_diff: story_path and state are required"), nil, nil
	}
	includeImages, ok := normalizeGitDiffIncludeImages(args.IncludeImages)
	if !ok {
		return buildToolError(ErrBadRequest, "visual.tui_git_diff: include_images must be none, from, to, or both"), nil, nil
	}
	cols, rows := geometry(args.Cols, args.Rows)
	repoRoot, err := gitRepoRoot(ctx, args.Dir)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: %v", err)), nil, nil
	}
	storyRel, err := gitSceneStoryRel(repoRoot, args.StoryPath)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: %v", err)), nil, nil
	}
	fromRev, err := gitCommit(ctx, repoRoot, args.From)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: from: %v", err)), nil, nil
	}
	toRev, err := gitCommit(ctx, repoRoot, args.To)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: to: %v", err)), nil, nil
	}
	tmpRoot, err := os.MkdirTemp("", "kitsoki-visual-tui-gitdiff-*")
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: temp dir: %v", err)), nil, nil
	}
	defer os.RemoveAll(tmpRoot)
	fromDir := filepath.Join(tmpRoot, "from")
	toDir := filepath.Join(tmpRoot, "to")
	if err := gitArchiveToDir(ctx, repoRoot, fromRev, fromDir); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: archive from: %v", err)), nil, nil
	}
	if err := gitArchiveToDir(ctx, repoRoot, toRev, toDir); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: archive to: %v", err)), nil, nil
	}
	fromPath := filepath.Join(fromDir, storyRel)
	toPath := filepath.Join(toDir, storyRel)
	if _, err := os.Stat(fromPath); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: from story_path %q: %v", storyRel, err)), nil, nil
	}
	if _, err := os.Stat(toPath); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: to story_path %q: %v", storyRel, err)), nil, nil
	}
	fromImage, fromInfo, fromPNG, err := srv.captureGitSceneTUI(ctx, fromPath, fromRev, args, cols, rows)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: capture from: %v", err)), nil, nil
	}
	toImage, toInfo, toPNG, err := srv.captureGitSceneTUI(ctx, toPath, toRev, args, cols, rows)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.tui_git_diff: capture to: %v", err)), nil, nil
	}
	diff := visualDiffImages(fromImage, toImage)
	out := VisualTUIGitDiffOK{
		OK:             true,
		RepoRoot:       repoRoot,
		From:           fromRev,
		To:             toRev,
		StoryPath:      storyRel,
		State:          args.State,
		FromImageID:    fromImage.ID,
		ToImageID:      toImage.ID,
		Same:           diff.Same,
		Changed:        diff.Changed,
		Reasons:        diff.Reasons,
		ChangedRegions: diff.ChangedRegions,
		ChangedBBox:    diff.ChangedBBox,
		FromSnapshot:   fromInfo,
		ToSnapshot:     toInfo,
	}
	return visualTUIGitDiffResult(req, out, fromPNG, toPNG, includeImages), nil, nil
}

// captureGitSceneTUI renders one TUI scene for visual.tui_git_diff: a
// headless spec render (specFrame — the same in-process, no-LLM path
// render.tui_png uses) rasterised to a PNG via shot.RenderPNG. Unlike
// captureGitScene this needs no injected webShot seam — TUI rasterisation is
// pure Go and always available, headless or otherwise.
func (srv *Server) captureGitSceneTUI(ctx context.Context, storyPath, rev string, args VisualTUIGitDiffArgs, cols, rows int) (*VisualImage, VisualSnapshotInfo, []byte, error) {
	frame, err := srv.specFrame(ctx, storyPath, args.State, args.World, cols, rows)
	if err != nil {
		return nil, VisualSnapshotInfo{}, nil, err
	}
	theme := blocks.ThemeByName(args.Theme)
	var buf bytes.Buffer
	if err := shot.RenderPNG(&buf, frame.ANSI, shot.Options{Theme: theme, Cols: cols, Rows: rows}); err != nil {
		return nil, VisualSnapshotInfo{}, nil, fmt.Errorf("rasterise: %w", err)
	}
	processed, err := processVisualPNG(buf.Bytes(), args.Region, args.Overlay, args.Scale, args.MaxPixels, nil)
	if err != nil {
		return nil, VisualSnapshotInfo{}, nil, err
	}
	vh := &VisualHandle{
		ID:       "git:" + rev,
		Kind:     VisualTUI,
		Handle:   storyPath,
		Viewport: processed.Original,
		Mode:     "git_diff",
		Created:  time.Now().UTC(),
	}
	image := srv.storeVisualImage(vh, args.Region, args.Overlay, args.Scale, processed.PNG, processed)
	info := VisualSnapshotInfo{
		OK:           true,
		VisualHandle: vh.ID,
		ImageID:      image.ID,
		Kind:         string(VisualTUI),
		Handle:       storyPath,
		Format:       "png",
		Width:        image.Width,
		Height:       image.Height,
		Original:     image.Original,
		CropBBox:     image.CropBBox,
		ScaleFactor:  image.ScaleFactor,
		MaxPixels:    processed.MaxPixels,
		Visible:      visualImageActionHandles(image.Actions),
		Bytes:        image.Bytes,
		SHA256:       image.SHA256,
	}
	return image, info, processed.PNG, nil
}

// visualTUIGitDiffResult mirrors visualGitDiffResult for the TUI result type.
func visualTUIGitDiffResult(req *mcpsdk.CallToolRequest, out VisualTUIGitDiffOK, fromPNG, toPNG []byte, include string) *mcpsdk.CallToolResult {
	content := []mcpsdk.Content{&mcpsdk.TextContent{Text: mustJSON(out)}}
	if !clientSupportsImages(req) {
		return &mcpsdk.CallToolResult{Content: content}
	}
	switch strings.ToLower(strings.TrimSpace(include)) {
	case "", "none":
	case "from":
		if len(fromPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: fromPNG, MIMEType: "image/png"})
		}
	case "to":
		if len(toPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: toPNG, MIMEType: "image/png"})
		}
	case "both":
		if len(fromPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: fromPNG, MIMEType: "image/png"})
		}
		if len(toPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: toPNG, MIMEType: "image/png"})
		}
	}
	return &mcpsdk.CallToolResult{Content: content}
}

func (srv *Server) handleVisualRecord(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args VisualRecordArgs,
) (*mcpsdk.CallToolResult, any, error) {
	switch strings.TrimSpace(args.Action) {
	case "start":
		vh, rerr := srv.resolveVisual(args.VisualHandle)
		if rerr != nil {
			return rerr, nil, nil
		}
		mode := strings.TrimSpace(args.Mode)
		if mode == "" {
			mode = "semantic"
		}
		id := srv.startVisualRecording(vh, mode)
		return nil, VisualRecordOK{OK: true, RecordingID: id, VisualHandle: vh.ID, Status: "recording", Mode: mode}, nil
	case "stop":
		rec, artifacts, rerr := srv.stopVisualRecording(args.RecordingID)
		if rerr != nil {
			return rerr, nil, nil
		}
		return nil, VisualRecordOK{OK: true, RecordingID: rec.ID, VisualHandle: rec.VisualHandle, Status: rec.Status, Mode: rec.Mode, Events: len(rec.Events), Artifacts: artifacts}, nil
	default:
		return buildToolError(ErrBadRequest, "visual.record: action must be start or stop"), nil, nil
	}
}

func (srv *Server) captureGitScene(ctx context.Context, shotResult WebShotResultFunc, storyPath, rev string, args VisualGitDiffArgs) (*VisualImage, VisualSnapshotInfo, []byte, error) {
	result, err := shotResult(ctx, WebRenderSpec{
		StoryPath:  storyPath,
		State:      args.State,
		World:      args.World,
		Query:      args.Query,
		AssertText: args.AssertText,
	})
	if err != nil {
		return nil, VisualSnapshotInfo{}, nil, err
	}
	if len(result.PNG) == 0 {
		return nil, VisualSnapshotInfo{}, nil, fmt.Errorf("web renderer returned an empty PNG")
	}
	semantic := compactSemantic(result.SemanticJSON)
	processed, err := processVisualPNG(result.PNG, args.Region, args.Overlay, args.Scale, args.MaxPixels, semantic)
	if err != nil {
		return nil, VisualSnapshotInfo{}, nil, err
	}
	vh := &VisualHandle{
		ID:       "git:" + rev,
		Kind:     VisualWeb,
		Handle:   storyPath,
		Viewport: processed.Original,
		Mode:     "git_diff",
		Created:  time.Now().UTC(),
	}
	image := srv.storeVisualImage(vh, args.Region, args.Overlay, args.Scale, processed.PNG, processed)
	info := VisualSnapshotInfo{
		OK:           true,
		VisualHandle: vh.ID,
		ImageID:      image.ID,
		Kind:         string(VisualWeb),
		Handle:       storyPath,
		Format:       "png",
		Width:        image.Width,
		Height:       image.Height,
		Original:     image.Original,
		CropBBox:     image.CropBBox,
		ScaleFactor:  image.ScaleFactor,
		MaxPixels:    processed.MaxPixels,
		Visible:      visualImageActionHandles(image.Actions),
		Bytes:        image.Bytes,
		SHA256:       image.SHA256,
		Semantic:     semantic,
	}
	return image, info, processed.PNG, nil
}

func visualGitDiffResult(req *mcpsdk.CallToolRequest, out VisualGitDiffOK, fromPNG, toPNG []byte, include string) *mcpsdk.CallToolResult {
	content := []mcpsdk.Content{&mcpsdk.TextContent{Text: mustJSON(out)}}
	if !clientSupportsImages(req) {
		return &mcpsdk.CallToolResult{Content: content}
	}
	switch strings.ToLower(strings.TrimSpace(include)) {
	case "", "none":
	case "from":
		if len(fromPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: fromPNG, MIMEType: "image/png"})
		}
	case "to":
		if len(toPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: toPNG, MIMEType: "image/png"})
		}
	case "both":
		if len(fromPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: fromPNG, MIMEType: "image/png"})
		}
		if len(toPNG) > 0 {
			content = append(content, &mcpsdk.ImageContent{Data: toPNG, MIMEType: "image/png"})
		}
	}
	return &mcpsdk.CallToolResult{Content: content}
}

func normalizeGitDiffIncludeImages(v string) (string, bool) {
	include := strings.ToLower(strings.TrimSpace(v))
	if include == "" {
		include = "none"
	}
	switch include {
	case "none", "from", "to", "both":
		return include, true
	default:
		return "", false
	}
}

func gitRepoRoot(ctx context.Context, dir string) (string, error) {
	out, err := gitOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return "", fmt.Errorf("%q is not inside a git repository", dir)
	}
	return root, nil
}

func gitCommit(ctx context.Context, repoRoot, rev string) (string, error) {
	out, err := gitOutput(ctx, repoRoot, "rev-parse", "--verify", strings.TrimSpace(rev)+"^{commit}")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(out)
	if commit == "" {
		return "", fmt.Errorf("empty resolved revision")
	}
	return commit, nil
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func gitSceneStoryRel(repoRoot, storyPath string) (string, error) {
	path := filepath.Clean(storyPath)
	if filepath.IsAbs(path) {
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return "", err
		}
		path = rel
	}
	if path == "." || path == "" {
		return "", fmt.Errorf("story_path must name a file inside the repository")
	}
	if filepath.IsAbs(path) || strings.HasPrefix(path, ".."+string(filepath.Separator)) || path == ".." {
		return "", fmt.Errorf("story_path %q must stay inside the repository", storyPath)
	}
	return filepath.ToSlash(path), nil
}

func gitArchiveToDir(ctx context.Context, repoRoot, rev, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", repoRoot, "archive", "--format=tar", rev)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	tr := tar.NewReader(stdout)
	readErr := extractTar(tr, dst)
	waitErr := cmd.Wait()
	if readErr != nil {
		return readErr
	}
	if waitErr != nil {
		return fmt.Errorf("git archive: %v: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func extractTar(tr *tar.Reader, dst string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		rel, ok := safeArchivePath(hdr.Name)
		if !ok {
			return fmt.Errorf("unsafe archive path %q", hdr.Name)
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		case tar.TypeSymlink:
			link, ok := safeArchivePath(hdr.Linkname)
			if !ok {
				return fmt.Errorf("unsafe archive symlink %q -> %q", hdr.Name, hdr.Linkname)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(filepath.FromSlash(link), target); err != nil {
				return err
			}
		}
	}
}

func safeArchivePath(p string) (string, bool) {
	clean := pathCleanSlash(p)
	if clean == "." || clean == "" || strings.HasPrefix(clean, "../") || clean == ".." || filepath.IsAbs(clean) {
		return "", false
	}
	return clean, true
}

func pathCleanSlash(p string) string {
	return filepath.ToSlash(filepath.Clean(filepath.FromSlash(p)))
}

func (srv *Server) visualWebSnapshot(ctx context.Context, req *mcpsdk.CallToolRequest, vh *VisualHandle, args VisualSnapshotArgs) (*mcpsdk.CallToolResult, any, error) {
	shotResult := srv.webShotResult
	if shotResult == nil && srv.webShot != nil {
		shotResult = func(ctx context.Context, spec WebRenderSpec) (WebShotResult, error) {
			png, err := srv.webShot(ctx, spec)
			return WebShotResult{PNG: png}, err
		}
	}
	if shotResult == nil {
		return imageResult(req, mustJSON(VisualSnapshotInfo{
			OK:           false,
			VisualHandle: vh.ID,
			Kind:         string(vh.Kind),
			Handle:       vh.Handle,
		})+"\nvisual.snapshot: web image rendering needs a browser-capable host (none attached)", nil, ""), nil, nil
	}
	spec, rerr := srv.webSpec(RenderArgs{Handle: vh.Handle, Query: args.Query, AssertText: args.AssertText})
	if rerr != nil {
		return rerr, nil, nil
	}
	result, err := shotResult(ctx, spec)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.snapshot: %v", err)), nil, nil
	}
	if len(result.PNG) == 0 {
		return buildToolError(ErrBadRequest, "visual.snapshot: web renderer returned an empty PNG"), nil, nil
	}
	semantic := compactSemantic(result.SemanticJSON)
	processed, err := processVisualPNG(result.PNG, args.Region, args.Overlay, args.Scale, args.MaxPixels, semantic)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.snapshot: %v", err)), nil, nil
	}
	image := srv.storeVisualImage(vh, args.Region, args.Overlay, args.Scale, processed.PNG, processed)
	info := VisualSnapshotInfo{
		OK:           true,
		VisualHandle: vh.ID,
		ImageID:      image.ID,
		Kind:         string(vh.Kind),
		Handle:       vh.Handle,
		Format:       nonEmpty(args.Format, "png"),
		Width:        image.Width,
		Height:       image.Height,
		Original:     image.Original,
		CropBBox:     image.CropBBox,
		ScaleFactor:  image.ScaleFactor,
		MaxPixels:    processed.MaxPixels,
		Visible:      visualImageActionHandles(image.Actions),
		Bytes:        image.Bytes,
		SHA256:       image.SHA256,
		Semantic:     semantic,
	}
	srv.recordVisualEvent(vh.ID, VisualRecordEvent{
		At:      time.Now().UTC().Format(time.RFC3339Nano),
		Type:    "snapshot",
		ImageID: image.ID,
		Region:  args.Region,
		Data:    map[string]any{"bytes": image.Bytes, "sha256": image.SHA256, "rrweb_events": rrwebEventCount(result.RRWebJSON)},
	})
	srv.recordVisualRRWeb(vh.ID, result.RRWebJSON)
	return imageResult(req, mustJSON(info), processed.PNG, "image/png"), nil, nil
}

func (srv *Server) visualWebSemantic(ctx context.Context, vh *VisualHandle) (map[string]any, []string, bool) {
	shotResult := srv.webShotResult
	if shotResult == nil {
		return nil, nil, true
	}
	spec, rerr := srv.webSpec(RenderArgs{Handle: vh.Handle})
	if rerr != nil {
		return nil, []string{"visual.observe: web semantic observation could not resolve the session"}, false
	}
	result, err := shotResult(ctx, spec)
	if err != nil {
		return nil, []string{err.Error()}, false
	}
	semantic := compactSemantic(result.SemanticJSON)
	if len(semantic) == 0 {
		return nil, nil, true
	}
	if ok, _ := semantic["ok"].(bool); !ok {
		if msg, _ := semantic["error"].(string); msg != "" {
			return semantic, []string{msg}, true
		}
	}
	return semantic, nil, true
}

func applyVisualSemantic(out *VisualObserveOK, semantic map[string]any, errs []string) {
	out.Errors = append(out.Errors, errs...)
	if len(semantic) == 0 {
		return
	}
	if route, _ := semantic["route"].(string); route != "" {
		out.Metadata.Route = route
	}
	if title, _ := semantic["title"].(string); title != "" {
		out.Metadata.Title = title
	}
	if focused, _ := semantic["focused"].(string); focused != "" {
		out.Metadata.Focused = focused
	}
	if dirty := stringSliceFromAny(semantic["dirty_regions"]); len(dirty) > 0 {
		out.Metadata.DirtyRegions = dirty
	}
	if vp, ok := viewportFromAny(semantic["viewport"]); ok {
		out.Metadata.Viewport = vp
	}
	if actions := semanticVisualActions(semantic); len(actions) > 0 {
		out.Actions = actions
	}
	if regions := semanticVisualRegions(semantic); len(regions) > 0 {
		out.Regions = regions
	}
}

func (srv *Server) visualWebAct(ctx context.Context, vh *VisualHandle, args VisualActArgs, action string, cols, rows int) (*mcpsdk.CallToolResult, any, error) {
	if srv.webAct == nil {
		return buildToolError(ErrBadRequest, "visual.act: web browser actions need a browser-capable host (none attached)"), nil, nil
	}
	if strings.TrimSpace(args.ActionHandle) == "" && args.Point == nil {
		return buildToolError(ErrBadRequest, "visual.act: web action requires action_handle or point"), nil, nil
	}
	spec, rerr := srv.webSpec(RenderArgs{Handle: vh.Handle})
	if rerr != nil {
		return rerr, nil, nil
	}
	button := strings.TrimSpace(args.Button)
	if button == "" && (action == "contextmenu" || action == "right_click") {
		button = "right"
	}
	webAction := WebActionSpec{
		Kind:         action,
		ActionHandle: strings.TrimSpace(args.ActionHandle),
		Point:        args.Point,
		Button:       button,
		Modifiers:    normalizeWebModifiers(args.Modifiers),
	}
	result, err := srv.webAct(ctx, spec, webAction)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("visual.act: %v", err)), nil, nil
	}
	frame := srv.visualPostActionFrame(ctx, vh, cols, rows)
	semantic := compactSemantic(result.SemanticJSON)
	changed := []string{"viewport"}
	if dirty := stringSliceFromAny(semantic["dirty_regions"]); len(dirty) > 0 {
		changed = dirty
	}
	imageID := ""
	needsSnapshot := true
	if len(result.PNG) > 0 {
		processed, err := processVisualPNG(result.PNG, "", "", "", 0, semantic)
		if err != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("visual.act: %v", err)), nil, nil
		}
		image := srv.storeVisualImage(vh, "", "", "", processed.PNG, processed)
		imageID = image.ID
		needsSnapshot = false
	}
	srv.recordVisualEvent(vh.ID, VisualRecordEvent{
		At:     time.Now().UTC().Format(time.RFC3339Nano),
		Type:   "act",
		Action: action,
		Data: map[string]any{
			"action_handle": webAction.ActionHandle,
			"button":        webAction.Button,
			"modifiers":     webAction.Modifiers,
			"point":         webAction.Point,
			"semantic":      semantic,
		},
	})
	return nil, VisualActOK{
		OK:             true,
		VisualHandle:   vh.ID,
		Kind:           string(vh.Kind),
		Handle:         vh.Handle,
		Action:         action,
		ImageID:        imageID,
		Frame:          frame,
		ChangedRegions: changed,
		Semantic:       semantic,
		NeedsSnapshot:  needsSnapshot,
	}, nil
}

func (srv *Server) visualPostActionFrame(ctx context.Context, vh *VisualHandle, cols, rows int) FrameResult {
	rt, rr := srv.resolveRuntime(vh.Handle)
	if rr != nil {
		return FrameResult{Width: cols, Height: rows}
	}
	return frameResult(rt.frame(cols, rows))
}

func normalizeWebModifiers(mods []string) []string {
	out := make([]string, 0, len(mods))
	seen := map[string]bool{}
	for _, mod := range mods {
		switch strings.ToLower(strings.TrimSpace(mod)) {
		case "alt", "option":
			if !seen["Alt"] {
				out = append(out, "Alt")
				seen["Alt"] = true
			}
		case "control", "ctrl":
			if !seen["Control"] {
				out = append(out, "Control")
				seen["Control"] = true
			}
		case "meta", "cmd", "command":
			if !seen["Meta"] {
				out = append(out, "Meta")
				seen["Meta"] = true
			}
		case "shift":
			if !seen["Shift"] {
				out = append(out, "Shift")
				seen["Shift"] = true
			}
		}
	}
	return out
}

func (srv *Server) visualTUISnapshot(ctx context.Context, req *mcpsdk.CallToolRequest, vh *VisualHandle, args VisualSnapshotArgs) (*mcpsdk.CallToolResult, any, error) {
	cols, rows := geometry(args.Cols, args.Rows)
	frame, rerr := srv.composeRenderFrame(ctx, RenderArgs{Handle: vh.Handle, Cols: cols, Rows: rows, Theme: args.Theme}, cols, rows)
	if rerr != nil {
		return rerr, nil, nil
	}
	var buf strings.Builder
	_ = json.NewEncoder(&buf).Encode(VisualSnapshotInfo{
		OK:           true,
		VisualHandle: vh.ID,
		Kind:         string(vh.Kind),
		Handle:       vh.Handle,
		Format:       "png",
	})
	var pngBytes []byte
	if imgResult, _, err := srv.handleRenderTUIPNG(ctx, req, RenderArgs{Handle: vh.Handle, Cols: cols, Rows: rows, Theme: args.Theme}); err == nil && imgResult != nil {
		for _, c := range imgResult.Content {
			if ic, ok := c.(*mcpsdk.ImageContent); ok {
				pngBytes = ic.Data
				break
			}
		}
	}
	text := strings.TrimSpace(buf.String())
	if len(pngBytes) == 0 {
		text += "\n" + frame.Text
	} else {
		processed, err := processVisualPNG(pngBytes, "", "", args.Scale, args.MaxPixels, nil)
		if err != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("visual.snapshot: %v", err)), nil, nil
		}
		pngBytes = processed.PNG
		image := srv.storeVisualImage(vh, args.Region, args.Overlay, args.Scale, processed.PNG, processed)
		var info VisualSnapshotInfo
		if err := json.Unmarshal([]byte(text), &info); err == nil {
			info.ImageID = image.ID
			info.Bytes = image.Bytes
			info.SHA256 = image.SHA256
			info.Width = image.Width
			info.Height = image.Height
			info.Original = image.Original
			info.ScaleFactor = image.ScaleFactor
			info.MaxPixels = processed.MaxPixels
			text = mustJSON(info)
		}
		srv.recordVisualEvent(vh.ID, VisualRecordEvent{
			At:      time.Now().UTC().Format(time.RFC3339Nano),
			Type:    "snapshot",
			ImageID: image.ID,
			Region:  args.Region,
			Data:    map[string]any{"bytes": image.Bytes, "sha256": image.SHA256},
		})
	}
	return imageResult(req, text, pngBytes, "image/png"), nil, nil
}

func (srv *Server) resolveVisual(id string) (*VisualHandle, *mcpsdk.CallToolResult) {
	if id == "" {
		return nil, buildToolError(ErrBadRequest, "visual_handle is required")
	}
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	vh := srv.visualHandles[id]
	if vh == nil {
		return nil, buildToolError(ErrUnknownHandle, fmt.Sprintf("unknown visual handle %q", id))
	}
	cp := *vh
	return &cp, nil
}

func (srv *Server) appendVisualInput(id, text string) string {
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	vh := srv.visualHandles[id]
	if vh == nil {
		return ""
	}
	vh.Input += text
	return vh.Input
}

func (srv *Server) setVisualInput(id, text string) {
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	if vh := srv.visualHandles[id]; vh != nil {
		vh.Input = text
	}
}

func (srv *Server) consumeVisualInput(id string) string {
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	vh := srv.visualHandles[id]
	if vh == nil {
		return ""
	}
	text := vh.Input
	vh.Input = ""
	return text
}

func (srv *Server) backspaceVisualInput(id string) string {
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	vh := srv.visualHandles[id]
	if vh == nil || vh.Input == "" {
		return ""
	}
	r := []rune(vh.Input)
	vh.Input = string(r[:len(r)-1])
	return vh.Input
}

func normalizeVisualKind(kind string) VisualKind {
	switch VisualKind(strings.ToLower(strings.TrimSpace(kind))) {
	case VisualWeb:
		return VisualWeb
	case VisualTUI:
		return VisualTUI
	case VisualVSCode:
		return VisualVSCode
	default:
		return ""
	}
}

func visualSummary(kind VisualKind, state string, actions int) string {
	return fmt.Sprintf("%s surface for session state %s with %d deterministic actions available", kind, state, actions)
}

func visualRoute(vh *VisualHandle) string {
	switch vh.Kind {
	case VisualTUI:
		return "tui://" + vh.Handle
	case VisualVSCode:
		return "vscode://kitsoki/" + vh.Handle
	default:
		return "#/s/" + vh.Handle
	}
}

func intentActions(intents []string) []VisualAction {
	if len(intents) == 0 {
		return nil
	}
	out := make([]VisualAction, 0, len(intents))
	for _, intent := range intents {
		out = append(out, VisualAction{
			Handle:      "intent:" + intent,
			Kind:        "submit",
			Label:       intent,
			Intent:      intent,
			Description: "Directly submit this Kitsoki intent; provide slots if the intent requires them.",
		})
	}
	return out
}

func cloneSlots(slots map[string]any) map[string]any {
	out := make(map[string]any, len(slots)+1)
	for k, v := range slots {
		out[k] = v
	}
	return out
}

func visualActions(kind VisualKind, intents []string, frame tui.Frame) []VisualAction {
	out := intentActions(intents)
	if kind != VisualTUI || len(out) == 0 {
		return out
	}
	locs := terminalActionLocations(frame, intents)
	for i := range out {
		if loc, ok := locs[out[i].Intent]; ok {
			box := loc.BBox
			out[i].BBox = &box
		}
	}
	return out
}

func terminalSummary(frame tui.Frame) *VisualTerminalSummary {
	rows := terminalRows(frame)
	return &VisualTerminalSummary{
		Rows:          rows,
		DirtyRows:     dirtyRows(rows),
		Focus:         "prompt",
		Actions:       terminalActionItems(frame),
		SlashCommands: tuiSlashCandidates(),
	}
}

func terminalRows(frame tui.Frame) []string {
	lines := strings.Split(strings.ReplaceAll(frame.Text, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if frame.Height > 0 && len(lines) > frame.Height {
		lines = lines[len(lines)-frame.Height:]
	}
	if frame.Width > 0 {
		for i, line := range lines {
			lines[i] = clipRunes(line, frame.Width)
		}
	}
	return lines
}

func dirtyRows(rows []string) []int {
	out := make([]int, 0, len(rows))
	for i, row := range rows {
		if strings.TrimSpace(row) != "" {
			out = append(out, i)
		}
	}
	return out
}

func terminalActionItems(frame tui.Frame) []VisualTerminalItem {
	intents := frame.Metadata.AllowedIntents
	if len(intents) == 0 {
		return nil
	}
	locs := terminalActionLocations(frame, intents)
	out := make([]VisualTerminalItem, 0, len(intents))
	for _, intent := range intents {
		loc, ok := locs[intent]
		if !ok {
			loc = VisualTerminalItem{
				Handle: "intent:" + intent,
				Label:  intent,
				Intent: intent,
				BBox:   fallbackTerminalBBox(frame, len(out)),
			}
		}
		out = append(out, loc)
	}
	return out
}

func terminalActionLocations(frame tui.Frame, intents []string) map[string]VisualTerminalItem {
	rows := terminalRows(frame)
	out := map[string]VisualTerminalItem{}
	for _, intent := range intents {
		if intent == "" {
			continue
		}
		for y, row := range rows {
			x := strings.Index(row, intent)
			if x < 0 {
				continue
			}
			out[intent] = VisualTerminalItem{
				Handle: "intent:" + intent,
				Label:  intent,
				Intent: intent,
				BBox:   VisualBBox{X: runeLen(row[:x]), Y: y, Width: runeLen(intent), Height: 1},
			}
			break
		}
	}
	return out
}

func fallbackTerminalBBox(frame tui.Frame, idx int) VisualBBox {
	y := frame.Height - 1
	if y < 0 {
		y = 0
	}
	x := idx * 12
	if frame.Width > 0 && x >= frame.Width {
		x = 0
	}
	width := 10
	if frame.Width > 0 && x+width > frame.Width {
		width = frame.Width - x
	}
	if width < 1 {
		width = 1
	}
	return VisualBBox{X: x, Y: y, Width: width, Height: 1}
}

func tuiSlashCandidates() []string {
	return []string{
		"/help",
		"/stories",
		"/intents",
		"/work --all",
		"/chat show <id>",
		"/trace",
		"/world",
		"/input",
		"/meta",
	}
}

func clipRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

func runeLen(s string) int { return len([]rune(s)) }

func defaultVisualRegions(kind VisualKind) []VisualRegion {
	switch kind {
	case VisualTUI:
		return []VisualRegion{{ID: "tui", Label: "Terminal frame", Description: "The rendered TUI frame."}}
	default:
		return []VisualRegion{
			{ID: "full", Label: "Full surface", Description: "The full web surface."},
			{ID: "chat", Label: "Chat", Description: "Conversation and composer region when visible."},
			{ID: "media", Label: "Media", Description: "Rendered media artifact region when visible."},
			{ID: "deck", Label: "Deck", Description: "Rendered slide/deck frame when visible."},
			{ID: "annotation", Label: "Annotation", Description: "Open artifact annotation panel when visible."},
			{ID: "trace", Label: "Trace", Description: "Trace/timeline region when visible."},
		}
	}
}

func visualImageAvailable(kind VisualKind, webAvailable bool) bool {
	if kind == VisualTUI {
		return true
	}
	return webAvailable
}

func previewText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

func intentFromActionHandle(handle string) string {
	handle = strings.TrimSpace(handle)
	if strings.HasPrefix(handle, "intent:") {
		return strings.TrimPrefix(handle, "intent:")
	}
	if strings.HasPrefix(handle, "testid:intent-btn-") {
		return strings.TrimPrefix(handle, "testid:intent-btn-")
	}
	return ""
}

func visualActResult(vh *VisualHandle, action string, out *orchestrator.TurnOutcome, frame tui.Frame, turnErr error) VisualActOK {
	tr := turnResponse(out, frame, turnErr)
	return VisualActOK{
		OK:             tr.OK,
		VisualHandle:   vh.ID,
		Kind:           string(vh.Kind),
		Handle:         vh.Handle,
		Action:         action,
		Outcome:        &tr.Outcome,
		Frame:          tr.Frame,
		ChangedRegions: []string{"frame", "state"},
		NeedsSnapshot:  false,
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":%q}`, err.Error())
	}
	return string(b)
}

func compactSemantic(data []byte) map[string]any {
	if len(data) == 0 {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return map[string]any{"ok": false, "error": "semantic observation was not valid JSON"}
	}
	out := map[string]any{}
	for _, key := range []string{"ok", "error", "route", "title", "viewport", "focused", "dirty_regions", "regions", "actions", "observed_at"} {
		if v, ok := raw[key]; ok {
			out[key] = v
		}
	}
	return out
}

type visualPNGResult struct {
	PNG         []byte
	Width       int
	Height      int
	Original    VisualViewport
	CropBBox    *VisualBBox
	ScaleFactor float64
	MaxPixels   int
	Actions     []VisualImageAction
}

func processVisualPNG(data []byte, region, overlay, scale string, maxPixels int, semantic map[string]any) (visualPNGResult, error) {
	out := append([]byte(nil), data...)
	orig, err := pngDimensions(out)
	if err != nil {
		return visualPNGResult{}, err
	}
	result := visualPNGResult{
		PNG:         out,
		Width:       orig.Width,
		Height:      orig.Height,
		Original:    orig,
		ScaleFactor: 1,
		MaxPixels:   visualMaxPixels(scale, maxPixels),
		Actions:     semanticImageActions(semantic),
	}
	var cropOrigin image.Point
	if strings.TrimSpace(region) != "" && strings.TrimSpace(region) != "full" {
		box, ok := semanticRegionBBox(semantic, region)
		if ok {
			cropped, cropRect, err := cropPNG(out, box)
			if err != nil {
				return visualPNGResult{}, err
			}
			out = cropped
			if !cropRect.Empty() {
				cropOrigin = cropRect.Min
				crop := VisualBBox{X: cropRect.Min.X, Y: cropRect.Min.Y, Width: cropRect.Dx(), Height: cropRect.Dy()}
				result.CropBBox = &crop
				result.Actions = translateImageActions(result.Actions, -cropOrigin.X, -cropOrigin.Y)
			}
		}
	}
	scaled, factor, err := scalePNG(out, result.MaxPixels)
	if err != nil {
		return visualPNGResult{}, err
	}
	out = scaled
	result.ScaleFactor = factor
	result.Actions = scaleImageActions(result.Actions, factor)
	if strings.TrimSpace(overlay) != "" && strings.TrimSpace(overlay) != "none" {
		boxes := semanticOverlayBoxes(semantic, overlay)
		if len(boxes) > 0 {
			if cropOrigin != (image.Point{}) {
				boxes = translateBoxes(boxes, -cropOrigin.X, -cropOrigin.Y)
			}
			boxes = scaleBoxes(boxes, factor)
			overlaid, err := overlayPNG(out, boxes)
			if err != nil {
				return visualPNGResult{}, err
			}
			out = overlaid
		}
	}
	dims, err := pngDimensions(out)
	if err != nil {
		return visualPNGResult{}, err
	}
	result.PNG = out
	result.Width = dims.Width
	result.Height = dims.Height
	result.Actions = filterImageActions(result.Actions, result.Width, result.Height)
	return result, nil
}

func semanticRegionBBox(semantic map[string]any, region string) (VisualBBox, bool) {
	regions, _ := semantic["regions"].([]any)
	for _, item := range regions {
		m, _ := item.(map[string]any)
		if fmt.Sprint(m["id"]) != region {
			continue
		}
		return bboxFromAny(m["bbox"])
	}
	return VisualBBox{}, false
}

func semanticOverlayBoxes(semantic map[string]any, overlay string) []VisualBBox {
	switch strings.TrimSpace(overlay) {
	case "action_ids", "actions":
		actions, _ := semantic["actions"].([]any)
		out := make([]VisualBBox, 0, len(actions))
		for _, item := range actions {
			m, _ := item.(map[string]any)
			if box, ok := bboxFromAny(m["bbox"]); ok {
				out = append(out, box)
			}
		}
		return out
	case "regions":
		regions, _ := semantic["regions"].([]any)
		out := make([]VisualBBox, 0, len(regions))
		for _, item := range regions {
			m, _ := item.(map[string]any)
			if box, ok := bboxFromAny(m["bbox"]); ok {
				out = append(out, box)
			}
		}
		return out
	default:
		return nil
	}
}

func semanticImageActions(semantic map[string]any) []VisualImageAction {
	actions, _ := semantic["actions"].([]any)
	out := make([]VisualImageAction, 0, len(actions))
	for _, item := range actions {
		m, _ := item.(map[string]any)
		box, ok := bboxFromAny(m["bbox"])
		if !ok {
			continue
		}
		handle := stringFromAny(m["handle"])
		if handle == "" {
			continue
		}
		out = append(out, VisualImageAction{
			Handle:   handle,
			Label:    stringFromAny(m["label"]),
			Intent:   intentFromActionHandle(handle),
			Disabled: boolFromAny(m["disabled"]),
			BBox:     box,
		})
	}
	return out
}

func semanticVisualActions(semantic map[string]any) []VisualAction {
	imgActions := semanticImageActions(semantic)
	if len(imgActions) == 0 {
		return nil
	}
	out := make([]VisualAction, 0, len(imgActions))
	for _, action := range imgActions {
		box := action.BBox
		out = append(out, VisualAction{
			Handle:   action.Handle,
			Kind:     "click",
			Label:    action.Label,
			Intent:   action.Intent,
			Disabled: action.Disabled,
			BBox:     &box,
		})
	}
	return out
}

func semanticVisualRegions(semantic map[string]any) []VisualRegion {
	regions, _ := semantic["regions"].([]any)
	out := make([]VisualRegion, 0, len(regions))
	for _, item := range regions {
		m, _ := item.(map[string]any)
		id := stringFromAny(m["id"])
		if id == "" {
			continue
		}
		region := VisualRegion{
			ID:       id,
			Label:    stringFromAny(m["label"]),
			Selector: stringFromAny(m["selector"]),
		}
		if region.Label == "" {
			region.Label = id
		}
		if box, ok := bboxFromAny(m["bbox"]); ok {
			region.BBox = &box
		}
		out = append(out, region)
	}
	return out
}

func stringFromAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func viewportFromAny(v any) (VisualViewport, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return VisualViewport{}, false
	}
	vp := VisualViewport{Width: intFromAny(m["width"]), Height: intFromAny(m["height"])}
	return vp, vp.Width > 0 && vp.Height > 0
}

func stringSliceFromAny(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s := strings.TrimSpace(fmt.Sprint(item)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func bboxFromAny(v any) (VisualBBox, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return VisualBBox{}, false
	}
	box := VisualBBox{
		X:      intFromAny(m["x"]),
		Y:      intFromAny(m["y"]),
		Width:  intFromAny(m["width"]),
		Height: intFromAny(m["height"]),
	}
	if box.Width <= 0 || box.Height <= 0 {
		return VisualBBox{}, false
	}
	return box, true
}

func boolFromAny(v any) bool {
	b, _ := v.(bool)
	return b
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}

func visualMaxPixels(scale string, requested int) int {
	if requested > 0 {
		return requested
	}
	switch strings.TrimSpace(scale) {
	case "native":
		return 0
	case "small":
		return 360000
	default:
		return 921600
	}
}

func pngDimensions(data []byte) (VisualViewport, error) {
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return VisualViewport{}, fmt.Errorf("decode png dimensions: %w", err)
	}
	return VisualViewport{Width: cfg.Width, Height: cfg.Height}, nil
}

func translateBoxes(boxes []VisualBBox, dx, dy int) []VisualBBox {
	out := make([]VisualBBox, 0, len(boxes))
	for _, box := range boxes {
		box.X += dx
		box.Y += dy
		out = append(out, box)
	}
	return out
}

func translateImageActions(actions []VisualImageAction, dx, dy int) []VisualImageAction {
	out := make([]VisualImageAction, 0, len(actions))
	for _, action := range actions {
		action.BBox.X += dx
		action.BBox.Y += dy
		out = append(out, action)
	}
	return out
}

func scaleBoxes(boxes []VisualBBox, factor float64) []VisualBBox {
	if factor == 1 {
		return boxes
	}
	out := make([]VisualBBox, 0, len(boxes))
	for _, box := range boxes {
		out = append(out, scaleBBox(box, factor))
	}
	return out
}

func scaleImageActions(actions []VisualImageAction, factor float64) []VisualImageAction {
	if factor == 1 {
		return actions
	}
	out := make([]VisualImageAction, 0, len(actions))
	for _, action := range actions {
		action.BBox = scaleBBox(action.BBox, factor)
		out = append(out, action)
	}
	return out
}

func scaleBBox(box VisualBBox, factor float64) VisualBBox {
	if factor == 1 {
		return box
	}
	return VisualBBox{
		X:      int(math.Round(float64(box.X) * factor)),
		Y:      int(math.Round(float64(box.Y) * factor)),
		Width:  maxInt(1, int(math.Round(float64(box.Width)*factor))),
		Height: maxInt(1, int(math.Round(float64(box.Height)*factor))),
	}
}

func filterImageActions(actions []VisualImageAction, width, height int) []VisualImageAction {
	out := make([]VisualImageAction, 0, len(actions))
	bounds := VisualBBox{Width: width, Height: height}
	for _, action := range actions {
		box, ok := intersectVisualBBox(action.BBox, bounds)
		if !ok {
			continue
		}
		action.BBox = box
		out = append(out, action)
	}
	return out
}

func intersectVisualBBox(box, bounds VisualBBox) (VisualBBox, bool) {
	x1 := maxInt(box.X, bounds.X)
	y1 := maxInt(box.Y, bounds.Y)
	x2 := minInt(box.X+box.Width, bounds.X+bounds.Width)
	y2 := minInt(box.Y+box.Height, bounds.Y+bounds.Height)
	if x2 <= x1 || y2 <= y1 {
		return VisualBBox{}, false
	}
	return VisualBBox{X: x1, Y: y1, Width: x2 - x1, Height: y2 - y1}, true
}

func cropPNG(data []byte, box VisualBBox) ([]byte, image.Rectangle, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, image.Rectangle{}, fmt.Errorf("decode png for crop: %w", err)
	}
	bounds := img.Bounds()
	rect := image.Rect(box.X, box.Y, box.X+box.Width, box.Y+box.Height).Intersect(bounds)
	if rect.Empty() {
		return data, image.Rectangle{}, nil
	}
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
	out, err := encodePNG(dst)
	return out, rect, err
}

func scalePNG(data []byte, maxPixels int) ([]byte, float64, error) {
	if maxPixels <= 0 {
		return data, 1, nil
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, fmt.Errorf("decode png for scale: %w", err)
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	pixels := w * h
	if pixels <= maxPixels {
		return data, 1, nil
	}
	factor := math.Sqrt(float64(maxPixels) / float64(pixels))
	nw := maxInt(1, int(math.Floor(float64(w)*factor)))
	nh := maxInt(1, int(math.Floor(float64(h)*factor)))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		sy := bounds.Min.Y + minInt(h-1, int(float64(y)/factor))
		for x := 0; x < nw; x++ {
			sx := bounds.Min.X + minInt(w-1, int(float64(x)/factor))
			dst.Set(x, y, img.At(sx, sy))
		}
	}
	out, err := encodePNG(dst)
	return out, factor, err
}

func overlayPNG(data []byte, boxes []VisualBBox) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode png for overlay: %w", err)
	}
	bounds := img.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, img, bounds.Min, draw.Src)
	for _, box := range boxes {
		drawBox(dst, image.Rect(box.X, box.Y, box.X+box.Width, box.Y+box.Height).Intersect(bounds), color.RGBA{R: 255, G: 64, B: 64, A: 255})
	}
	return encodePNG(dst)
}

func visualImageActionHandles(actions []VisualImageAction) []string {
	if len(actions) == 0 {
		return nil
	}
	out := make([]string, 0, len(actions))
	for _, action := range actions {
		out = append(out, action.Handle)
	}
	return out
}

func visualActionAtPoint(actions []VisualImageAction, point VisualPoint) (VisualImageAction, bool) {
	for _, action := range actions {
		if point.X >= action.BBox.X && point.X < action.BBox.X+action.BBox.Width &&
			point.Y >= action.BBox.Y && point.Y < action.BBox.Y+action.BBox.Height {
			return action, true
		}
	}
	return VisualImageAction{}, false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func drawBox(img *image.RGBA, rect image.Rectangle, c color.Color) {
	if rect.Empty() {
		return
	}
	for x := rect.Min.X; x < rect.Max.X; x++ {
		img.Set(x, rect.Min.Y, c)
		img.Set(x, rect.Max.Y-1, c)
	}
	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		img.Set(rect.Min.X, y, c)
		img.Set(rect.Max.X-1, y, c)
	}
}

func changedImageBBox(aPNG, bPNG []byte) *VisualBBox {
	a, errA := png.Decode(bytes.NewReader(aPNG))
	b, errB := png.Decode(bytes.NewReader(bPNG))
	if errA != nil || errB != nil {
		return nil
	}
	ab, bb := a.Bounds(), b.Bounds()
	if ab.Dx() != bb.Dx() || ab.Dy() != bb.Dy() {
		w, h := bb.Dx(), bb.Dy()
		if w < ab.Dx() {
			w = ab.Dx()
		}
		if h < ab.Dy() {
			h = ab.Dy()
		}
		return &VisualBBox{X: 0, Y: 0, Width: w, Height: h}
	}
	minX, minY, maxX, maxY := ab.Max.X, ab.Max.Y, ab.Min.X-1, ab.Min.Y-1
	for y := ab.Min.Y; y < ab.Max.Y; y++ {
		for x := ab.Min.X; x < ab.Max.X; x++ {
			ar, ag, abv, aa := a.At(x, y).RGBA()
			br, bg, bbv, ba := b.At(x, y).RGBA()
			if ar == br && ag == bg && abv == bbv && aa == ba {
				continue
			}
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x > maxX {
				maxX = x
			}
			if y > maxY {
				maxY = y
			}
		}
	}
	if maxX < minX || maxY < minY {
		return nil
	}
	return &VisualBBox{X: minX - ab.Min.X, Y: minY - ab.Min.Y, Width: maxX - minX + 1, Height: maxY - minY + 1}
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (srv *Server) storeVisualImage(vh *VisualHandle, region, overlay, scale string, png []byte, processed visualPNGResult) *VisualImage {
	sum := sha256.Sum256(png)
	hash := hex.EncodeToString(sum[:])
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	srv.nextVisualID++
	id := fmt.Sprintf("img%d", srv.nextVisualID)
	img := &VisualImage{
		ID:           id,
		VisualHandle: vh.ID,
		Kind:         vh.Kind,
		Handle:       vh.Handle,
		Region:       region,
		Overlay:      overlay,
		Scale:        scale,
		Bytes:        len(png),
		SHA256:       hash,
		Created:      time.Now().UTC(),
		PNG:          append([]byte(nil), png...),
		Width:        processed.Width,
		Height:       processed.Height,
		Original:     processed.Original,
		CropBBox:     cloneVisualBBox(processed.CropBBox),
		ScaleFactor:  processed.ScaleFactor,
		Actions:      append([]VisualImageAction(nil), processed.Actions...),
	}
	srv.visualImages[id] = img
	return img
}

func cloneVisualBBox(box *VisualBBox) *VisualBBox {
	if box == nil {
		return nil
	}
	cp := *box
	return &cp
}

func (srv *Server) resolveVisualImage(id string) (*VisualImage, *mcpsdk.CallToolResult) {
	if id == "" {
		return nil, buildToolError(ErrBadRequest, "visual.act: image_id is required")
	}
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	img := srv.visualImages[id]
	if img == nil {
		return nil, buildToolError(ErrUnknownHandle, fmt.Sprintf("unknown visual image %q", id))
	}
	cp := *img
	cp.PNG = append([]byte(nil), img.PNG...)
	cp.Actions = append([]VisualImageAction(nil), img.Actions...)
	cp.CropBBox = cloneVisualBBox(img.CropBBox)
	return &cp, nil
}

func (srv *Server) resolveVisualImages(fromID, toID string) (*VisualImage, *VisualImage, *mcpsdk.CallToolResult) {
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	from := srv.visualImages[fromID]
	to := srv.visualImages[toID]
	if from == nil {
		return nil, nil, buildToolError(ErrUnknownHandle, fmt.Sprintf("unknown visual image %q", fromID))
	}
	if to == nil {
		return nil, nil, buildToolError(ErrUnknownHandle, fmt.Sprintf("unknown visual image %q", toID))
	}
	fc := *from
	tc := *to
	fc.PNG = append([]byte(nil), from.PNG...)
	tc.PNG = append([]byte(nil), to.PNG...)
	fc.Actions = append([]VisualImageAction(nil), from.Actions...)
	tc.Actions = append([]VisualImageAction(nil), to.Actions...)
	fc.CropBBox = cloneVisualBBox(from.CropBBox)
	tc.CropBBox = cloneVisualBBox(to.CropBBox)
	return &fc, &tc, nil
}

func (srv *Server) startVisualRecording(vh *VisualHandle, mode string) string {
	now := time.Now().UTC()
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	srv.nextVisualID++
	id := fmt.Sprintf("rec%d", srv.nextVisualID)
	srv.visualRecords[id] = &VisualRecording{
		ID:           id,
		VisualHandle: vh.ID,
		Kind:         string(vh.Kind),
		Handle:       vh.Handle,
		Mode:         mode,
		Status:       "recording",
		StartedAt:    now.Format(time.RFC3339Nano),
		Events: []VisualRecordEvent{{
			At:   now.Format(time.RFC3339Nano),
			Type: "record.start",
			Data: map[string]any{"mode": mode},
		}},
	}
	return id
}

func (srv *Server) stopVisualRecording(id string) (*VisualRecording, []string, *mcpsdk.CallToolResult) {
	if id == "" {
		return nil, nil, buildToolError(ErrBadRequest, "visual.record stop: recording_id is required")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	srv.visualMu.Lock()
	rec := srv.visualRecords[id]
	if rec == nil {
		srv.visualMu.Unlock()
		return nil, nil, buildToolError(ErrUnknownHandle, fmt.Sprintf("unknown visual recording %q", id))
	}
	rec.Status = "stopped"
	rec.StoppedAt = now
	rec.Events = append(rec.Events, VisualRecordEvent{At: now, Type: "record.stop"})
	cp := *rec
	cp.Events = append([]VisualRecordEvent(nil), rec.Events...)
	srv.visualMu.Unlock()

	artifacts, err := srv.writeVisualRecordingArtifacts(&cp)
	if err != nil {
		return nil, nil, buildToolError(ErrBadRequest, fmt.Sprintf("visual.record: write artifacts: %v", err))
	}
	return &cp, artifacts, nil
}

func (srv *Server) resolveStoppedVisualRecording(id string) (*VisualRecording, *mcpsdk.CallToolResult) {
	if strings.TrimSpace(id) == "" {
		return nil, buildToolError(ErrBadRequest, "issue.create: visual recording id is required")
	}
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	rec := srv.visualRecords[id]
	if rec == nil {
		return nil, buildToolError(ErrUnknownHandle, fmt.Sprintf("unknown visual recording %q", id))
	}
	if rec.Status != "stopped" {
		return nil, buildToolError(ErrBadRequest, fmt.Sprintf("visual recording %q is not stopped", id))
	}
	cp := *rec
	cp.Events = append([]VisualRecordEvent(nil), rec.Events...)
	cp.RRWebJSON = append([]byte(nil), rec.RRWebJSON...)
	return &cp, nil
}

func (srv *Server) recordVisualEvent(visualHandle string, ev VisualRecordEvent) {
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	for _, rec := range srv.visualRecords {
		if rec.VisualHandle == visualHandle && rec.Status == "recording" {
			rec.Events = append(rec.Events, ev)
		}
	}
}

func (srv *Server) recordVisualRRWeb(visualHandle string, data []byte) {
	if len(data) == 0 {
		return
	}
	count := rrwebEventCount(data)
	srv.visualMu.Lock()
	defer srv.visualMu.Unlock()
	for _, rec := range srv.visualRecords {
		if rec.VisualHandle == visualHandle && rec.Status == "recording" {
			rec.RRWebJSON = append(rec.RRWebJSON[:0], data...)
			rec.RRWebEvents = count
		}
	}
}

func (srv *Server) writeVisualRecordingArtifacts(rec *VisualRecording) ([]string, error) {
	dir := filepath.Join(srv.visualArtifactsDir(), rec.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	timeline := filepath.Join(dir, "timeline.json")
	semantic := filepath.Join(dir, "capture.semantic.json")
	rrweb := filepath.Join(dir, "session.rrweb.json")
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(timeline, data, 0o644); err != nil {
		return nil, err
	}
	summary := map[string]any{
		"id":            rec.ID,
		"visual_handle": rec.VisualHandle,
		"kind":          rec.Kind,
		"handle":        rec.Handle,
		"mode":          rec.Mode,
		"status":        rec.Status,
		"event_count":   len(rec.Events),
		"rrweb_events":  rec.RRWebEvents,
		"started_at":    rec.StartedAt,
		"stopped_at":    rec.StoppedAt,
	}
	sdata, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(semantic, sdata, 0o644); err != nil {
		return nil, err
	}
	artifacts := []string{timeline, semantic}
	if len(rec.RRWebJSON) > 0 {
		if err := os.WriteFile(rrweb, rec.RRWebJSON, 0o644); err != nil {
			return nil, err
		}
		artifacts = append(artifacts, rrweb)
	}
	return artifacts, nil
}

func (srv *Server) visualArtifactsDir() string {
	if srv.artifactsDir != "" {
		return filepath.Join(srv.artifactsDir, "visual")
	}
	return filepath.Join(".artifacts", "visual")
}

func nonEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func rrwebEventCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	var env struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return 0
	}
	return len(env.Events)
}
