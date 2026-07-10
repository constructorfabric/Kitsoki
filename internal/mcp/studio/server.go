// Package studio implements the `kitsoki mcp` server — the studio facade an
// external coding agent attaches to in order to author a story, drive a session,
// and see the result. It is a sibling to internal/mcp (the per-app `transition`
// server): same official github.com/modelcontextprotocol/go-sdk v1.0.0 SDK, same
// StdioTransport, same AddTool/handler/structured-error shape — but its state is
// an authoring workspace and a set of live driving sessions (the handle model in
// handles.go), not one app's transitions.
//
// # Slice scope (the server core)
//
// This is the keystone of the facade: the server, the handle model, the tool
// registry, and the no-LLM default. It ships a trivial studio.ping/studio.handles
// pair plus a read-only studio.work queue so the transport, attach config, and
// handle lifecycle are verifiable
// end-to-end before the domain tools (story.* slice 6, session.*/render.* slice 7)
// plug into the same registry. No interpretive act (free-text routing, any live
// harness call) happens in the server core — those belong to a session handle's
// orchestrator + harness and are deferred to slice 7, replay-gated there.
//
// # No-LLM default
//
// Every driving handle is built with a replay harness unless the caller
// explicitly opts into harness:live (HarnessMode). The harness is constructed
// behind an injectable seam (HarnessBuilder) so a test can supply a failing live
// harness and assert a default-mode handle never reaches it.
//
// # Tool names
//
// Tools keep the dotted family.verb convention (studio.ping); the SDK exposes
// them to the client as mcp__kitsoki__studio.ping per the mcp__<server>__<tool>
// convention. The v1.0.0 SDK accepts a dot in the tool name at registration.
package studio

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"

	"kitsoki/internal/app"
	"kitsoki/internal/bugprivacy"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is the studio server's reported implementation version. It rides the
// MCP Implementation block and is echoed by studio.ping so a client can confirm
// which server build it attached to.
const Version = "0.0.1"

// implementationName is the MCP server name. Combined with a tool name it forms
// the mcp__kitsoki__<tool> identifier the client sees.
const implementationName = "kitsoki-studio"

// Server is the concrete studio MCP server wrapping the Go SDK. Construct with
// NewServer; serve with Run (stdio) or Connect (in-process testing). It owns one
// StudioSession — the in-memory handle store every tool operates on.
type Server struct {
	mcpSrv *mcpsdk.Server
	sess   *StudioSession
	// visualHandles are lightweight logical surfaces opened by visual.open.
	// They deliberately do not own a browser process in the first slice; they
	// bind a stable visual handle to an existing driving handle and reuse the
	// render/session seams for observation, snapshots, and deterministic acts.
	visualMu      sync.Mutex
	visualHandles map[string]*VisualHandle
	visualImages  map[string]*VisualImage
	visualRecords map[string]*VisualRecording
	nextVisualID  int
	// webShot is the injectable render.web seam (slice 4 webshot.Shot, wrapped
	// by cmd/kitsoki). Nil → render.web degrades to text (no browser host). A
	// test injects a stub that returns a synthetic PNG with no Chromium.
	webShot WebShotFunc
	// webShotResult is the richer visual.snapshot seam: it returns the PNG plus
	// the page-side compact semantic observation when the browser helper emits
	// one. Kept separate so existing render.web tests can inject only WebShotFunc.
	webShotResult WebShotResultFunc
	// webAct performs browser actions for web/vscode visual.act targets.
	webAct WebActFunc
	// readOnly drops the only story-tree mutation tool (story.write) from the
	// registry. The read tools (story.read/validate/graph/test), the session
	// driving tools (session.*, replay-default → no LLM, no story-file
	// mutation), and the render tools stay available. Used by the meta-mode
	// Q&A surface (`/meta story ask`), which must not edit the story.
	readOnly bool
	// issueFiler is the injectable issue.create GitHub seam: it files a composed
	// {repo, title, body, labels} GitHub issue when issueSink resolves to
	// "github". Nil only makes the github sink unavailable; local-artifact stays
	// usable. Production (cmd/kitsoki) routes through Kitsoki's native GitHub
	// issue filing; tests inject fakes. See WithIssueFiler.
	issueFiler IssueFiler
	// issueSink is the default filing destination for issue.create. Empty means
	// local-artifact, so local dogfood does not burn GitHub issues.
	issueSink string
	// artifactsDir is where issue.create writes rendered assets. Empty →
	// defaultIssueArtifactsDir. See WithArtifactsDir.
	artifactsDir string
	// bugPrivacyChecker gates issue.create before the IssueFiler is called. Nil
	// keeps deterministic scrubbing only.
	bugPrivacyChecker bugprivacy.Checker
	// importResolver is the loader/test resolver used for @kitsoki/<name>
	// imports. It is the MCP twin of the CLI's buildImportResolver seam.
	importResolver app.ImportResolver
}

