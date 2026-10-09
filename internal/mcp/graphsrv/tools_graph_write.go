package graphsrv

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"kitsoki/internal/host"
)

// registerGraphWriteTools registers the P4 write family (plan §3.4):
// graph.propose, graph.withdraw, graph.apply, graph.authorize.
// graph.changeset (read-only, all modes) lives in tools_graph.go alongside
// the other read tools. Called from RegisterGraphTools's mode switch — not
// at all in read mode, per the plan §3.1 "an MCP client shouldn't see write
// tools it can never call" tools/list economics rationale. graph.authorize
// is still registered (but gated STEWARD_ONLY at runtime) in propose mode,
// since a propose-mode caller attempting authorize is a meaningful,
// teachable rejection, not a tool that shouldn't exist for that mode.
func registerGraphWriteTools(srv *mcpsdk.Server, deps *Deps) {
	registerGraphProposeTool(srv, deps)
	registerGraphWithdrawTool(srv, deps)
	registerGraphApplyTool(srv, deps)
	registerGraphAuthorizeTool(srv, deps)
}

// classifyWriteReject maps a write op's reject_reasons/lint content to an
// mcp-graph error code. Unlike classifyHostErr (a raw Go error's message),
// this inspects RejectReasons/Lint strings returned INSIDE a successful
// host.graph.* Result — the hazard guards (guards.go) surface as reject
// reasons prefixed "NEEDS_CANONICALIZATION:", never as Go errors, from the
// write ops.
func classifyWriteReject(rejectReasons []any, hasLintIssues bool) string {
	for _, r := range rejectReasons {
		if s, ok := r.(string); ok && strings.HasPrefix(s, "NEEDS_CANONICALIZATION") {
			return CodeNeedsCanonicalization
		}
	}
	if hasLintIssues {
		return CodeCatalogLintBlocked
	}
	return CodeValidation
}

// writeRejectResult renders a rejected write op (non-empty reject_reasons
// and/or lint issues) as an isError:true CallToolResult, joining every
// reason/issue string into the error message so nothing the engine reported
// is lost just because this layer turns it into a single error payload.
func writeRejectResult(tool string, rejectReasons []any, lintIssues []any) *mcpsdk.CallToolResult {
	code := classifyWriteReject(rejectReasons, len(lintIssues) > 0)
	parts := make([]string, 0, len(rejectReasons)+len(lintIssues))
	for _, r := range rejectReasons {
		if s, ok := r.(string); ok {
			parts = append(parts, s)
		}
	}
	for _, l := range lintIssues {
		if s, ok := l.(string); ok {
			parts = append(parts, s)
		}
	}
	return errorResult(NewError(code, tool+": rejected: "+strings.Join(parts, "; "), ""))
}

// writeCtx builds the context a write op is invoked with: the operator
// identity (host.WithActor, always) and, only in steward mode, the
// steward-trusted flag (host.WithSteward) that lets graphProposeOp honor
// caller-supplied provenance. A propose-mode caller's context is never
// marked steward-trusted, so any provenance it passed is silently stripped
// by graphProposeOp regardless of what's in the wire args.
func writeCtx(ctx context.Context, deps *Deps) context.Context {
	ctx = host.WithActor(ctx, deps.Actor)
	if deps.Mode == ModeSteward {
		ctx = host.WithSteward(ctx, true)
	}
	return ctx
}

// operationsToAny adapts graph.propose's []map[string]any wire operations
// to the []any shape host.graph.propose's args["operations"] expects.
func operationsToAny(ops []map[string]any) []any {
	out := make([]any, len(ops))
	for i, op := range ops {
		out[i] = op
	}
	return out
}

// ─── graph.propose ───

const graphProposeInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "title": {"type": "string", "description": "Short title for the changeset."},
    "operations": {
      "type": "array",
      "minItems": 1,
      "description": "Changeset operations, each {kind, ...}. kind is one of added|modified|removed|renamed|retyped|registry_type_added|registry_type_modified. added/registry_type_added need 'node'+'after' (full new node mapping). modified/registry_type_modified need 'node'+'changes' ([{path, before, after}]). removed/retyped need 'node'+'before' (full expected current mapping; omit to auto guard-fill from the live node). renamed needs 'from'+'to'; retyped also needs 'from_type'+'to_type'. Call graph.get first to see a node's current full mapping.",
      "items": {"type": "object", "additionalProperties": true}
    },
    "visibility": {"type": "string", "description": "Changeset node visibility (default internal)."},
    "validate_only": {"type": "boolean", "description": "If true, run the full validation + scratch lint but write nothing — not even the changeset node."}
  },
  "required": ["title", "operations"],
  "additionalProperties": false
}`

type graphProposeArgs struct {
	Catalog      string           `json:"catalog,omitempty"`
	Title        string           `json:"title"`
	Operations   []map[string]any `json:"operations"`
	Visibility   string           `json:"visibility,omitempty"`
	ValidateOnly bool             `json:"validate_only,omitempty"`
}

type graphProposeOK struct {
	OK            bool   `json:"ok"`
	Catalog       string `json:"catalog"`
	ChangesetID   string `json:"changeset_id"`
	Status        string `json:"status"`
	GuardFills    []any  `json:"guard_fills,omitempty"`
	ValidatedOnly bool   `json:"validated_only,omitempty"`
	Truncated     bool   `json:"truncated,omitempty"`
}

func registerGraphProposeTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.propose",
		Description: "Propose a changeset (add/modify/remove/rename/retype operations) against the bound catalog. Writes a `proposed` changeset node; nothing else is mutated. validate_only runs the same validation + scratch lint without writing anything. A rejected proposal (stale `before` guard, unknown node, a new lint regression) comes back as an error naming why — the catalog is never partially written.",
		InputSchema: json.RawMessage(graphProposeInputSchema),
	}, recorded(deps, "graph.propose", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphPropose(ctx, deps, req)
	}))
}

func handleGraphPropose(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphProposeArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.propose: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if args.Title == "" {
		return errorResult(NewError(CodeValidation, "graph.propose: `title` is required", "")), nil
	}
	if len(args.Operations) == 0 {
		return errorResult(NewError(CodeValidation, "graph.propose: `operations` requires at least 1 entry", "")), nil
	}

	path, anchor, alias, errPayload := deps.resolveWrite(ctx, args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{
		"catalog_path":  path,
		"title":         args.Title,
		"operations":    operationsToAny(args.Operations),
		"validate_only": args.ValidateOnly,
	}
	deps.applyScope(alias, hostArgs)
	if args.Visibility != "" {
		hostArgs["visibility"] = args.Visibility
	}

	res, err := deps.Registry.Invoke(writeCtx(ctx, deps), "host.graph.propose", hostArgs)
	if err != nil {
		return journal(deps, "graph.propose", anchor, alias, req.Params.Arguments, hostErrResult("graph.propose", err), ""), nil
	}
	if res.Error != "" {
		return journal(deps, "graph.propose", anchor, alias, req.Params.Arguments, errorResult(NewError(CodeValidation, "graph.propose: "+res.Error, "")), ""), nil
	}

	changesetID, _ := res.Data["changeset_id"].(string)
	rejected, _ := res.Data["rejected"].(bool)
	if rejected {
		rejectReasons, _ := res.Data["reject_reasons"].([]any)
		lint, _ := res.Data["lint"].([]any)
		// Best-effort: a rejected proposal normally writes nothing (the
		// integrate is then a clean no-op), and a lifecycle warning must
		// never mask the engine's reject reasons.
		_ = deps.integrateWrite(ctx, anchor, "graph-mcp: graph.propose rejected "+changesetID, true)
		return journal(deps, "graph.propose", anchor, alias, req.Params.Arguments, writeRejectResult("graph.propose", rejectReasons, lint), changesetID), nil
	}

	status, _ := res.Data["status"].(string)
	guardFills, _ := res.Data["guard_fills"].([]any)
	validatedOnly, _ := res.Data["validated_only"].(bool)

	if !args.ValidateOnly && !validatedOnly {
		if ep := deps.integrateWrite(ctx, anchor, "graph-mcp: graph.propose "+changesetID, false); ep != nil {
			return journal(deps, "graph.propose", anchor, alias, req.Params.Arguments, errorResult(ep), changesetID), nil
		}
	}

	out := graphProposeOK{OK: true, Catalog: alias, ChangesetID: changesetID, Status: status, GuardFills: guardFills, ValidatedOnly: validatedOnly}
	for !fitsBudget(out, BudgetGraphPropose) && len(out.GuardFills) > 0 {
		out.GuardFills = out.GuardFills[:len(out.GuardFills)-1]
		out.Truncated = true
	}
	return journal(deps, "graph.propose", anchor, alias, req.Params.Arguments, okResult(out), changesetID), nil
}

// ─── graph.withdraw ───

const graphWithdrawInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "id": {"type": "string", "description": "Changeset node id to withdraw."}
  },
  "required": ["id"],
  "additionalProperties": false
}`

