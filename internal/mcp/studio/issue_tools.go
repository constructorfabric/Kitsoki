// issue_tools.go — the issue.* family: file a GitHub issue natively from the
// studio, bundling the evidence the studio already produces.
//
// The kitsoki-mcp-driver agent develops/tests kitsoki through this MCP with no
// shell and no write tools. When the MCP surface itself can't do something the
// agent needs, that gap must be filed — reproducibly. issue.create does the
// three things the agent can't do itself, all server-side so the agent never
// handles bytes:
//
//   - assets — render the requested screens (tui_png / web / tui_text) via the
//     same composeRenderFrame / shot / webShot seams render.* use, write them
//     under the artifacts dir, and reference them in the body BY RELATIVE PATH.
//     (Stopgap: paths are not uploaded yet — an HTML comment marks that. The
//     IssueResult carries the asset list so a later upload pass is localized.)
//   - context — given a driving handle, bundle the session trace (the same tail
//     session.trace returns) and the inspect snapshot into the body.
//   - file — hand {repo, title, body, labels} to the injected IssueFiler seam
//     (production: gh; tests: a fake). source-autonomous is always added so an
//     agent-filed issue is identifiable.
//
// The filing seam is injected (WithIssueFiler) so tests never touch the network
// or an LLM, mirroring the webShot / HarnessBuilder seams. issue.create is
// allowed in --read-only mode: it mutates the artifacts dir and GitHub, not the
// story tree.
package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/tui/blocks"
	"kitsoki/internal/tui/shot"
)

// defaultIssueArtifactsDir is where issue.create writes rendered assets when no
// artifacts dir is injected. Repo-relative so the referenced paths in the issue
// body resolve from the repo root (the studio's cwd when launched there).
const defaultIssueArtifactsDir = ".artifacts/mcp-issues"

// defaultTraceLimit caps how many trailing trace events issue.create bundles
// when include_trace is set and trace_limit is omitted.
const defaultTraceLimit = 50

// autonomousLabel marks an issue as filed by an autonomous agent. issue.create
// always applies it (first), so agent-originated reports are filterable.
const autonomousLabel = "source-autonomous"

// IssueRequest is what the IssueFiler seam receives: the composed issue, ready
// to file. Repo empty means "let the filer resolve it" (gh uses the cwd remote).
type IssueRequest struct {
	Repo   string
	Title  string
	Body   string
	Labels []string
}

// IssueResult is what the IssueFiler seam returns: the created issue's URL and
// number.
type IssueResult struct {
	URL    string
	Number int
}

// IssueFiler is the injectable issue-creation seam. Production (cmd/kitsoki)
// shells to `gh issue create`; a test injects a fake that records the request
// and returns a canned result — no network, no LLM. Nil → issue.create returns
// ErrIssueUnavailable.
type IssueFiler func(ctx context.Context, req IssueRequest) (IssueResult, error)

// WithIssueFiler injects the issue-creation seam. Without it, issue.create is
// still registered but returns ErrIssueUnavailable (assets render, but filing is
// the point of the tool).
func WithIssueFiler(f IssueFiler) ServerOption {
	return func(s *Server) { s.issueFiler = f }
}

// WithArtifactsDir overrides where issue.create writes rendered assets (default
// defaultIssueArtifactsDir). A test points it at a temp dir.
func WithArtifactsDir(dir string) ServerOption {
	return func(s *Server) { s.artifactsDir = dir }
}

// registerIssueTools wires the issue.* tools onto the server. Called from
// NewServer after the session/render tools.
func (srv *Server) registerIssueTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name: "issue.create",
		Description: "File a GitHub issue natively from the studio, bundling evidence the studio produces. " +
			"{title, body?, labels?, repo?, handle?, include_trace?, trace_limit?, include_inspect?, assets?}. " +
			"Renders any requested assets (kind: tui_png|web|tui_text) to the artifacts dir and references them by relative path; " +
			"optionally bundles a handle's trace + inspect snapshot into the body; always adds the source-autonomous label. " +
			"Returns {ok, url, number, labels[], assets[]}.",
	}, srv.handleIssueCreate)
}

// IssueAssetSpec is one rendered attachment for issue.create. The render target
// is either a handle (defaults to the top-level handle) or an explicit
// {story_path, state, world?} spec — the same targeting render.* accepts.
type IssueAssetSpec struct {
	// Kind selects the renderer: "tui_png" (default), "web", or "tui_text".
	Kind    string `json:"kind,omitempty"`
	Caption string `json:"caption,omitempty"`
	// Name is the filename stem (no extension); defaults to "<kind>-<n>".
	Name string `json:"name,omitempty"`

	Handle    string         `json:"handle,omitempty"`
	StoryPath string         `json:"story_path,omitempty"`
	State     string         `json:"state,omitempty"`
	World     map[string]any `json:"world,omitempty"`
	Cols      int            `json:"cols,omitempty"`
	Rows      int            `json:"rows,omitempty"`
	Theme     string         `json:"theme,omitempty"`
}