// ServerOption configures a studio Server at construction.
type ServerOption func(*Server)

// ReadOnly omits the story-mutating tool (story.write) from the registry. Read
// tools and replay-default session driving stay available — read-only here means
// "cannot edit the story tree", not "cannot run the story". See Server.readOnly.
func ReadOnly() ServerOption { return func(s *Server) { s.readOnly = true } }

// WithImportResolver threads the same import-resolution seam used by the CLI
// into story.* and session.* tools. Nil keeps the loader's legacy behaviour.
func WithImportResolver(resolver app.ImportResolver) ServerOption {
	return func(s *Server) { s.importResolver = resolver }
}

// WithBugPrivacyChecker enables the provider-backed issue.create privacy gate.
func WithBugPrivacyChecker(checker bugprivacy.Checker) ServerOption {
	return func(s *Server) { s.bugPrivacyChecker = checker }
}

// NewServer constructs a studio Server over the given StudioSession and registers
// the studio.ping / studio.handles tools. Pass a session built with NewStudioSession
// (or one seeded with an initial workspace). The server is ready to call Run or
// Connect. Pass ReadOnly() to omit story.write (the Q&A surface).
func NewServer(sess *StudioSession, opts ...ServerOption) *Server {
	srv := &Server{
		sess:          sess,
		visualHandles: make(map[string]*VisualHandle),
		visualImages:  make(map[string]*VisualImage),
		visualRecords: make(map[string]*VisualRecording),
	}
	for _, opt := range opts {
		opt(srv)
	}
	srv.mcpSrv = mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    implementationName,
		Version: Version,
	}, nil)

	// studio.ping — liveness; proves transport + attach.
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "studio.ping",
		Description: "Liveness and identity probe for the kitsoki studio server. Returns {ok, version, revision?, modified?, go_version?, executable?, working_dir?}; compare revision/working_dir with the checkout before trusting workflow generation.",
	}, srv.handlePing)

	// studio.handles — lists the open handles and their modes.
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "studio.handles",
		Description: "List the open studio handles: the driving sessions (id, harness mode, trace path) and the authoring workspace (if one is bound).",
	}, srv.handleHandles)

	// studio.work — global async/reacquisition queue across open handles.
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "studio.work",
		Description: "Read-only prioritized work queue across all open driving handles. Returns async jobs, unread notifications, pending/dispatching/failed chat drives, backgrounded chats, and parked operator questions with reacquisition hints.",
	}, srv.handleWork)

	// story.* — the deterministic, LLM-free authoring tools (slice 6).
	srv.registerStoryTools()

	// story.list / story.search — discover and grep a story's files (the
	// read/grep surface that lets a driver stay off the host Read/Grep).
	srv.registerStorySearchTools()

	// story.turn — dry-run one transition (the debugging microscope).
	srv.registerStoryTurnTool()

	// workflow.* — dynamic-workflow create/validate/export receipts.
	srv.registerWorkflowTools()

	// session.* / render.* — drive a live (replay-default) session and see it
	// (slice 7).
	srv.registerSessionTools()

	// chat.* — read-side drill-down for async chat/subagent context.
	srv.registerChatTools()

	// inbox.* — external intake into the per-session inbox.
	srv.registerInboxTools()

	// issue.* — file a local artifact ticket or GitHub issue with studio-produced
	// evidence bundled in.
	srv.registerIssueTools()

	// host.* — the standalone gate-runner: run a command against a worktree
	// (e.g. go test) outside any live session, to gate on the real deliverable.
	srv.registerHostTools()

	// trace.* — read a session trace OFF DISK (no live handle) and convert one
	// to a replayable flow fixture (the no-LLM test loop, closed in MCP).
	srv.registerTraceTools()

	// vcs.* / worktree.* — a structured git surface: status/diff/log, worktree
	// lifecycle, and the guarded squash-merge that replaces the main-destroying
	// `reset --soft` ritual.
	srv.registerVCSTools()

	// gh.* — read GitHub issues/PRs and post comments from inside the studio.
	srv.registerGHTools()

	// ticket.call — reusable Starlark ticket-provider modules.
	srv.registerTicketProviderTools()

	// visual.* — token-efficient visual interaction over web/TUI/VS Code-like
	// surfaces, built on existing session/render seams.
	srv.registerVisualTools()

	return srv
}

