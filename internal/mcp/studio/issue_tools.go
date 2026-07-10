// issue_tools.go — the issue.* family: file a local artifact issue or GitHub
// issue natively from the studio, bundling the evidence the studio already
// produces.
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
//   - context — infer the current driving handle when possible, then bundle a
//     redacted session trace and inspect snapshot into the body by default.
//   - file — by default write the report as a local artifact ticket under
//     .artifacts/issues/bugs; when requested, hand {repo, root, title, body, labels}
//     to the injected IssueFiler seam (production: Kitsoki's native GitHub
//     filer; tests: a fake).
//     source-autonomous is always added so an agent-filed issue is identifiable.
//
// The filing seam is injected (WithIssueFiler) so tests never touch the network
// or an LLM, mirroring the webShot / HarnessBuilder seams. issue.create is
// allowed in --read-only mode: it mutates the artifacts dir and, only for the
// github sink, GitHub; it never edits the story tree.
package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/bugfile"
	"kitsoki/internal/bugprivacy"
	"kitsoki/internal/bugreport"
	"kitsoki/internal/reportmeta"
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

const (
	// IssueSinkLocalArtifact writes issue.create reports under
	// .artifacts/issues/bugs (or the configured artifacts root in tests).
	IssueSinkLocalArtifact = "local-artifact"
	// IssueSinkGitHub files issue.create reports through the injected IssueFiler.
	IssueSinkGitHub = "github"
)

