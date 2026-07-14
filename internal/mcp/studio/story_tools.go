package studio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/app"
	"kitsoki/internal/app/graph"
	"kitsoki/internal/kitstage"
	"kitsoki/internal/testrunner"
)

// story_tools.go — the five deterministic, LLM-free authoring tools the studio
// server exposes for an external agent to read / write / validate / inspect /
// test the story it is authoring (docs/proposals/mcp-authoring-tools.md, slice
// 6). Every tool wraps a shipped function:
//
//   - story.read / story.write  — workspace-scoped file IO (path-escape rejected).
//   - story.validate            — app.Load → []ValidationError (the same set
//                                 `kitsoki run` enforces).
//   - story.graph               — graph.RoomList / Detail / AgentContracts (the
//                                 exact computation behind the web /editor view).
//   - story.test                — testrunner.RunFlows (the no-LLM flow gate,
//                                 `kitsoki test flows` reached over MCP).
//
// None of these makes an interpretive (LLM) call: validate/graph/read are pure
// reads, write is a file write + re-validate, and test runs the replay/cassette
// flow harness. dir / path default to the bound workspace handle (slice 5).

// registerStoryTools wires the five story.* tools onto the server. Called from
// NewServer after the server-core tools so they share one registry.
func (srv *Server) registerStoryTools() {
	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "story.read",
		Description: "Read a workspace-scoped file (rooms/*.yaml, prompts, schemas, flows). {path} is relative to the bound workspace dir; absolute paths and `..` escapes outside the workspace are rejected.",
	}, srv.handleStoryRead)

	// story.write is the only story-tree mutation; a read-only server (the Q&A
	// surface) omits it so the agent physically cannot edit the story.
	if !srv.readOnly {
		mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
			Name:        "story.write",
			Description: "Write a workspace-scoped file, auto-validating when the workspace contains app.yaml. {path, content, validate?}; validate defaults to auto (true for story packages, false for plain workspaces). Returns {written, validated, validation?}. Writes are confined to the workspace dir (path escape rejected).",
		}, srv.handleStoryWrite)
	}

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "story.validate",
		Description: "Load and validate the story (app.Load). Returns {ok, errors[]} where each error is {file, line, column, message} — the exact load-time invariant set kitsoki run enforces. {dir?, staged?}: dir defaults to the bound workspace; staged=true resolves staged kit-update candidates for this call only.",
	}, srv.handleStoryValidate)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "story.graph",
		Description: "Inspect the story graph. {dir?, graph?, room?, agents?}: graph=true → renderer-neutral kitsoki.graph/v1 nodes/edges; room set → that room's detail; agents=true → that room's agent contracts; else the BFS room list. {dir?} defaults to the bound workspace.",
	}, srv.handleStoryGraph)

	mcpsdk.AddTool(srv.mcpSrv, &mcpsdk.Tool{
		Name:        "story.test",
		Description: "Run the story's deterministic flow fixtures (testrunner.RunFlows — no LLM, replay/cassette honored). {dir?, flows?, recording?, allow_missing_recording?, fail_fast?, verbose?, json?, trace_out?, detailed?, staged?}: flows overrides the default <dir>/flows/*.yaml glob; staged=true resolves staged kit-update candidates for this call only. Returns a per-fixture pass/fail report (per-fixture failure_count only; pass detailed=true for full failure strings).",
	}, srv.handleStoryTest)
}

// ── tool args / results ──────────────────────────────────────────────────────

// StoryReadArgs is the input to story.read.
type StoryReadArgs struct {
	// Path is the workspace-relative file to read (rooms/idle.yaml, prompts/x.md…).
	Path string `json:"path"`
	// Dir overrides the workspace dir the path is resolved against (optional;
	// defaults to the bound workspace handle).
	Dir string `json:"dir,omitempty"`
}

