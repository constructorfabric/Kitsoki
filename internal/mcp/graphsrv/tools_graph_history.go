// graph.history — plan §3.5 "History": a read-only MCP wrapper around
// host.graph.history (internal/host/graph_history_ops.go), which merges
// the changeset era (already-recorded changeset lifecycle events) with the
// git era (commits touching the catalog file) into one newest-first,
// paginated timeline. Kept in its own file, separate from tools_graph.go,
// per the P5 spec's reviewability note — this tool has enough of its own
// cursor/budget/error plumbing to be worth isolating.
package graphsrv

import (
	"context"
	"encoding/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const graphHistoryInputSchema = `{
  "type": "object",
  "properties": {
    "catalog": {"type": "string", "description": "Bound catalog alias (omit to use the default catalog)."},
    "id": {"type": "string", "description": "Node id to scope the timeline to (omit for a catalog-wide timeline)."},
    "since": {"type": "string", "description": "RFC3339 timestamp; only return entries at or after this time."},
    "limit": {"type": "integer", "minimum": 1, "description": "Max entries per page (default 25)."},
    "cursor": {"type": "string", "description": "Opaque pagination cursor from a previous graph.history call's next_cursor."}
  },
  "additionalProperties": false
}`

type graphHistoryArgs struct {
	Catalog string `json:"catalog,omitempty"`
	ID      string `json:"id,omitempty"`
	Since   string `json:"since,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
}

type graphHistoryOK struct {
	OK         bool   `json:"ok"`
	Catalog    string `json:"catalog"`
	Entries    []any  `json:"entries"`
	NextCursor string `json:"next_cursor,omitempty"`
	Truncated  bool   `json:"truncated,omitempty"`
}

// registerGraphHistoryTool wires graph.history. Like graph.changeset, it's
// a pure read tool over already-recorded state (changeset nodes + git
// history), so it's registered in every mode, not mode-gated behind
// propose/steward the way the P4 write family is.
func registerGraphHistoryTool(srv *mcpsdk.Server, deps *Deps) {
	srv.AddTool(&mcpsdk.Tool{
		Name:        "graph.history",
		Description: "Merged change timeline for a node (or the whole catalog if `id` is omitted): every recorded changeset event plus every git commit that changed the node, newest-first. Each entry's `source` is \"changeset\" or \"git\".",
		InputSchema: json.RawMessage(graphHistoryInputSchema),
	}, recorded(deps, "graph.history", func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return handleGraphHistory(ctx, deps, req)
	}))
}

func handleGraphHistory(ctx context.Context, deps *Deps, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
	var args graphHistoryArgs
	if len(req.Params.Arguments) > 0 {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return errorResult(NewError(CodeValidation, "graph.history: arguments are not valid JSON: "+err.Error(), "")), nil
		}
	}

	path, _, alias, errPayload := deps.resolveRead(args.Catalog)
	if errPayload != nil {
		return errorResult(errPayload), nil
	}

	hostArgs := map[string]any{"catalog_path": path}
	deps.applyScope(alias, hostArgs)
	if args.ID != "" {
		hostArgs["id"] = args.ID
	}
	if args.Since != "" {
		hostArgs["since"] = args.Since
	}
	if args.Limit != nil {
		hostArgs["limit"] = *args.Limit
	}
	if args.Cursor != "" {
		hostArgs["cursor"] = args.Cursor
	}

	res, err := deps.Registry.Invoke(ctx, "host.graph.history", hostArgs)
	if err != nil {
		return hostErrResult("graph.history", err), nil
	}
	if res.Error != "" {
		return errorResult(NewError(CodeValidation, "graph.history: "+res.Error, "")), nil
	}

	entries, _ := res.Data["entries"].([]any)
	nextCursor, _ := res.Data["next_cursor"].(string)

	out := graphHistoryOK{OK: true, Catalog: alias, Entries: entries, NextCursor: nextCursor}

	// ≤4KB/page budget (BudgetGraphHistory): truncate entries in-band
	// before giving up, mirroring graph.find/graph.changeset's truncation
	// convention. A truncated page still carries next_cursor (computed by
	// the host op against the full, untruncated page) so pagination keeps
	// working even when a page had to be shrunk to fit the byte budget.
	truncated := false
	for !fitsBudget(out, BudgetGraphHistory) && len(out.Entries) > 0 {
		out.Entries = out.Entries[:len(out.Entries)-1]
		truncated = true
	}
	out.Truncated = truncated

	return okResult(out), nil
}