// IssueRequest is what the IssueFiler seam receives: the composed issue, ready
// to file. Repo empty means "let the filer resolve it"; Root gives the filer a
// checkout to inspect for that inference.
type IssueRequest struct {
	Repo   string
	Root   string
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

// IssueFiler is the injectable GitHub issue-creation seam. Production
// (cmd/kitsoki) routes through Kitsoki's native GitHub issue filing; tests
// inject fakes that record the request and return canned results with no
// network or LLM. Nil → issue.create returns ErrIssueUnavailable when the
// requested sink is github.
type IssueFiler func(ctx context.Context, req IssueRequest) (IssueResult, error)

// WithIssueFiler injects the GitHub issue-creation seam. Without it,
// issue.create can still write local artifact tickets, but sink=github returns
// ErrIssueUnavailable.
func WithIssueFiler(f IssueFiler) ServerOption {
	return func(s *Server) { s.issueFiler = f }
}

// WithIssueSink sets the default issue.create sink. Empty keeps the production
// default: local-artifact.
func WithIssueSink(sink string) ServerOption {
	return func(s *Server) { s.issueSink = sink }
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
		Description: "File an issue natively from the studio, bundling evidence the studio produces. Defaults to a local artifact ticket under .artifacts/issues/bugs; pass sink=github to file through the native GitHub provider. " +
			"{title, body?, labels?, repo?, sink?, repo_root?, handle?, trace_ref?, trace_path?, trace_app?, trace_ticket?, include_trace?, trace_limit?, include_inspect?, include_visual_recordings?, assets?}. " +
			"When handle is omitted, the current live session is inferred when unambiguous unless a trace source is supplied; handle- and trace-backed reports include scrubbed trace and context evidence by default unless explicitly disabled. " +
			"Renders any requested assets (kind: tui_png|web|tui_text) to the artifacts dir and references them by relative path; " +
			"bundles a handle's redacted trace/inspect snapshot, a resolved on-disk trace, and stopped visual.record artifact bundles into the body; always adds the source-autonomous label. " +
			"Returns {ok, url?, number?, local_path?, filing_error?, labels[], assets[]}.",
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
	Sink   string   `json:"sink,omitempty"`
	// RepoRoot is an optional checkout path used only to infer Repo from a git
	// remote when Repo is omitted. When empty, issue.create infers it from the
	// active handle or bound workspace.
	RepoRoot string `json:"repo_root,omitempty"`

	// Handle is a driving session whose evidence to bundle (and the default
	// render target for assets that name neither a handle nor a spec).
	Handle string `json:"handle,omitempty"`
	// TraceRef resolves an on-disk trace to bundle. It accepts an explicit JSONL
	// path or the same kind of filename substring operators pass to `kitsoki
	// trace` after debugging a non-MCP TUI/session run.
	TraceRef string `json:"trace_ref,omitempty"`
	// TracePath is an explicit-path alias for TraceRef, kept separate so callers
	// can be unambiguous when they already found the JSONL file.
	TracePath string `json:"trace_path,omitempty"`
	// TraceApp restricts trace lookup to an app id. With no TraceRef/TracePath it
	// selects the newest matching trace.
	TraceApp string `json:"trace_app,omitempty"`
	// TraceTicket restricts trace lookup to traces whose world ticket_id matches.
	TraceTicket string `json:"trace_ticket,omitempty"`
	// IncludeTrace/IncludeInspect default to true for handle- and trace-backed
	// reports. Pass false explicitly to opt out of zero-friction evidence.
	IncludeTrace   *bool `json:"include_trace,omitempty"`
	TraceLimit     int   `json:"trace_limit,omitempty"`
	IncludeInspect *bool `json:"include_inspect,omitempty"`
	// IncludeVisualRecordings copies stopped visual.record artifact bundles into
	// this issue's artifact directory and links them in the body.
	IncludeVisualRecordings []string `json:"include_visual_recordings,omitempty"`

	Assets []IssueAssetSpec `json:"assets,omitempty"`
}

// IssueCreateResult is the issue.create success payload.
//
// LocalPath is set for the local-artifact sink. When the github sink's wired
// filer fails (auth, network, a label/triage wall the unlabelled-retry couldn't
// clear, …), the issue is NOT lost: the composed markdown is written to the
// artifacts dir and LocalPath + FilingError are set instead of URL/Number. OK is
// false because no remote issue was created; the caller can still recover the
// markdown from LocalPath.
type IssueCreateResult struct {
	OK          bool               `json:"ok"`
	URL         string             `json:"url"`
	Number      int                `json:"number"`
	Labels      []string           `json:"labels"`                 // the final labels applied (source-autonomous first)
	Assets      []string           `json:"assets"`                 // relative paths of the rendered assets written
	LocalPath   string             `json:"local_path,omitempty"`   // set for local-artifact and github fallback files
	FilingError string             `json:"filing_error,omitempty"` // the GitHub filing error that triggered the fallback
	Privacy     *bugprivacy.Result `json:"privacy,omitempty"`
}

func (srv *Server) handleIssueCreate(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args IssueCreateArgs,
) (*mcpsdk.CallToolResult, any, error) {
	if strings.TrimSpace(args.Title) == "" {
		return buildToolError(ErrBadRequest, "issue.create: title is required"), nil, nil
	}
	sink, sinkErr := srv.resolveIssueSink(args.Sink)
	if sinkErr != nil {
		return buildToolError(ErrBadRequest, "issue.create: "+sinkErr.Error()), nil, nil
	}
	if traceErr := validateIssueTraceArgs(args); traceErr != nil {
		return buildToolError(ErrBadRequest, "issue.create: "+traceErr.Error()), nil, nil
	}
	if args.Handle == "" && !issueHasTraceSource(args) {
		if sh, ok := srv.sess.CurrentDrivingSession(); ok {
			args.Handle = sh.Key
		}
	}

	// 1. Render + persist assets, building their markdown references.
	dir := srv.issueArtifactDir(args.Title)
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
	contextMD, rerr := srv.issueContext(ctx, args, dir)
	if rerr != nil {
		return rerr, nil, nil
	}
	visualPaths, visualMD, rerr := srv.issueVisualRecordings(dir, args.IncludeVisualRecordings)
	if rerr != nil {
		return rerr, nil, nil
	}
	assetPaths = append(assetPaths, visualPaths...)
	assetMD = append(assetMD, visualMD...)

	// 3. Compose the body and file it.
	body := reportmeta.AppendFence(composeIssueBody(args.Body, contextMD, assetMD), srv.issueRuntimeSnapshot(args))
	labels := withAutonomousLabel(args.Labels)
	privacyFollowUpRoot := filepath.Join(srv.resolveArtifactsDir(), "privacy-followups")
	safeReport, privacy, perr := bugprivacy.Check(ctx, srv.bugPrivacyChecker, bugprivacy.Report{
		Surface:       "mcp",
		Target:        "kitsoki",
		Title:         args.Title,
		Body:          body,
		Component:     "studio-mcp",
		ArtifactNames: assetPaths,
	}, bugreport.ScrubOptions(), privacyFollowUpRoot, "")
	if perr != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: privacy check: %v", perr)), nil, nil
	}
	if privacy.Blocked() {
		msg := privacy.Message
		if privacy.FollowUpPath != "" {
			msg += "; depersonalized follow-up filed at " + privacy.FollowUpPath
		}
		return buildToolError(ErrBadRequest, "issue.create: "+msg), nil, nil
	}
	args.Title = safeReport.Title
	body = safeReport.Body
	if sink == IssueSinkLocalArtifact {
		localPath, werr := srv.writeLocalArtifactIssue(args.Title, body, labels)
		if werr != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: write local artifact issue: %v", werr)), nil, nil
		}
		return nil, IssueCreateResult{
			OK:        true,
			Labels:    labels,
			Assets:    assetPaths,
			LocalPath: localPath,
			Privacy:   &privacy,
		}, nil
	}
	if srv.issueFiler == nil {
		return buildToolError(ErrIssueUnavailable,
			"issue.create: no issue filer wired (this studio was started without GitHub filing)"), nil, nil
	}
	res, err := srv.issueFiler(ctx, IssueRequest{
		Repo:   args.Repo,
		Root:   srv.issueRepoRoot(args),
		Title:  args.Title,
		Body:   body,
		Labels: labels,
	})
	if err != nil {
		// GitHub filing failed for some reason (auth, network, a triage wall the
		// filer's unlabelled-retry couldn't clear). Don't lose the finding: write
		// the composed issue to the artifacts dir so a human can recover and file
		// it by hand. Only a write failure here is fatal.
		localPath, werr := srv.writeIssueFallback(args.Title, body, labels, err)
		if werr != nil {
			return buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: file issue: %v (and fallback write failed: %v)", err, werr)), nil, nil
		}
		return nil, IssueCreateResult{
			OK:          false,
			Labels:      labels,
			Assets:      assetPaths,
			LocalPath:   localPath,
			FilingError: err.Error(),
			Privacy:     &privacy,
		}, nil
	}

	return nil, IssueCreateResult{
		OK:      true,
		URL:     res.URL,
		Number:  res.Number,
		Labels:  labels,
		Assets:  assetPaths,
		Privacy: &privacy,
	}, nil
}