// StoryReadOK is the story.read success result.
type StoryReadOK struct {
	OK      bool   `json:"ok"`      // always true on this branch
	Path    string `json:"path"`    // the workspace-relative path that was read
	Content string `json:"content"` // the file contents
}

// StoryWriteArgs is the input to story.write.
type StoryWriteArgs struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Dir      string `json:"dir,omitempty"`
	Validate *bool  `json:"validate,omitempty"`
}

// StoryWriteOK is the story.write success result: the write landed and the
// story was re-validated in the same round-trip when Validated is true.
type StoryWriteOK struct {
	OK         bool             `json:"ok"`                   // always true on this branch (the write succeeded)
	Written    string           `json:"written"`              // the workspace-relative path written
	Validated  bool             `json:"validated"`            // true when app.Load was run after the write
	Validation *StoryValidateOK `json:"validation,omitempty"` // the post-write validation (same shape as story.validate)
}

// StoryValidateArgs is the input to story.validate.
type StoryValidateArgs struct {
	// Dir overrides the workspace dir to validate (optional; defaults to the
	// bound workspace handle). May be a story directory or an app.yaml path.
	Dir string `json:"dir,omitempty"`
	// Staged resolves kits with a staged update candidate (kitsoki kit
	// update) to the candidate's pinned tree FOR THIS CALL ONLY — the
	// long-lived server's resolver and environment are never mutated.
	// Kits without a staged entry resolve normally.
	Staged bool `json:"staged,omitempty"`
}

// StoryValidateOK is the story.validate result: the structured load-time
// invariant set. ok is true exactly when errors is empty.
type StoryValidateOK struct {
	OK     bool             `json:"ok"`     // true when the story loaded with no validation errors
	Errors []ValidationItem `json:"errors"` // one per load-time invariant violated (empty when ok)
}

// ValidationItem is one app.ValidationError projected to the wire. It mirrors
// app.ValidationError{File, Line, Column, Message} exactly so the agent gets the
// same File:Line:Message a human sees on `kitsoki run`.
type ValidationItem struct {
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
	Message string `json:"message"`
}

// StoryGraphArgs is the input to story.graph. The mode is selected by params:
// Graph=true → graph wire shape; Room set → room detail; Agents=true → agent
// contracts; else the room list.
type StoryGraphArgs struct {
	Dir    string `json:"dir,omitempty"`
	Room   string `json:"room,omitempty"`
	Agents bool   `json:"agents,omitempty"`
	Graph  bool   `json:"graph,omitempty"`
}

// StoryGraphOK is the story.graph result. Exactly one of Rooms / Detail /
// Agents is populated, per the selected mode; Mode names which.
type StoryGraphOK struct {
	OK     bool                  `json:"ok"`               // always true on this branch
	Mode   string                `json:"mode"`             // "rooms" | "detail" | "agents" | "graph"
	Rooms  []RoomSummaryItem     `json:"rooms,omitempty"`  // mode == rooms
	Detail *graph.RoomDetail     `json:"detail,omitempty"` // mode == detail
	Agents []graph.AgentContract `json:"agents,omitempty"` // mode == agents
	Graph  *graph.KitsokiGraph   `json:"graph,omitempty"`  // mode == graph
}

// RoomSummaryItem is the token-diet projection of graph.RoomSummary for the
// room-list mode. It deliberately drops the UI-only Distance field (BFS layout
// chrome for the web editor) which carries no signal for agent reasoning.
type RoomSummaryItem struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	HasAgent bool   `json:"has_agent"`
}

// projectRoomList strips graph.RoomSummary to the agent-facing RoomSummaryItem
// (no Distance). Order is preserved from graph.RoomList.
func projectRoomList(rooms []graph.RoomSummary) []RoomSummaryItem {
	out := make([]RoomSummaryItem, 0, len(rooms))
	for _, r := range rooms {
		out = append(out, RoomSummaryItem{ID: r.ID, Label: r.Label, HasAgent: r.HasAgent})
	}
	return out
}

