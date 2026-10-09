package graphsrv

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RegisterGraphTools registers the read family (plan §3.3, now eight tools
// with graph.changeset) against srv, closing over deps, then mode-gates the
// P4 write family (plan §3.4): graph.propose/withdraw/apply/authorize are
// registered in propose and steward mode, never in read mode — an MCP
// client shouldn't see tools/list entries it can never call (plan §3.1).
// graph.changeset is read-only and registered in every mode alongside the
// rest of the read family.
//
// This function (and RegisterFeedbackTools) is deliberately a free function
// taking (srv, deps, mode) rather than a Server method, so P6's studio
// server can call it directly against its own mcpsdk.Server + Deps without
// depending on this package's Server/NewServer/cobra plumbing.
func RegisterGraphTools(srv *mcpsdk.Server, deps *Deps, mode string) {
	registerGraphLintTool(srv, deps)
	registerGraphOpenTool(srv, deps)
	registerGraphGetTool(srv, deps)
	registerGraphFindTool(srv, deps)
	registerGraphNeighborsTool(srv, deps)
	registerGraphTypeTool(srv, deps)
	registerGraphImpactTool(srv, deps)
	registerGraphChangesetTool(srv, deps)
	registerGraphHistoryTool(srv, deps)
	registerGraphClaimTools(srv, deps)

	switch mode {
	case ModePropose, ModeSteward:
		registerGraphWriteTools(srv, deps)
	case ModeRead:
		// Write tools intentionally not registered in read mode.
	}
}

// RegisterFeedbackTools registers feedback.report and feedback.list (plan
// §3.6) — see tools_feedback.go. Like RegisterGraphTools, it's a free
// function so P6's studio mount can reuse it directly.

const graphLintInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {
      "type": "string",
      "description": "Bound catalog alias (omit to use the default catalog)."
    },
    "max": {
      "type": "integer",
      "minimum": 1,
      "description": "Maximum number of issues to return (default 50)."
    }
  },
  "additionalProperties": false
}`

// graphLintArgs is the input to graph.lint.
type graphLintArgs struct {
	Catalog string `json:"catalog,omitempty"`
	Max     int    `json:"max,omitempty"`
}

// graphLintOK is graph.lint's successful result shape: {ok, clean,
// issue_count, issues}.
type graphLintOK struct {
	OK         bool   `json:"ok"`
	Catalog    string `json:"catalog"`
	Clean      bool   `json:"clean"`
	IssueCount int    `json:"issue_count"`
	Issues     []any  `json:"issues"`
	Truncated  bool   `json:"truncated,omitempty"`
}

const graphLintDefaultMax = 50

// registerGraphLintTool wires graph.lint, the one read tool built end-to-end
// in the P2 foundation step (chosen as the simplest wrapper: it's a direct
// pass-through of the pre-existing host.graph.lint op with no
// catalog-overview composition and no cursor/truncation-budget math beyond
// a flat issue-count cap).
func registerGraphLintTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.lint",
		Description: "Report catalog lint issues (dangling edges, missing required fields, etc). Wraps the existing lint engine op.",
		InputSchema: json.RawMessage(graphLintInputSchema),
	}, recorded(deps, "graph.lint", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphLint(ctx, deps, req)
	}))
}

func handleGraphLint(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphLintArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.lint: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}

	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{"catalog_path": path}
	deps.applyScope(alias, hostArgs)
	res, err := deps.Registry.Invoke(ctx, "host.graph.lint", hostArgs)
	if err != nil {
		return errorResult(NewError(CodeValidation, "graph.lint: "+err.Error(), "check that the bound catalog path is a valid catalog file or bundle")), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.lint: "+res.Error, "")), nil
	}

	issues, _ := res.Data["issues"].([]any)
	max := args.Max
	if max <= 0 {
		max = graphLintDefaultMax
	}
	kept, truncated := TruncateSlice(issues, max)

	clean, _ := res.Data["clean"].(bool)
	issueCount, _ := res.Data["issue_count"].(int)

	out := graphLintOK{
		OK:         true,
		Catalog:    alias,
		Clean:      clean,
		IssueCount: issueCount,
		Issues:     kept,
		Truncated:  truncated,
	}
	return okResult(out), nil
}

// classifyHostErr maps a host.graph.* Go error (never a Result.Error — those
// are handler-shaped, this is the raw "unknown node req-x (nearest: [...])"
// style error internal/host's ops return) to an mcp-graph error code. The
// underlying error text already carries nearest-id suggestions / vocabulary
// lists (see internal/host/graph_read_ops.go and graph_handlers.go), so it
// doubles as the hint rather than being re-derived here.
func classifyHostErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "out of scope"):
		return CodeOutOfScope
	case strings.Contains(msg, "unknown node") || strings.Contains(msg, "node") && strings.Contains(msg, "not found"):
		return CodeUnknownNode
	case strings.Contains(msg, "unknown type") || strings.Contains(msg, "type") && strings.Contains(msg, "not found in registry"):
		return CodeUnknownType
	case strings.Contains(msg, "unknown edge"):
		return CodeUnknownEdge
	default:
		return CodeValidation
	}
}

func hostErrResult(toolName string, err error) *mcpsdk.CallToolResult {
	return errorResult(NewError(classifyHostErr(err), toolName+": "+err.Error(), ""))
}

// ─── graph.open ───

const graphOpenInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {
      "type": "string",
      "description": "Bound catalog alias (omit to use the default catalog)."
    }
  },
  "additionalProperties": false
}`