type graphWithdrawArgs struct {
	Catalog string `json:"catalog,omitempty"`
	ID      string `json:"id"`
}

type graphWithdrawOK struct {
	OK           bool   `json:"ok"`
	Catalog      string `json:"catalog"`
	ChangesetID  string `json:"changeset_id"`
	Applied      bool   `json:"applied"`
	ChangedFiles []any  `json:"changed_files,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
}

func registerGraphWithdrawTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.withdraw",
		Description: "Withdraw a `proposed` or `authorized` changeset, flipping it to `withdrawn` (the review queue's \"clean this up\" action). In propose mode you may only withdraw a changeset you authored; steward mode may withdraw any changeset.",
		InputSchema: json.RawMessage(graphWithdrawInputSchema),
	}, recorded(deps, "graph.withdraw", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphWithdraw(ctx, deps, req)
	}))
}

// checkWithdrawOwnership fetches changesetID's authored_by field (via the
// already-implemented host.graph.get, not a new host verb) and reports
// NOT_YOUR_CHANGESET when it's set and doesn't match deps.Actor. A missing
// node or a changeset with no authored_by stamp (pre-actor-stamping content,
// or --actor never set) is left for host.graph.withdraw's own "not found"/
// lifecycle checks to report — this only ever blocks a genuine mismatch.
func checkWithdrawOwnership(ctx context.Context, deps *Deps, catalogPath, changesetID string) *ErrorPayload {
	res, err := deps.Registry.Invoke(ctx, "host.graph.get", map[string]any{
		"catalog_path": catalogPath,
		"ids":          []string{changesetID},
		"fields":       []string{"authored_by"},
	})
	if err != nil || res.Error != "" {
		return nil
	}
	nodes, _ := res.Data["nodes"].([]any)
	if len(nodes) == 0 {
		return nil
	}
	node, _ := nodes[0].(map[string]any)
	fields, _ := node["fields"].(map[string]any)
	authoredBy, _ := fields["authored_by"].(string)
	if authoredBy == "" || authoredBy == deps.Actor {
		return nil
	}
	return NewError(CodeNotYourChangeset,
		fmt.Sprintf("graph.withdraw: changeset %q was authored by %q, not the calling actor %q", changesetID, authoredBy, deps.Actor),
		"only the original author (or a steward-mode server) may withdraw a changeset")
}

func handleGraphWithdraw(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphWithdrawArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.withdraw: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if args.ID == "" {
		return errorResult(NewError(CodeValidation, "graph.withdraw: `id` is required", "")), nil
	}

	path, anchor, alias, errPayload := deps.resolveWrite(ctx, args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	if deps.Mode == ModePropose {
		if ep := checkWithdrawOwnership(ctx, deps, path, args.ID); ep != nil {
			return journal(deps, "graph.withdraw", anchor, alias, req.Params.Arguments, errorResult(ep), args.ID), nil
		}
	}

	withdrawArgs := map[string]any{"catalog_path": path, "changeset_id": args.ID}
	deps.applyScope(alias, withdrawArgs)
	res, err := deps.Registry.Invoke(writeCtx(ctx, deps), "host.graph.withdraw", withdrawArgs)
	if err != nil {
		return journal(deps, "graph.withdraw", anchor, alias, req.Params.Arguments, hostErrResult("graph.withdraw", err), args.ID), nil
	}
	if res.Error != "" {
		return journal(deps, "graph.withdraw", anchor, alias, req.Params.Arguments, errorResult(NewError(CodeValidation, "graph.withdraw: "+res.Error, "")), args.ID), nil
	}

	rejected, _ := res.Data["rejected"].(bool)
	if rejected {
		rejectReasons, _ := res.Data["reject_reasons"].([]any)
		lintIssues, _ := res.Data["lint_issues"].([]any)
		return journal(deps, "graph.withdraw", anchor, alias, req.Params.Arguments, writeRejectResult("graph.withdraw", rejectReasons, lintIssues), args.ID), nil
	}

	if ep := deps.integrateWrite(ctx, anchor, "graph-mcp: graph.withdraw "+args.ID, false); ep != nil {
		return journal(deps, "graph.withdraw", anchor, alias, req.Params.Arguments, errorResult(ep), args.ID), nil
	}

	changedFiles, _ := res.Data["changed_files"].([]any)
	out := graphWithdrawOK{OK: true, Catalog: alias, ChangesetID: args.ID, Applied: true, ChangedFiles: changedFiles}
	for !fitsBudget(out, BudgetGraphWithdraw) && len(out.ChangedFiles) > 0 {
		out.ChangedFiles = out.ChangedFiles[:len(out.ChangedFiles)-1]
		out.Truncated = true
	}
	return journal(deps, "graph.withdraw", anchor, alias, req.Params.Arguments, okResult(out), args.ID), nil
}

// ─── graph.apply ───

const graphApplyInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "id": {"type": "string", "description": "Changeset node id to apply."},
    "dry_run": {"type": "boolean", "description": "If true, validate + lint the apply without writing anything. Propose mode requires dry_run:true (real applies need steward mode); steward mode may do either."}
  },
  "required": ["id"],
  "additionalProperties": false
}`

type graphApplyArgs struct {
	Catalog string `json:"catalog,omitempty"`
	ID      string `json:"id"`
	DryRun  bool   `json:"dry_run,omitempty"`
}

type graphApplyOK struct {
	OK           bool   `json:"ok"`
	Catalog      string `json:"catalog"`
	ChangesetID  string `json:"changeset_id"`
	DryRun       bool   `json:"dry_run"`
	Applied      bool   `json:"applied"`
	ChangedFiles []any  `json:"changed_files,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
}

func registerGraphApplyTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.apply",
		Description: "Apply an authorized changeset to the catalog (dry-run-first, all-or-nothing). Propose mode may only dry-run (dry_run:true) — a real apply (dry_run:false) requires --mode steward.",
		InputSchema: json.RawMessage(graphApplyInputSchema),
	}, recorded(deps, "graph.apply", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphApply(ctx, deps, req)
	}))
}

func handleGraphApply(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphApplyArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.apply: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if args.ID == "" {
		return errorResult(NewError(CodeValidation, "graph.apply: `id` is required", "")), nil
	}

	path, anchor, alias, errPayload := deps.resolveWrite(ctx, args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	if deps.Mode == ModePropose && !args.DryRun {
		ep := NewError(CodeStewardOnly,
			"graph.apply: a real apply (dry_run:false) requires --mode steward; propose-mode callers may only dry-run",
			"call graph.apply with dry_run:true, or ask a steward-mode operator to apply")
		return journal(deps, "graph.apply", anchor, alias, req.Params.Arguments, errorResult(ep), args.ID), nil
	}

	applyArgs := map[string]any{"catalog_path": path, "changeset_id": args.ID, "dry_run": args.DryRun}
	deps.applyScope(alias, applyArgs)
	res, err := deps.Registry.Invoke(writeCtx(ctx, deps), "host.graph.apply", applyArgs)
	if err != nil {
		return journal(deps, "graph.apply", anchor, alias, req.Params.Arguments, hostErrResult("graph.apply", err), args.ID), nil
	}
	if res.Error != "" {
		return journal(deps, "graph.apply", anchor, alias, req.Params.Arguments, errorResult(NewError(CodeValidation, "graph.apply: "+res.Error, "")), args.ID), nil
	}

	rejected, _ := res.Data["rejected"].(bool)
	if rejected {
		rejectReasons, _ := res.Data["reject_reasons"].([]any)
		lintIssues, _ := res.Data["lint_issues"].([]any)
		return journal(deps, "graph.apply", anchor, alias, req.Params.Arguments, writeRejectResult("graph.apply", rejectReasons, lintIssues), args.ID), nil
	}

	if !args.DryRun {
		if ep := deps.integrateWrite(ctx, anchor, "graph-mcp: graph.apply "+args.ID, false); ep != nil {
			return journal(deps, "graph.apply", anchor, alias, req.Params.Arguments, errorResult(ep), args.ID), nil
		}
	}

	changedFiles, _ := res.Data["changed_files"].([]any)
	out := graphApplyOK{OK: true, Catalog: alias, ChangesetID: args.ID, DryRun: args.DryRun, Applied: true, ChangedFiles: changedFiles}
	for !fitsBudget(out, BudgetGraphApply) && len(out.ChangedFiles) > 0 {
		out.ChangedFiles = out.ChangedFiles[:len(out.ChangedFiles)-1]
		out.Truncated = true
	}
	return journal(deps, "graph.apply", anchor, alias, req.Params.Arguments, okResult(out), args.ID), nil
}

// ─── graph.authorize ───

const graphAuthorizeInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "id": {"type": "string", "description": "Changeset node id to authorize."}
  },
  "required": ["id"],
  "additionalProperties": false
}`

type graphAuthorizeArgs struct {
	Catalog string `json:"catalog,omitempty"`
	ID      string `json:"id"`
}

type graphAuthorizeOK struct {
	OK           bool   `json:"ok"`
	Catalog      string `json:"catalog"`
	ChangesetID  string `json:"changeset_id"`
	Applied      bool   `json:"applied"`
	ChangedFiles []any  `json:"changed_files,omitempty"`
	Truncated    bool   `json:"truncated,omitempty"`
}

func registerGraphAuthorizeTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.authorize",
		Description: "Authorize a `proposed` changeset, flipping it to `authorized`. Steward-only: nothing an agent does can silently self-authorize a change it proposed — a human (steward-mode operator) must call this.",
		InputSchema: json.RawMessage(graphAuthorizeInputSchema),
	}, recorded(deps, "graph.authorize", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphAuthorize(ctx, deps, req)
	}))
}