// StoryTestArgs is the input to story.test.
type StoryTestArgs struct {
	Dir string `json:"dir,omitempty"`
	// Flows overrides the flow glob (default <dir>/flows/*.yaml).
	Flows string `json:"flows,omitempty"`
	// Recording overrides the recording path declared in fixture files.
	Recording string `json:"recording,omitempty"`
	// AllowMissingRecording treats recording misses as skips rather than failures.
	AllowMissingRecording bool `json:"allow_missing_recording,omitempty"`
	// FailFast stops after the first failing fixture.
	FailFast bool `json:"fail_fast,omitempty"`
	// Verbose enables per-turn verbose output in the underlying flow runner.
	Verbose bool `json:"verbose,omitempty"`
	// JSON writes the full JSON flow report to this path, matching CLI --json.
	JSON string `json:"json,omitempty"`
	// TraceOut writes the authoritative JSONL trace to this path, matching CLI
	// --trace-out. Intended for single-fixture reconstruction.
	TraceOut string `json:"trace_out,omitempty"`
	// Detailed includes the full per-turn failure strings in the result. When
	// false (default) only a per-fixture failure COUNT is returned, keeping the
	// MCP payload small; set true to see *why* a fixture failed.
	Detailed bool `json:"detailed,omitempty"`
	// Staged resolves kits with a staged update candidate (kitsoki kit
	// update) to the candidate's pinned tree FOR THIS CALL ONLY — the
	// long-lived server's resolver and environment are never mutated.
	// Kits without a staged entry resolve normally.
	Staged bool `json:"staged,omitempty"`
}

// StoryTestOK is the story.test result: the per-fixture pass/fail report.
type StoryTestOK struct {
	OK      bool             `json:"ok"`      // true when every (non-skipped) fixture passed
	Passed  int              `json:"passed"`  // number of fixtures that passed
	Failed  int              `json:"failed"`  // number of fixtures that failed
	Results []FlowResultItem `json:"results"` // one entry per flow file
}

// FlowResultItem is one flow file's result, projected from
// testrunner.FlowResult. By default only FailureCount is populated (token
// diet); the full per-turn Failures strings are included only when the caller
// passes detailed=true.
type FlowResultItem struct {
	File         string   `json:"file"`                    // the flow fixture path
	Passed       bool     `json:"passed"`                  // whether every turn passed
	Skipped      bool     `json:"skipped,omitempty"`       // recording-miss skip (when allow-missing set)
	FailureCount int      `json:"failure_count,omitempty"` // number of per-turn failures (always set)
	Failures     []string `json:"failures,omitempty"`      // per-turn failure messages (only when detailed=true)
}

// ── handlers ──────────────────────────────────────────────────────────────────

// handleStoryRead reads a workspace-scoped file. The path is resolved under the
// workspace story dir and confined there (no `..`/absolute escape).
func (srv *Server) handleStoryRead(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StoryReadArgs,
) (*mcpsdk.CallToolResult, any, error) {
	storyDir, _, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}
	abs, err := safeJoin(storyDir, args.Path)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("read %s: %v", args.Path, err)), nil, nil
	}
	return nil, StoryReadOK{OK: true, Path: args.Path, Content: string(b)}, nil
}

// handleStoryWrite writes a workspace-scoped file then re-validates the story in
// the same round-trip. The write is confined to the workspace story dir; an
// escaping path is rejected before any IO.
func (srv *Server) handleStoryWrite(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StoryWriteArgs,
) (*mcpsdk.CallToolResult, any, error) {
	storyDir, appPath, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}
	abs, err := safeJoin(storyDir, args.Path)
	if err != nil {
		return buildToolError(ErrBadRequest, err.Error()), nil, nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("mkdir for %s: %v", args.Path, err)), nil, nil
	}
	if err := os.WriteFile(abs, []byte(args.Content), 0o644); err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("write %s: %v", args.Path, err)), nil, nil
	}
	out := StoryWriteOK{OK: true, Written: args.Path}
	if shouldValidateStoryWrite(appPath, args.Validate) {
		validation := validateStory(appPath, srv.importResolver)
		out.Validated = true
		out.Validation = &validation
	}
	return nil, out, nil
}