// IssueCreateArgs is the input to issue.create.
type IssueCreateArgs struct {
	Title  string   `json:"title"`
	Body   string   `json:"body,omitempty"`
	Labels []string `json:"labels,omitempty"`
	Repo   string   `json:"repo,omitempty"`

	// Handle is a driving session whose evidence to bundle (and the default
	// render target for assets that name neither a handle nor a spec).
	Handle         string `json:"handle,omitempty"`
	IncludeTrace   bool   `json:"include_trace,omitempty"`
	TraceLimit     int    `json:"trace_limit,omitempty"`
	IncludeInspect bool   `json:"include_inspect,omitempty"`

	Assets []IssueAssetSpec `json:"assets,omitempty"`
}

// IssueCreateResult is the issue.create success payload.
type IssueCreateResult struct {
	OK     bool     `json:"ok"`
	URL    string   `json:"url"`
	Number int      `json:"number"`
	Labels []string `json:"labels"` // the final labels applied (source-autonomous first)
	Assets []string `json:"assets"` // relative paths of the rendered assets written
}

func (srv *Server) handleIssueCreate(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args IssueCreateArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.Title) == "" {
		return buildToolError(ErrBadRequest, "issue.create: title is required"), nil, nil
	}
	if srv.issueFiler == nil {
		return buildToolError(ErrIssueUnavailable,
			"issue.create: no issue filer wired (this studio was started without GitHub filing)"), nil, nil
	}

	// 1. Render + persist assets, building their markdown references.
	dir := filepath.Join(srv.resolveArtifactsDir(), issueSlug(args.Title))
	var assetPaths, assetMD []string
	for i, a := range args.Assets {
		if a.Handle == "" && a.StoryPath == "" {
			a.Handle = args.Handle // default the render target to the bundled handle
		}
		data, ext, rerr := srv.renderAsset(ctx, a)
		if rerr != nil {
			return rerr, nil, nil
		}
		name := a.Name
		if name == "" {
			name = fmt.Sprintf("%s-%d", assetKind(a.Kind), i+1)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: mkdir %q: %v", dir, err)), nil, nil
		}
		path := filepath.Join(dir, name+ext)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: write asset %q: %v", path, err)), nil, nil
		}
		assetPaths = append(assetPaths, path)
		caption := a.Caption
		if caption == "" {
			caption = name
		}
		if ext == ".png" {
			assetMD = append(assetMD, fmt.Sprintf("![%s](%s)", caption, path))
		} else {
			assetMD = append(assetMD, fmt.Sprintf("- [%s](%s)", caption, path))
		}
	}

	// 2. Bundle the handle's evidence (trace / inspect) into the body.
	contextMD, rerr := srv.issueContext(ctx, args)
	if rerr != nil {
		return rerr, nil, nil
	}

	// 3. Compose the body and file it.
	body := composeIssueBody(args.Body, contextMD, assetMD)
	labels := withAutonomousLabel(args.Labels)
	res, err := srv.issueFiler(ctx, IssueRequest{
		Repo:   args.Repo,
		Title:  args.Title,
		Body:   body,
		Labels: labels,
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: file issue: %v", err)), nil, nil
	}

	return nil, IssueCreateResult{
		OK:     true,
		URL:    res.URL,
		Number: res.Number,
		Labels: labels,
		Assets: assetPaths,
	}, nil
}