type graphOpenArgs struct {
	Catalog string `json:"catalog,omitempty"`
}

type graphOpenOK struct {
	OK         bool           `json:"ok"`
	Catalog    string         `json:"catalog"`
	Head       map[string]any `json:"head"`
	NodeCount  int            `json:"node_count"`
	Types      []any          `json:"types"`
	Lint       map[string]any `json:"lint"`
	Changesets map[string]any `json:"changesets"`
	Feedback   map[string]any `json:"feedback"`
	// Scope is present only on a scoped session: {active, member_count,
	// total_node_count, pruned_edges, spec}.
	Scope     map[string]any `json:"scope,omitempty"`
	Guide     string         `json:"guide"`
	Truncated bool           `json:"truncated,omitempty"`
}

func registerGraphOpenTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.open",
		Description: "Catalog overview: head, node count, per-type census, lint summary, changeset lifecycle counts, feedback pending count, and a short orientation guide. Call this first.",
		InputSchema: json.RawMessage(graphOpenInputSchema),
	}, recorded(deps, "graph.open", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphOpen(ctx, deps, req)
	}))
}

func handleGraphOpen(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphOpenArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.open: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}
	hostArgs := map[string]any{"catalog_path": path}
	deps.applyScope(alias, hostArgs)
	res, err := deps.Registry.Invoke(ctx, "host.graph.open", hostArgs)
	if err != nil {
		return hostErrResult("graph.open", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.open: "+res.Error, "")), nil
	}

	head, _ := res.Data["head"].(map[string]any)
	types, _ := res.Data["types"].([]any)
	lint, _ := res.Data["lint"].(map[string]any)
	changesets, _ := res.Data["changesets"].(map[string]any)
	feedback, _ := res.Data["feedback"].(map[string]any)
	if feedback == nil {
		feedback = map[string]any{}
	}
	// The engine op only stubs feedback.pending at 0 (it doesn't know
	// about the local sink, which lives in this package). Overwrite it
	// here with the real pending count from the local sink now that
	// feedback.report/feedback.list exist.
	feedback["pending"] = pendingFeedbackCount(path)
	guide, _ := res.Data["guide"].(string)
	nodeCount, _ := res.Data["node_count"].(int)
	scope, _ := res.Data["scope"].(map[string]any)

	out := graphOpenOK{
		OK:         true,
		Catalog:    alias,
		Head:       head,
		NodeCount:  nodeCount,
		Types:      types,
		Lint:       lint,
		Changesets: changesets,
		Feedback:   feedback,
		Scope:      scope,
		Guide:      guide,
	}

	// Enforce the ≤2KB overview budget (BudgetGraphOpen): truncate the
	// per-type census list in-band before giving up on the byte cap — the
	// plan explicitly calls this out ("truncate per-type census / vocab
	// listing if needed") rather than allowing graph.open to blow up on a
	// large catalog.
	for fitsBudget(out, BudgetGraphOpen) == false && len(out.Types) > 0 {
		out.Types = out.Types[:len(out.Types)-1]
		out.Truncated = true
	}

	return okResult(out), nil
}

// fitsBudget reports whether v's JSON encoding is within capBytes.
func fitsBudget(v any, capBytes int) bool {
	b, err := json.Marshal(v)
	if err != nil {
		return true // don't loop forever over an unmarshalable value
	}
	return len(b) <= capBytes
}