// handleStoryValidate loads and validates the story, returning the structured
// invariant set.
func (srv *Server) handleStoryValidate(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StoryValidateArgs,
) (*mcpsdk.CallToolResult, any, error) {
	_, appPath, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}
	return nil, validateStory(appPath, srv.callResolver(args.Staged)), nil
}

// handleStoryGraph computes the story graph views. The mode is selected by the
// params, mirroring the web editor's dispatch for legacy modes, with graph=true
// returning the renderer-neutral Kitsoki graph API.
func (srv *Server) handleStoryGraph(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StoryGraphArgs,
) (*mcpsdk.CallToolResult, any, error) {
	_, appPath, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}
	a, err := loadApp(appPath, srv.importResolver)
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("load story: %v", err)), nil, nil
	}

	switch {
	case args.Graph:
		g := graph.RoomGraph(a, "story:"+appPath+"#rooms")
		return nil, StoryGraphOK{OK: true, Mode: "graph", Graph: &g}, nil
	case args.Room != "" && args.Agents:
		return nil, StoryGraphOK{OK: true, Mode: "agents", Agents: graph.AgentContracts(a, args.Room)}, nil
	case args.Agents:
		return buildToolError(ErrBadRequest, "story.graph: agents mode requires a 'room'"), nil, nil
	case args.Room != "":
		detail, ok := graph.Detail(a, args.Room, appPath)
		if !ok {
			return buildToolError(ErrBadRequest, "story.graph: unknown room: "+args.Room), nil, nil
		}
		return nil, StoryGraphOK{OK: true, Mode: "detail", Detail: &detail}, nil
	default:
		return nil, StoryGraphOK{OK: true, Mode: "rooms", Rooms: projectRoomList(graph.RoomList(a))}, nil
	}
}

// handleStoryTest runs the deterministic flow gate over the story's fixtures.
// No LLM: testrunner.RunFlows replays recordings / host cassettes the fixtures
// declare. The flow glob defaults to <storyDir>/flows/*.yaml.
func (srv *Server) handleStoryTest(
	ctx context.Context,
	req *mcpsdk.CallToolRequest,
	args StoryTestArgs,
) (*mcpsdk.CallToolResult, any, error) {
	storyDir, appPath, rerr := srv.resolveWorkspace(args.Dir)
	if rerr != nil {
		return rerr, nil, nil
	}
	glob, gerr := resolveFlowGlob(storyDir, args.Flows)
	if gerr != nil {
		return buildToolError(ErrBadRequest, "story.test: "+gerr.Error()), nil, nil
	}
	report, err := testrunner.RunFlows(ctx, appPath, glob, testrunner.FlowOptions{
		RecordingOverride:     args.Recording,
		AllowMissingRecording: args.AllowMissingRecording,
		FailFast:              args.FailFast,
		Verbose:               args.Verbose,
		JSONOut:               args.JSON,
		TracePath:             args.TraceOut,
		ImportResolver:        srv.callResolver(args.Staged),
	})
	if err != nil {
		return buildToolError(ErrBadRequest, fmt.Sprintf("run flows: %v", err)), nil, nil
	}
	return nil, projectFlowReport(report, args.Detailed), nil
}

// ── workspace resolution & path safety ────────────────────────────────────────