func handleGraphAuthorize(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphAuthorizeArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.authorize: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if args.ID == "" {
		return errorResult(NewError(CodeValidation, "graph.authorize: `id` is required", "")), nil
	}

	path, anchor, alias, errPayload := deps.resolveWrite(ctx, args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	if deps.Mode != ModeSteward {
		ep := NewError(CodeStewardOnly,
			fmt.Sprintf("graph.authorize: only a steward-mode server may authorize a changeset — this server is running in %q mode", deps.Mode),
			"ask a steward-mode operator to authorize this changeset")
		return journal(deps, "graph.authorize", anchor, alias, req.Params.Arguments, errorResult(ep), args.ID), nil
	}

	authorizeArgs := map[string]any{"catalog_path": path, "changeset_id": args.ID}
	deps.applyScope(alias, authorizeArgs)
	res, err := deps.Registry.Invoke(writeCtx(ctx, deps), "host.graph.authorize", authorizeArgs)
	if err != nil {
		return journal(deps, "graph.authorize", anchor, alias, req.Params.Arguments, hostErrResult("graph.authorize", err), args.ID), nil
	}
	if res.Error != "" {
		return journal(deps, "graph.authorize", anchor, alias, req.Params.Arguments, errorResult(NewError(CodeValidation, "graph.authorize: "+res.Error, "")), args.ID), nil
	}

	rejected, _ := res.Data["rejected"].(bool)
	if rejected {
		rejectReasons, _ := res.Data["reject_reasons"].([]any)
		lintIssues, _ := res.Data["lint_issues"].([]any)
		return journal(deps, "graph.authorize", anchor, alias, req.Params.Arguments, writeRejectResult("graph.authorize", rejectReasons, lintIssues), args.ID), nil
	}

	if ep := deps.integrateWrite(ctx, anchor, "graph-mcp: graph.authorize "+args.ID, false); ep != nil {
		return journal(deps, "graph.authorize", anchor, alias, req.Params.Arguments, errorResult(ep), args.ID), nil
	}

	changedFiles, _ := res.Data["changed_files"].([]any)
	out := graphAuthorizeOK{OK: true, Catalog: alias, ChangesetID: args.ID, Applied: true, ChangedFiles: changedFiles}
	for !fitsBudget(out, BudgetGraphAuthorize) && len(out.ChangedFiles) > 0 {
		out.ChangedFiles = out.ChangedFiles[:len(out.ChangedFiles)-1]
		out.Truncated = true
	}
	return journal(deps, "graph.authorize", anchor, alias, req.Params.Arguments, okResult(out), args.ID), nil
}