// ─── graph.get ───

const graphGetInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {
      "type": "string",
      "description": "Bound catalog alias (omit to use the default catalog)."
    },
    "ids": {
      "type": "array",
      "items": {"type": "string"},
      "minItems": 1,
      "maxItems": 20,
      "description": "Node ids to fetch (1-20)."
    },
    "view": {
      "type": "string",
      "enum": ["full"],
      "description": "Envelope view. Only \"full\" is supported; omit for the default."
    },
    "fields": {
      "type": "array",
      "items": {"type": "string"},
      "description": "Restrict returned fields to this list. Passing exactly one entry lifts the per-field truncation cap from 2KB to 32KB (single-field refetch)."
    }
  },
  "required": ["ids"],
  "additionalProperties": false
}`

type graphGetArgs struct {
	Catalog string   `json:"catalog,omitempty"`
	IDs     []string `json:"ids"`
	View    string   `json:"view,omitempty"`
	Fields  []string `json:"fields,omitempty"`
}

type graphGetOK struct {
	OK        bool   `json:"ok"`
	Catalog   string `json:"catalog"`
	Nodes     []any  `json:"nodes"`
	Missing   []any  `json:"missing"`
	Truncated bool   `json:"truncated,omitempty"`
}

func registerGraphGetTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.get",
		Description: "Fetch full node envelopes (fields, edges, refs_in) by id. Unknown ids come back in `missing` with nearest-id suggestions instead of failing the whole call.",
		InputSchema: json.RawMessage(graphGetInputSchema),
	}, recorded(deps, "graph.get", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphGet(ctx, deps, req)
	}))
}

func handleGraphGet(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphGetArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.get: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if len(args.IDs) == 0 {
		return errorResult(NewError(CodeValidation, "graph.get: `ids` requires at least 1 entry", "")), nil
	}
	if len(args.IDs) > 20 {
		return errorResult(NewError(CodeValidation, "graph.get: `ids` accepts at most 20 entries, got "+strconv.Itoa(len(args.IDs)), "")), nil
	}
	if args.View != "" && args.View != "full" {
		return errorResult(NewError(CodeValidation, "graph.get: `view` must be \"full\" if given, got "+args.View, "")), nil
	}

	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{"catalog_path": path, "ids": args.IDs}
	deps.applyScope(alias, hostArgs)
	if len(args.Fields) > 0 {
		hostArgs["fields"] = args.Fields
	}
	res, err := deps.Registry.Invoke(ctx, "host.graph.get", hostArgs)
	if err != nil {
		return hostErrResult("graph.get", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.get: "+res.Error, "")), nil
	}

	nodes, _ := res.Data["nodes"].([]any)
	missing, _ := res.Data["missing"].([]any)
	truncated := false

	// Per-field cap (2KB, or 32KB on a single-field refetch): truncate each
	// field's serialized value in-band rather than dropping it.
	fieldCap := BudgetGraphGetField
	if len(args.Fields) == 1 {
		fieldCap = BudgetGraphGetSingle
	}
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			continue
		}
		fields, ok := node["fields"].(map[string]any)
		if !ok {
			continue
		}
		for k, v := range fields {
			b, err := json.Marshal(v)
			if err != nil {
				continue
			}
			if len(b) > fieldCap {
				out, did := TruncateString(string(b), fieldCap)
				fields[k] = out
				truncated = truncated || did
			}
		}
	}

	// Overall per-call budget (24KB): drop trailing nodes in-band if the
	// per-field caps above weren't enough (many small-but-not-tiny fields
	// across up to 20 nodes).
	out := graphGetOK{OK: true, Catalog: alias, Nodes: nodes, Missing: missing}
	for !fitsBudget(out, BudgetGraphGetTotal) && len(out.Nodes) > 1 {
		out.Nodes = out.Nodes[:len(out.Nodes)-1]
		truncated = true
	}
	out.Truncated = truncated

	return okResult(out), nil
}

// ─── graph.find ───

const graphFindInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "type": {"type": "string", "description": "Restrict to nodes IsA this type id."},
    "status": {"type": "array", "items": {"type": "string"}, "description": "Restrict to nodes whose status is one of these values."},
    "visibility": {"type": "string", "description": "Restrict to nodes with this visibility."},
    "edge": {
      "type": "object",
      "properties": {
        "field": {"type": "string", "description": "Edge field id the node must carry."},
        "to": {"type": "string", "description": "Optional: the edge must target this node id."}
      },
      "required": ["field"],
      "additionalProperties": false
    },
    "no_inbound": {
      "type": "object",
      "properties": {"edge": {"type": "string", "description": "Optional edge field; omit to mean \"no inbound refs of any kind\"."}},
      "additionalProperties": false
    },
    "no_outbound": {
      "type": "object",
      "properties": {"edge": {"type": "string", "description": "Optional edge field; omit to mean \"no outbound edges of any kind\"."}},
      "additionalProperties": false
    },
    "field": {
      "type": "object",
      "properties": {
        "key": {"type": "string", "description": "Field name to test."},
        "equals": {"type": "string", "description": "Match if the field's string form equals this value."},
        "contains": {"type": "string", "description": "Match if the field's string form contains this substring."}
      },
      "required": ["key"],
      "additionalProperties": false
    },
    "text": {"type": "string", "description": "Case-sensitive substring match over id, title, and every field value."},
    "view": {"type": "string", "enum": ["summary"], "description": "Row shape. Only \"summary\" is supported today."},
    "limit": {"type": "integer", "minimum": 0, "description": "Max rows per page (default 25)."},
    "cursor": {"type": "string", "description": "Opaque pagination cursor from a previous graph.find call's next_cursor."},
    "count_only": {"type": "boolean", "description": "If true, return only the total count, no rows."}
  },
  "additionalProperties": false
}`