// resolveWorkspace resolves the (storyDir, appPath) a story.* tool operates on.
// override (the dir/path param) wins when set; otherwise the bound workspace
// handle's Dir is used. Either may be a story directory OR an app.yaml file:
//   - a *.yaml/*.yml path is the manifest; storyDir is its parent.
//   - any other path is a directory; appPath is <dir>/app.yaml.
//
// A missing workspace (no override and no bound handle) is ErrNoWorkspace.
func (srv *Server) resolveWorkspace(override string) (storyDir, appPath string, rerr *mcpsdk.CallToolResult) {
	base := override
	if base == "" {
		wh, ok := srv.sess.Workspace()
		if !ok {
			return "", "", buildToolError(ErrNoWorkspace, "no workspace bound; open one with --workspace or pass dir/path")
		}
		base = wh.Dir
	}
	dir, app := splitWorkspacePath(base)
	if st, err := os.Stat(app); err == nil && !st.IsDir() {
		return dir, app, nil
	}
	if suggestions := discoverStoryRoots(dir, 8); len(suggestions) > 0 {
		return "", "", buildToolError(ErrBadRequest, fmt.Sprintf("no app.yaml at %s; pass dir as a story root, for example: %s", app, strings.Join(suggestions, ", ")))
	}
	return dir, app, nil
}

func resolveFlowGlob(storyDir, flows string) (string, error) {
	if strings.TrimSpace(flows) == "" {
		return filepath.Join(storyDir, "flows", "*.yaml"), nil
	}
	parts := strings.Split(flows, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if filepath.IsAbs(part) {
			out = append(out, part)
			continue
		}
		joined, err := safeJoin(storyDir, part)
		if err != nil {
			return "", err
		}
		out = append(out, joined)
	}
	if len(out) == 0 {
		return "", errors.New("flows is empty")
	}
	return strings.Join(out, ","), nil
}

func discoverStoryRoots(base string, max int) []string {
	if max <= 0 {
		return nil
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return nil
	}
	if st, err := os.Stat(absBase); err != nil || !st.IsDir() {
		return nil
	}
	seen := map[string]bool{}
	var roots []string
	addRoot := func(root string) bool {
		if rel, relErr := filepath.Rel(absBase, root); relErr == nil && rel != "." {
			root = rel
		}
		root = filepath.ToSlash(root)
		if root == "." || seen[root] {
			return len(roots) >= max
		}
		seen[root] = true
		roots = append(roots, root)
		return len(roots) >= max
	}
	for _, preferred := range []string{"stories", filepath.Join("internal", "basestories", "stories"), filepath.Join(".kitsoki", "stories")} {
		entries, err := os.ReadDir(filepath.Join(absBase, preferred))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			root := filepath.Join(absBase, preferred, entry.Name())
			if st, err := os.Stat(filepath.Join(root, "app.yaml")); err == nil && !st.IsDir() {
				if addRoot(root) {
					return roots
				}
			}
		}
	}
	walkErr := filepath.WalkDir(absBase, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != absBase && skipStoryDiscoveryDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "app.yaml" {
			return nil
		}
		root := filepath.Dir(path)
		if addRoot(root) {
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		return nil
	}
	return roots
}

func skipStoryDiscoveryDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "node_modules", "vendor", "dist", "build", "target":
		return true
	default:
		return false
	}
}

// splitWorkspacePath maps a workspace handle (a story dir or an app.yaml path)
// to its (storyDir, appPath) pair. A path ending in a YAML extension is treated
// as the manifest; anything else is treated as the story directory.
func splitWorkspacePath(base string) (storyDir, appPath string) {
	if ext := strings.ToLower(filepath.Ext(base)); ext == ".yaml" || ext == ".yml" {
		return filepath.Dir(base), base
	}
	return base, filepath.Join(base, "app.yaml")
}

func shouldValidateStoryWrite(appPath string, explicit *bool) bool {
	if explicit != nil {
		return *explicit
	}
	if st, err := os.Stat(appPath); err == nil && !st.IsDir() {
		return true
	}
	return false
}