// Session exposes the underlying StudioSession so callers and tests can open or
// inspect handles directly (the domain tools in slices 6/7 dispatch through it).
func (srv *Server) Session() *StudioSession { return srv.sess }

// SetWebShot injects the render.web seam: the function that rasterises a studio
// web render spec to a PNG. The production wiring (cmd/kitsoki) builds it over
// the slice-4 webshot.Shot with a real HandlerServer + NodeInvoker; a test
// injects a stub returning a synthetic PNG with no browser. When unset,
// render.web degrades to a text-only "needs a browser-capable host" result.
func (srv *Server) SetWebShot(fn WebShotFunc) { srv.webShot = fn }

// SetWebShotResult injects the richer web renderer used by visual.snapshot. It
// also keeps render.web available by adapting the result back to PNG bytes.
func (srv *Server) SetWebShotResult(fn WebShotResultFunc) {
	srv.webShotResult = fn
	if fn == nil {
		srv.webShot = nil
		return
	}
	srv.webShot = func(ctx context.Context, spec WebRenderSpec) ([]byte, error) {
		res, err := fn(ctx, spec)
		return res.PNG, err
	}
}

// SetWebAct injects the browser action seam used by visual.act for web/vscode
// handles.
func (srv *Server) SetWebAct(fn WebActFunc) { srv.webAct = fn }

// Run starts the studio server on the StdioTransport and blocks until the context
// is done or the peer disconnects. This is the entry point for `kitsoki mcp`.
func (srv *Server) Run(ctx context.Context) error {
	return srv.mcpSrv.Run(ctx, &mcpsdk.StdioTransport{})
}

// Connect exposes the underlying mcpsdk.Server so callers can use
// mcpsdk.NewInMemoryTransports for in-process testing (mirrors mcp.Server.Connect).
func (srv *Server) Connect(ctx context.Context, t mcpsdk.Transport, opts *mcpsdk.ServerSessionOptions) (*mcpsdk.ServerSession, error) {
	return srv.mcpSrv.Connect(ctx, t, opts)
}

// ── tool args / results ──────────────────────────────────────────────────────

// PingArgs is the (empty) input to studio.ping.
type PingArgs struct{}

// PingOK is the success response of studio.ping. JSON tags are load-bearing
// (read by the client).
type PingOK struct {
	OK         bool   `json:"ok"`                    // always true on this branch
	Version    string `json:"version"`               // the studio server version (== Version)
	Revision   string `json:"revision,omitempty"`    // vcs.revision from Go build info, when available
	Modified   string `json:"modified,omitempty"`    // vcs.modified from Go build info, when available
	GoVersion  string `json:"go_version,omitempty"`  // Go toolchain version from build info
	Executable string `json:"executable,omitempty"`  // resolved process path, useful when an MCP client is stale
	WorkingDir string `json:"working_dir,omitempty"` // process cwd, useful when an MCP client is pointed at another checkout
	Checkout   string `json:"checkout,omitempty"`    // current working tree HEAD, used to detect stale attached servers
	Stale      bool   `json:"stale,omitempty"`       // true when revision differs from checkout
	ReloadHint string `json:"reload_hint,omitempty"` // operator-facing reconnect guidance when stale
}