type graphFindArgs struct {
	Catalog    string         `json:"catalog,omitempty"`
	Type       string         `json:"type,omitempty"`
	Status     []string       `json:"status,omitempty"`
	Visibility string         `json:"visibility,omitempty"`
	Edge       map[string]any `json:"edge,omitempty"`
	NoInbound  map[string]any `json:"no_inbound,omitempty"`
	NoOutbound map[string]any `json:"no_outbound,omitempty"`
	Field      map[string]any `json:"field,omitempty"`
	Text       string         `json:"text,omitempty"`
	View       string         `json:"view,omitempty"`
	Limit      *int           `json:"limit,omitempty"`
	Cursor     string         `json:"cursor,omitempty"`
	CountOnly  bool           `json:"count_only,omitempty"`
}

type graphFindOK struct {
	OK             bool   `json:"ok"`
	Catalog        string `json:"catalog"`
	Total          int    `json:"total"`
	Rows           []any  `json:"rows"`
	NextCursor     string `json:"next_cursor,omitempty"`
	CatalogChanged bool   `json:"catalog_changed,omitempty"`
	Truncated      bool   `json:"truncated,omitempty"`
}

// findCursor is graph.find's opaque pagination token: an offset guarded by
// a filter hash (did the caller change filters mid-pagination?) and a
// catalog-content hash (did the bound catalog change under us?). Encoded as
// base64url(json(...)) — opaque to callers, not meant to be hand-constructed.
type findCursor struct {
	Offset      int    `json:"o"`
	FilterHash  string `json:"f"`
	CatalogHash string `json:"c"`
}

func encodeFindCursor(c findCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeFindCursor(s string) (findCursor, error) {
	var c findCursor
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}

// findFilterHash digests the filter-shaping arguments (everything except
// pagination/count_only), so a cursor minted for one filter is detected as
// stale if the caller silently changes filters between calls.
func findFilterHash(args graphFindArgs) string {
	key := map[string]any{
		"type": args.Type, "status": args.Status, "visibility": args.Visibility,
		"edge": args.Edge, "no_inbound": args.NoInbound, "no_outbound": args.NoOutbound,
		"field": args.Field, "text": args.Text,
	}
	b, _ := json.Marshal(key)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

// findCatalogHash best-effort digests the bound catalog's current content
// shape (node ids + warnings, via host.graph.load) so graph.find can detect
// "the catalog changed since this cursor was minted" without keeping any
// server-side pagination state. Returns "" on any failure — callers treat
// that as "unknown", which conservatively means "assume changed" wherever a
// prior cursor claimed a specific hash.
func findCatalogHash(ctx context.Context, deps *Deps, path, alias string) string {
	hostArgs := map[string]any{"catalog_path": path}
	// The hash must digest the SCOPED view: an out-of-scope edit that can't
	// affect any page a scoped find returns shouldn't invalidate cursors.
	deps.applyScope(alias, hostArgs)
	res, err := deps.Registry.Invoke(ctx, "host.graph.load", hostArgs)
	if err != nil {
		return ""
	}
	b, err := json.Marshal(res.Data)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

func registerGraphFindTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.find",
		Description: "Search nodes by type/status/visibility/edge/field/text. Paginates via an opaque cursor (next_cursor); catalog_changed:true means the bound catalog changed since the cursor was minted, so pagination restarted from the top rather than silently skewing.",
		InputSchema: json.RawMessage(graphFindInputSchema),
	}, recorded(deps, "graph.find", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphFind(ctx, deps, req)
	}))
}