func (srv *Server) issueRepoRoot(args IssueCreateArgs) string {
	if root := strings.TrimSpace(args.RepoRoot); root != "" {
		return root
	}
	if args.Handle != "" {
		if sh, err := srv.sess.ResolveSession(args.Handle); err == nil && sh.Runtime != nil && sh.Runtime.def != nil {
			if root := strings.TrimSpace(sh.Runtime.def.BaseDir); root != "" {
				return root
			}
		}
	}
	if wh, ok := srv.sess.Workspace(); ok {
		if wh.Def != nil && strings.TrimSpace(wh.Def.BaseDir) != "" {
			return wh.Def.BaseDir
		}
		dir, _ := splitWorkspacePath(wh.Dir)
		return dir
	}
	return ""
}

func (srv *Server) resolveIssueSink(requested string) (string, error) {
	sink := strings.TrimSpace(requested)
	if sink == "" {
		sink = strings.TrimSpace(srv.issueSink)
	}
	switch sink {
	case "", IssueSinkLocalArtifact, "local", "artifact", "artifacts":
		return IssueSinkLocalArtifact, nil
	case IssueSinkGitHub:
		return IssueSinkGitHub, nil
	default:
		return "", fmt.Errorf("sink must be local-artifact or github (got %q)", requested)
	}
}