// HandlesArgs is the (empty) input to studio.handles.
type HandlesArgs struct{}

// ── handlers ──────────────────────────────────────────────────────────────────

// handlePing returns server identity. It never errors — its job is to prove the
// transport and attach config resolved to this server, and to make stale MCP
// attachments obvious before workflow generation spends time on the wrong code.
func (srv *Server) handlePing(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args PingArgs,
) (*mcpsdk.CallToolResult, any, error) {
	return nil, buildPingOK(), nil
}

func buildPingOK() PingOK {
	ok := PingOK{OK: true, Version: Version}
	if info, available := debug.ReadBuildInfo(); available {
		ok.GoVersion = info.GoVersion
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				ok.Revision = setting.Value
			case "vcs.modified":
				ok.Modified = setting.Value
			}
		}
	}
	if exe, err := os.Executable(); err == nil {
		ok.Executable = exe
	}
	if wd, err := os.Getwd(); err == nil {
		ok.WorkingDir = wd
		ok.Checkout = pingGitOutput(wd, "rev-parse", "HEAD")
		if ok.Revision == "" {
			ok.Revision = ok.Checkout
		}
		if ok.Modified == "" {
			if status := pingGitOutput(wd, "status", "--porcelain"); status != "" {
				ok.Modified = "true"
			} else if ok.Revision != "" {
				ok.Modified = "false"
			}
		}
		if ok.Revision != "" && ok.Checkout != "" && ok.Revision != ok.Checkout {
			ok.Stale = true
			ok.ReloadHint = "Restart or reconnect the Kitsoki Studio MCP server for this checkout before calling workflow.create or workflow.launch."
		}
	}
	return ok
}

func pingGitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// handleHandles snapshots the open handles. It never errors — an empty studio
// session reports zero sessions and no workspace.
func (srv *Server) handleHandles(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args HandlesArgs,
) (*mcpsdk.CallToolResult, any, error) {
	return nil, srv.sess.Snapshot(), nil
}

// ── structured tool errors ───────────────────────────────────────────────────

// ToolError is the structured error payload returned when a studio tool rejects
// a call (an unknown handle, no workspace bound, etc.). It mirrors
// internal/mcp.TransitionError: ok:false plus a typed error so the client can
// self-correct rather than parse a free-text message.
type ToolError struct {
	OK    bool   `json:"ok"`    // always false on this branch
	Code  string `json:"code"`  // machine-readable error code (see the Err* codes)
	Error string `json:"error"` // human-readable message
}

// Studio tool-error codes. Stable strings the client can branch on.
const (
	// ErrUnknownHandle — a tool named a session handle that is not open.
	ErrUnknownHandle = "UNKNOWN_HANDLE"
	// ErrNoWorkspace — a story.* tool was called with no workspace bound.
	ErrNoWorkspace = "NO_WORKSPACE"
	// ErrWorkspaceExists — open requested a workspace while one is already bound.
	ErrWorkspaceExists = "WORKSPACE_EXISTS"
	// ErrBadRequest — the arguments were malformed (e.g. empty required field).
	ErrBadRequest = "BAD_REQUEST"
	// ErrHarness — the session's harness could not be constructed.
	ErrHarness = "HARNESS"
	// ErrIssueUnavailable — issue.create was called with sink=github on a studio
	// with no issue filer wired (started without GitHub filing).
	ErrIssueUnavailable = "ISSUE_UNAVAILABLE"
)

// buildToolError wraps a code + message into a CallToolResult with IsError=true.
// The structured JSON payload rides in Content so the client can branch on Code.
// Mirrors internal/mcp.buildErrorResult. Handlers return
// `return buildToolError(code, msg), nil, nil` — a rejected call is a normal
// interpreted outcome, not a transport error.
func buildToolError(code, msg string) *mcpsdk.CallToolResult {
	payload := ToolError{OK: false, Code: code, Error: msg}
	b, _ := json.Marshal(payload)
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(b)},
		},
	}
}