func handleGraphFind(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphFindArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.find: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if args.View != "" && args.View != "summary" {
		return errorResult(NewError(CodeValidation, "graph.find: `view` must be \"summary\" if given, got "+args.View, "")), nil
	}

	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	limit := 25
	if args.Limit != nil {
		limit = *args.Limit
	}

	fh := findFilterHash(args)
	ch := findCatalogHash(ctx, deps, path, alias)

	offset := 0
	catalogChanged := false
	if args.Cursor != "" {
		cur, err := decodeFindCursor(args.Cursor)
		if err != nil {
			return errorResult(NewError(CodeValidation, "graph.find: `cursor` is not a valid graph.find cursor", "omit cursor to start a fresh page")), nil
		}
		if cur.FilterHash != fh || ch == "" || cur.CatalogHash != ch {
			catalogChanged = true
			// offset stays 0: restart pagination from the top rather than
			// silently returning a page computed against a stale offset.
		} else {
			offset = cur.Offset
		}
	}

	hostArgs := map[string]any{
		"catalog_path": path,
		"limit":        limit,
		"offset":       offset,
		"count_only":   args.CountOnly,
	}
	deps.applyScope(alias, hostArgs)
	if args.Type != "" {
		hostArgs["type"] = args.Type
	}
	if len(args.Status) > 0 {
		hostArgs["status"] = args.Status
	}
	if args.Visibility != "" {
		hostArgs["visibility"] = args.Visibility
	}
	if args.Edge != nil {
		hostArgs["edge"] = args.Edge
	}
	if args.NoInbound != nil {
		hostArgs["no_inbound"] = args.NoInbound
	}
	if args.NoOutbound != nil {
		hostArgs["no_outbound"] = args.NoOutbound
	}
	if args.Field != nil {
		hostArgs["field"] = args.Field
	}
	if args.Text != "" {
		hostArgs["text"] = args.Text
	}

	res, err := deps.Registry.Invoke(ctx, "host.graph.find", hostArgs)
	if err != nil {
		return hostErrResult("graph.find", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.find: "+res.Error, "")), nil
	}

	total, _ := res.Data["total"].(int)
	rows, _ := res.Data["rows"].([]any)

	out := graphFindOK{OK: true, Catalog: alias, Total: total, Rows: rows, CatalogChanged: catalogChanged}

	// ≤8KB/page budget (BudgetGraphFindPage): truncate rows in-band before
	// reaching the requested limit count if needed.
	truncated := false
	for !fitsBudget(out, BudgetGraphFindPage) && len(out.Rows) > 0 {
		out.Rows = out.Rows[:len(out.Rows)-1]
		truncated = true
	}
	out.Truncated = truncated

	if offset+len(out.Rows) < total {
		out.NextCursor = encodeFindCursor(findCursor{Offset: offset + len(out.Rows), FilterHash: fh, CatalogHash: ch})
	}

	return okResult(out), nil
}

// ─── graph.neighbors ───

const graphNeighborsInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "id": {"type": "string", "description": "Root node id to walk edges from."},
    "direction": {"type": "string", "enum": ["in", "out", "both"], "description": "Edge direction to walk (default both)."},
    "edges": {"type": "array", "items": {"type": "string"}, "description": "Restrict to these edge field ids (default: all)."},
    "depth": {"type": "integer", "minimum": 1, "maximum": 3, "description": "BFS depth, 1-3 (default 1)."},
    "limit": {"type": "integer", "minimum": 0, "description": "Max triples to return (default: unlimited)."},
    "view": {"type": "string", "enum": ["summary"], "description": "Row shape for the summary rows. Only \"summary\" is supported today."}
  },
  "required": ["id"],
  "additionalProperties": false
}`

type graphNeighborsArgs struct {
	Catalog   string   `json:"catalog,omitempty"`
	ID        string   `json:"id"`
	Direction string   `json:"direction,omitempty"`
	Edges     []string `json:"edges,omitempty"`
	Depth     *int     `json:"depth,omitempty"`
	Limit     *int     `json:"limit,omitempty"`
	View      string   `json:"view,omitempty"`
}

type graphNeighborsOK struct {
	OK        bool   `json:"ok"`
	Catalog   string `json:"catalog"`
	Triples   []any  `json:"triples"`
	Rows      []any  `json:"rows"`
	Truncated bool   `json:"truncated,omitempty"`
}

func registerGraphNeighborsTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.neighbors",
		Description: "Walk edges from a node (in/out/both, 1-3 hops) and return the edge triples plus a deduplicated summary of the nodes reached.",
		InputSchema: json.RawMessage(graphNeighborsInputSchema),
	}, recorded(deps, "graph.neighbors", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphNeighbors(ctx, deps, req)
	}))
}

func handleGraphNeighbors(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphNeighborsArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.neighbors: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if args.ID == "" {
		return errorResult(NewError(CodeValidation, "graph.neighbors: `id` is required", "")), nil
	}
	if args.View != "" && args.View != "summary" {
		return errorResult(NewError(CodeValidation, "graph.neighbors: `view` must be \"summary\" if given, got "+args.View, "")), nil
	}

	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{"catalog_path": path, "id": args.ID}
	deps.applyScope(alias, hostArgs)
	if args.Direction != "" {
		hostArgs["direction"] = args.Direction
	}
	if len(args.Edges) > 0 {
		hostArgs["edges"] = args.Edges
	}
	if args.Depth != nil {
		hostArgs["depth"] = *args.Depth
	}
	if args.Limit != nil {
		hostArgs["limit"] = *args.Limit
	}

	res, err := deps.Registry.Invoke(ctx, "host.graph.neighbors", hostArgs)
	if err != nil {
		return hostErrResult("graph.neighbors", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.neighbors: "+res.Error, "")), nil
	}

	triples, _ := res.Data["triples"].([]any)
	rows, _ := res.Data["rows"].([]any)

	out := graphNeighborsOK{OK: true, Catalog: alias, Triples: triples, Rows: rows}

	// ≤10KB budget (BudgetGraphNeighbors): drop trailing triples first (the
	// bulkier of the two lists), then trailing rows, in-band.
	truncated := false
	for !fitsBudget(out, BudgetGraphNeighbors) && (len(out.Triples) > 0 || len(out.Rows) > 0) {
		if len(out.Triples) > 0 {
			out.Triples = out.Triples[:len(out.Triples)-1]
		} else {
			out.Rows = out.Rows[:len(out.Rows)-1]
		}
		truncated = true
	}
	out.Truncated = truncated

	return okResult(out), nil
}

// ─── graph.type ───

const graphTypeInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "type_id": {"type": "string", "description": "Type id to explain. Omit to list every registered type."}
  },
  "additionalProperties": false
}`