// renderAsset renders one asset spec to bytes + file extension, reusing the
// render.* seams (composeRenderFrame / shot.RenderPNG / webShot) so an issue
// asset can never drift from what render.tui_png / render.web produce.
func (srv *Server) renderAsset(ctx context.Context, a IssueAssetSpec) ([]byte, string, *mcpsdk.CallToolResult) {
	ra := RenderArgs{
		Handle:    a.Handle,
		StoryPath: a.StoryPath,
		State:     a.State,
		World:     a.World,
		Cols:      a.Cols,
		Rows:      a.Rows,
		Theme:     a.Theme,
	}
	switch assetKind(a.Kind) {
	case "tui_png":
		cols, rows := geometry(ra.Cols, ra.Rows)
		frame, rerr := srv.composeRenderFrame(ctx, ra, cols, rows)
		if rerr != nil {
			return nil, "", rerr
		}
		var buf bytes.Buffer
		if err := shot.RenderPNG(&buf, frame.ANSI, shot.Options{Theme: blocks.ThemeByName(a.Theme), Cols: cols, Rows: rows}); err != nil {
			return nil, "", buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: rasterise asset: %v", err))
		}
		return buf.Bytes(), ".png", nil
	case "tui_text":
		cols, rows := geometry(ra.Cols, ra.Rows)
		frame, rerr := srv.composeRenderFrame(ctx, ra, cols, rows)
		if rerr != nil {
			return nil, "", rerr
		}
		return []byte(frame.Text), ".txt", nil
	case "web":
		if srv.webShot == nil {
			return nil, "", buildToolError(ErrBadRequest,
				"issue.create: web asset needs a browser-capable host (none attached)")
		}
		spec, rerr := srv.webSpec(ra)
		if rerr != nil {
			return nil, "", rerr
		}
		png, err := srv.webShot(ctx, spec)
		if err != nil {
			return nil, "", buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: web asset: %v", err))
		}
		return png, ".png", nil
	default:
		return nil, "", buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: unknown asset kind %q", a.Kind))
	}
}

// issueContext bundles a driving handle's trace + inspect snapshot into a
// markdown block. Returns "" when no handle / nothing requested. Reuses the same
// runtime reads session.inspect / session.trace use, so the bundled evidence
// matches those tools exactly.
func (srv *Server) issueContext(ctx context.Context, args IssueCreateArgs) (string, *mcpsdk.CallToolResult) {
	if args.Handle == "" || (!args.IncludeTrace && !args.IncludeInspect) {
		return "", nil
	}
	rt, rerr := srv.resolveRuntime(args.Handle)
	if rerr != nil {
		return "", rerr
	}
	var b strings.Builder
	if args.IncludeInspect {
		if out, err := rt.inspect(ctx, 5, args.Handle); err == nil {
			fmt.Fprintf(&b, "\n\n## Context — session `%s` @ `%s`\n", args.Handle, out.State)
			if len(out.AllowedIntents) > 0 {
				fmt.Fprintf(&b, "- allowed intents: %s\n", strings.Join(out.AllowedIntents, ", "))
			}
			if w, err := json.MarshalIndent(out.World, "", "  "); err == nil {
				fmt.Fprintf(&b, "- world:\n```json\n%s\n```\n", string(w))
			}
		}
	}
	if args.IncludeTrace {
		limit := args.TraceLimit
		if limit <= 0 {
			limit = defaultTraceLimit
		}
		events := rt.history()
		if len(events) > limit {
			events = events[len(events)-limit:]
		}
		fmt.Fprintf(&b, "\n## Trace (last %d events)\n```\n", len(events))
		for _, ev := range events {
			line, _ := json.Marshal(ev)
			b.Write(line)
			b.WriteByte('\n')
		}
		b.WriteString("```\n")
	}
	return b.String(), nil
}

// composeIssueBody assembles the final issue body: the agent's narrative, then
// the bundled context, then an Assets section referencing the rendered files by
// relative path (a stopgap until asset upload lands — flagged with an HTML
// comment).
func composeIssueBody(body, contextMD string, assetMD []string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(body, "\n"))
	if contextMD != "" {
		b.WriteString(contextMD)
	}
	if len(assetMD) > 0 {
		b.WriteString("\n\n## Assets\n")
		b.WriteString("<!-- stopgap: repo-relative paths; asset upload lands later -->\n")
		b.WriteString(strings.Join(assetMD, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

// resolveArtifactsDir is the configured artifacts dir or the default.
func (srv *Server) resolveArtifactsDir() string {
	if srv.artifactsDir != "" {
		return srv.artifactsDir
	}
	return defaultIssueArtifactsDir
}

// assetKind normalises an asset kind, defaulting empty to "tui_png".
func assetKind(kind string) string {
	if kind == "" {
		return "tui_png"
	}
	return kind
}

// withAutonomousLabel returns the labels with source-autonomous prepended and
// duplicates removed, so an agent-filed issue is always identifiable.
func withAutonomousLabel(labels []string) []string {
	out := make([]string, 0, len(labels)+1)
	seen := map[string]bool{}
	add := func(l string) {
		l = strings.TrimSpace(l)
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	add(autonomousLabel)
	for _, l := range labels {
		add(l)
	}
	return out
}

// issueSlug derives a filesystem-safe directory stem from an issue title:
// lowercase, non-alphanumeric runs collapsed to a single '-', trimmed, capped.
func issueSlug(title string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			prevDash = false
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	slug := strings.Trim(b.String(), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if slug == "" {
		slug = "issue"
	}
	return slug
}