// safeJoin resolves rel under root and confines it there: an absolute rel, or
// one whose cleaned result escapes root via `..`, is rejected. This is the
// principle-of-least-surprise guard the proposal requires — an authoring tool
// cannot write outside the story it is authoring.
func safeJoin(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the workspace", rel)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace dir: %w", err)
	}
	joined := filepath.Clean(filepath.Join(absRoot, rel))
	// joined must be absRoot itself or a descendant. Compare with a trailing
	// separator so a sibling dir sharing a prefix (e.g. <root>x) is not accepted.
	if joined != absRoot && !strings.HasPrefix(joined, absRoot+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the workspace", rel)
	}
	return joined, nil
}

// callResolver returns the import resolver one story.* call should use:
// the server's long-lived resolver, wrapped with staged-candidate
// resolution (kitstage.WrapResolver, select-all posture — kits without a
// staged entry fall through) when the caller passed staged:true. The wrap
// is strictly per-call: the server's resolver and process environment are
// never mutated, so concurrent calls without staged:true keep accepted
// resolution.
func (srv *Server) callResolver(staged bool) app.ImportResolver {
	if !staged {
		return srv.importResolver
	}
	return kitstage.WrapResolver(srv.importResolver, kitstage.SelectAll)
}

// ── shared wrappers over shipped functions ────────────────────────────────────

// loadApp loads and compiles the story at appPath to an app.App, mirroring the
// web editor's EditorApp construction (registry.go) so story.graph and the
// /editor view are the same computation.
func loadApp(appPath string, resolver app.ImportResolver) (app.App, error) {
	def, err := app.LoadWithResolver(appPath, nil, resolver)
	if err != nil {
		return nil, err
	}
	return app.Compile(def), nil
}

// validateStory loads the story and projects app.Load's error to the structured
// {ok, errors[]} result. ok is true exactly when no ValidationError was found.
func validateStory(appPath string, resolver app.ImportResolver) StoryValidateOK {
	_, err := app.LoadWithResolver(appPath, nil, resolver)
	if err == nil {
		return StoryValidateOK{OK: true, Errors: []ValidationItem{}}
	}
	items := collectValidationErrors(err)
	if len(items) == 0 {
		// A non-validation load error (e.g. file not found) still has to surface
		// as a structured problem rather than silently report ok.
		items = []ValidationItem{{Message: err.Error()}}
	}
	return StoryValidateOK{OK: false, Errors: items}
}

// collectValidationErrors walks the (possibly errors.Join'd) error tree and
// returns every *app.ValidationError it carries, in tree order. app.Load joins
// the full invariant set, so a single errors.As would surface only the first;
// the walk recovers them all.
func collectValidationErrors(err error) []ValidationItem {
	var out []ValidationItem
	var walk func(e error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if ve, ok := e.(*app.ValidationError); ok {
			out = append(out, ValidationItem{
				File:    ve.File,
				Line:    ve.Line,
				Column:  ve.Column,
				Message: ve.Message,
			})
		}
		switch u := e.(type) {
		case interface{ Unwrap() []error }:
			for _, c := range u.Unwrap() {
				walk(c)
			}
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		}
	}
	walk(err)
	return out
}

// projectFlowReport flattens a testrunner.FlowReport to the wire shape: the
// aggregate counts plus one entry per fixture. Each entry always carries a
// per-fixture FailureCount; the full per-turn failure strings are included only
// when detailed is true (token diet). ok is true exactly when no fixture failed.
func projectFlowReport(report *testrunner.FlowReport, detailed bool) StoryTestOK {
	out := StoryTestOK{
		OK:      report.Failed == 0,
		Passed:  report.Passed,
		Failed:  report.Failed,
		Results: make([]FlowResultItem, 0, len(report.Results)),
	}
	for _, r := range report.Results {
		item := FlowResultItem{File: r.File, Passed: r.Passed, Skipped: r.Skipped}
		var failures []string
		for _, turn := range r.Turns {
			failures = append(failures, turn.Failures...)
		}
		item.FailureCount = len(failures)
		if detailed {
			item.Failures = failures
		}
		out.Results = append(out.Results, item)
	}
	return out
}