func (srv *Server) issueRuntimeSnapshot(args IssueCreateArgs) reportmeta.Snapshot {
	var def *app.AppDef
	var root string
	if args.Handle != "" {
		if sh, err := srv.sess.ResolveSession(args.Handle); err == nil && sh.Runtime != nil {
			def = sh.Runtime.def
			if def != nil {
				root = def.BaseDir
			}
		}
	}
	if def == nil {
		if wh, ok := srv.sess.Workspace(); ok && wh.Def != nil {
			def = wh.Def
			root = wh.Dir
		}
	}
	snap := reportmeta.Capture(root, def)
	if def == nil && strings.TrimSpace(args.TraceApp) != "" {
		snap.Story.AppID = strings.TrimSpace(args.TraceApp)
	}
	return snap
}

// writeIssueFallback persists a composed issue to the artifacts dir when GitHub
// filing fails, so an agent-found gap is never silently dropped. It writes a
// self-contained markdown file (front-matter-ish header with the title, labels,
// and the filing error, then the composed body) under
// <artifactsDir>/unfiled/<slug>.md and returns its path.
func (srv *Server) writeIssueFallback(title, body string, labels []string, filingErr error) (string, error) {
	dir := filepath.Join(srv.resolveArtifactsDir(), "unfiled")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	path := filepath.Join(dir, issueSlug(title)+".md")
	var b strings.Builder
	b.WriteString("# " + title + "\n\n")
	b.WriteString("> **Unfiled** — GitHub filing failed; recover this and file by hand.\n")
	b.WriteString(">\n")
	b.WriteString("> filing_error: " + filingErr.Error() + "\n")
	if len(labels) > 0 {
		b.WriteString("> labels: " + strings.Join(labels, ", ") + "\n")
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("write %q: %w", path, err)
	}
	return path, nil
}

func (srv *Server) writeLocalArtifactIssue(title, body string, labels []string) (string, error) {
	root := srv.issueTicketRoot()
	_, rel, _, err := bugfile.Create(bugfile.CreateRequest{
		Target:    "kitsoki",
		Title:     title,
		Body:      body,
		Component: "studio-mcp",
		Labels:    labels,
		TargetDir: root,
		FiledBy:   "kitsoki-mcp",
		Warnf:     func(string, ...any) {},
	})
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(filepath.Join(root, rel)), nil
}

func (srv *Server) issueArtifactDir(title string) string {
	root := srv.resolveArtifactsDir()
	base := issueSlug(title)
	dir := filepath.Join(root, base)
	if _, err := os.Stat(dir); err != nil {
		return dir
	}
	for i := 2; ; i++ {
		candidate := filepath.Join(root, fmt.Sprintf("%s-%d", base, i))
		if _, err := os.Stat(candidate); err != nil {
			return candidate
		}
	}
}