type graphTypeArgs struct {
	Catalog string `json:"catalog,omitempty"`
	TypeID  string `json:"type_id,omitempty"`
}

func registerGraphTypeTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.type",
		Description: "Explain one type's schema, ancestry, and edge vocabulary (+ instance count), or list every registered type when type_id is omitted.",
		InputSchema: json.RawMessage(graphTypeInputSchema),
	}, recorded(deps, "graph.type", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphType(ctx, deps, req)
	}))
}

func handleGraphType(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphTypeArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.type: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{"catalog_path": path}
	deps.applyScope(alias, hostArgs)
	if args.TypeID != "" {
		hostArgs["type_id"] = args.TypeID
	}
	res, err := deps.Registry.Invoke(ctx, "host.graph.type_census", hostArgs)
	if err != nil {
		return hostErrResult("graph.type", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.type: "+res.Error, "")), nil
	}

	out := map[string]any{"ok": true, "catalog": alias}
	for k, v := range res.Data {
		out[k] = v
	}
	return okResult(out), nil
}

// ─── graph.changeset ───

const graphChangesetInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "op": {"type": "string", "enum": ["list", "get", "touching"], "description": "list: every changeset's lifecycle summary + status counts. get: one changeset's parsed operations (requires id). touching: changesets whose operations reference a node (requires node)."},
    "id": {"type": "string", "description": "Changeset node id. Required when op is \"get\"."},
    "node": {"type": "string", "description": "Node id to reverse-index. Required when op is \"touching\"."}
  },
  "required": ["op"],
  "additionalProperties": false
}`

type graphChangesetArgs struct {
	Catalog string `json:"catalog,omitempty"`
	Op      string `json:"op"`
	ID      string `json:"id,omitempty"`
	Node    string `json:"node,omitempty"`
}

// registerGraphChangesetTool wires graph.changeset — a read-only wrapper
// around the already-implemented host.graph.changeset op (list/get/touching
// changeset lifecycle reads), registered in every mode alongside the rest
// of the read family (unlike propose/withdraw/apply/authorize, which are
// P4's actual write surface).
func registerGraphChangesetTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.changeset",
		Description: "Read changeset lifecycle state: list every changeset (+ status counts), get one changeset's parsed operations, or find every changeset touching a given node id.",
		InputSchema: json.RawMessage(graphChangesetInputSchema),
	}, recorded(deps, "graph.changeset", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphChangeset(ctx, deps, req)
	}))
}

func handleGraphChangeset(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphChangesetArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.changeset: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	switch args.Op {
	case "list", "get", "touching":
	default:
		return errorResult(NewError(CodeValidation, "graph.changeset: `op` must be one of list|get|touching, got "+args.Op, "")), nil
	}
	if args.Op == "get" && args.ID == "" {
		return errorResult(NewError(CodeValidation, "graph.changeset: op \"get\" requires `id`", "")), nil
	}
	if args.Op == "touching" && args.Node == "" {
		return errorResult(NewError(CodeValidation, "graph.changeset: op \"touching\" requires `node`", "")), nil
	}

	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{"catalog_path": path, "action": args.Op}
	deps.applyScope(alias, hostArgs)
	if args.ID != "" {
		hostArgs["changeset_id"] = args.ID
	}
	if args.Node != "" {
		hostArgs["node_id"] = args.Node
	}
	res, err := deps.Registry.Invoke(ctx, "host.graph.changeset", hostArgs)
	if err != nil {
		return hostErrResult("graph.changeset", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.changeset: "+res.Error, "")), nil
	}

	out := map[string]any{"ok": true, "catalog": alias}
	for k, v := range res.Data {
		out[k] = v
	}

	truncated := false
	for _, key := range []string{"changesets", "touching"} {
		list, ok := out[key].([]any)
		if !ok {
			continue
		}
		for !fitsBudget(out, BudgetGraphChangeset) && len(list) > 0 {
			list = list[:len(list)-1]
			out[key] = list
			truncated = true
		}
	}
	if truncated {
		out["truncated"] = true
	}

	return okResult(out), nil
}

// ─── graph.impact ───

const graphImpactInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "id": {"type": "string", "description": "Node id to assess."},
    "to_type": {"type": "string", "description": "If retyping to this type id, also report incompatible inbound refs."}
  },
  "required": ["id"],
  "additionalProperties": false
}`