func (srv *Server) issueTicketRoot() string {
	dir := filepath.Clean(srv.resolveArtifactsDir())
	if filepath.Base(dir) == "mcp-issues" {
		return filepath.Dir(dir)
	}
	return dir
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

func (srv *Server) issueVisualRecordings(issueDir string, ids []string) ([]string, []string, *mcpsdk.CallToolResult) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	outDir := filepath.Join(issueDir, "visual-recordings")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, nil, buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: mkdir visual recordings: %v", err))
	}
	var paths, md []string
	for _, id := range ids {
		rec, rerr := srv.resolveStoppedVisualRecording(id)
		if rerr != nil {
			return nil, nil, rerr
		}
		srcs, err := srv.writeVisualRecordingArtifacts(rec)
		if err != nil {
			return nil, nil, buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: visual recording %q: %v", id, err))
		}
		recDir := filepath.Join(outDir, rec.ID)
		if err := os.MkdirAll(recDir, 0o755); err != nil {
			return nil, nil, buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: mkdir visual recording %q: %v", id, err))
		}
		md = append(md, fmt.Sprintf("- Visual recording `%s` (%d events, %s)", rec.ID, len(rec.Events), rec.Kind))
		for _, src := range srcs {
			dst := filepath.Join(recDir, filepath.Base(src))
			if err := copyFile(dst, src); err != nil {
				return nil, nil, buildToolError(ErrBadRequest, fmt.Sprintf("issue.create: copy visual recording %q: %v", id, err))
			}
			paths = append(paths, dst)
			md = append(md, fmt.Sprintf("  - [%s](%s)", filepath.Base(dst), dst))
		}
	}
	return paths, md, nil
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// issueContext bundles a driving handle's trace + inspect snapshot into a
// markdown block. Returns "" when no handle / nothing requested. Reuses the same
// runtime reads session.inspect / session.trace use, so the bundled evidence
// matches those tools exactly.
func (srv *Server) issueContext(ctx context.Context, args IssueCreateArgs, sidecarDir string) (string, *mcpsdk.CallToolResult) {
	hasTraceSource := issueHasTraceSource(args)
	includeTrace := issueBoolDefault(args.IncludeTrace, args.Handle != "" || hasTraceSource)
	includeInspect := issueBoolDefault(args.IncludeInspect, args.Handle != "" || hasTraceSource)
	if args.Handle == "" && !hasTraceSource {
		return "", nil
	}

	// Bulky machine context (full world, full trace) is written to a sidecar file
	// under the issue's artifacts dir; the body embeds only a compact summary so
	// the GitHub issue (and the MCP result echoing it) stays readable.
	scrubOpts := bugreport.ScrubOptions()
	var b strings.Builder
	if args.Handle != "" && (includeTrace || includeInspect) {
		rt, rerr := srv.resolveRuntime(args.Handle)
		if rerr != nil {
			return "", rerr
		}
		if includeInspect {
			if out, err := rt.inspect(ctx, 5, args.Handle); err == nil {
				fmt.Fprintf(&b, "\n\n## Context — session `%s` @ `%s`\n", args.Handle, out.State)
				if len(out.AllowedIntents) > 0 {
					fmt.Fprintf(&b, "- allowed intents: %s\n", strings.Join(out.AllowedIntents, ", "))
				}
				// World can be large; write the pretty version to a sidecar and embed a
				// compact one-liner (with a key count) in the body.
				world := bugreport.DepersonalizedTraceValue("world", out.World, scrubOpts)
				if w, err := json.Marshal(world); err == nil {
					if pretty, perr := json.MarshalIndent(world, "", "  "); perr == nil {
						if path, werr := writeIssueSidecar(sidecarDir, "world.redacted.json", pretty); werr == nil {
							fmt.Fprintf(&b, "- world (%d keys, redacted full: [%s](%s)):\n```json\n%s\n```\n",
								len(out.World), filepath.Base(path), path, string(w))
						} else {
							fmt.Fprintf(&b, "- world (%d keys, redacted):\n```json\n%s\n```\n", len(out.World), string(w))
						}
					}
				}
			}
		}
		if includeTrace {
			limit := args.TraceLimit
			if limit <= 0 {
				limit = defaultTraceLimit
			}
			events, err := rt.Events()
			if err != nil {
				events = nil
			}
			if len(events) > limit {
				events = events[len(events)-limit:]
			}
			redacted := bugreport.DepersonalizedTraceJSONL(events, scrubOpts)
			sidecarRef := ""
			if path, werr := writeIssueSidecar(sidecarDir, "trace.redacted.jsonl", redacted); werr == nil {
				sidecarRef = fmt.Sprintf(" (full: [%s](%s))", filepath.Base(path), path)
			}
			lines := strings.Split(strings.TrimRight(string(redacted), "\n"), "\n")
			if len(lines) == 1 && lines[0] == "" {
				lines = nil
			}
			fmt.Fprintf(&b, "\n## Trace (last %d events, redacted)%s\n```\n", len(lines), sidecarRef)
			for _, line := range lines {
				b.WriteString(line)
				b.WriteByte('\n')
			}
			b.WriteString("```\n")
		}
	}
	if hasTraceSource {
		contextMD, rerr := srv.issueOnDiskTraceContext(args, sidecarDir, includeTrace, includeInspect, args.Handle != "")
		if rerr != nil {
			return "", rerr
		}
		b.WriteString(contextMD)
	}
	return b.String(), nil
}

func issueBoolDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

// writeIssueSidecar writes machine context too bulky for the issue body to a
// file under the issue's artifacts dir, returning its path for a link.
func writeIssueSidecar(dir, name string, data []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
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