type graphImpactArgs struct {
	Catalog string `json:"catalog,omitempty"`
	ID      string `json:"id"`
	ToType  string `json:"to_type,omitempty"`
}

func registerGraphImpactTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.impact",
		Description: "Predict what retyping or removing a node would break: its type explanation, every inbound reference, and (with to_type) which of those refs would become type-incompatible. Call before retype/remove; propose will reject what this predicts.",
		InputSchema: json.RawMessage(graphImpactInputSchema),
	}, recorded(deps, "graph.impact", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphImpact(ctx, deps, req)
	}))
}

func handleGraphImpact(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphImpactArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.impact: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}
	if args.ID == "" {
		return errorResult(NewError(CodeValidation, "graph.impact: `id` is required", "")), nil
	}
	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{"catalog_path": path, "mode": "impact", "target": args.ID}
	deps.applyScope(alias, hostArgs)
	if args.ToType != "" {
		hostArgs["to_type"] = args.ToType
	}
	res, err := deps.Registry.Invoke(ctx, "host.graph.query", hostArgs)
	if err != nil {
		return hostErrResult("graph.impact", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.impact: "+res.Error, "")), nil
	}

	out := map[string]any{"ok": true, "catalog": alias}
	for k, v := range res.Data {
		out[k] = v
	}
	return okResult(out), nil
}
