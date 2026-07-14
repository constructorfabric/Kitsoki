// graph-mcp-plan.md §3.3 P1 read ops (Workstream A): host.graph.get/find/
// neighbors/type_census/changeset. Every op resolves edges exclusively
// through Node.EdgeTargets against the registry decl (never the raw Edges
// map) per the plan's spec rule — see internal/graph/neighbors.go's
// BuildReverseIndex/Neighbors, which this file is the sole caller of on the
// host side.
package host

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	objectgraph "kitsoki/internal/graph"
)

const maxGetIDs = 20

// graphGetOp: {catalog_path, ids[1..20], fields?} -> full node envelopes
// (id, type_id, title, status, visibility, schema, sources, fields, edges
// via EdgeTargets, refs_in ALWAYS computed). Unknown ids land in a
// "missing" list with nearest-id suggestions.
func graphGetOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}
	ids, err := graphStringListArg(args, "ids")
	if err != nil {
		return Result{}, err
	}
	if len(ids) == 0 {
		return Result{}, fmt.Errorf("host.graph.get: requires at least 1 id in %q", "ids")
	}
	if len(ids) > maxGetIDs {
		return Result{}, fmt.Errorf("host.graph.get: %q accepts at most %d ids, got %d", "ids", maxGetIDs, len(ids))
	}

	var fieldsFilter map[string]bool
	if rawFields, err := graphStringListArg(args, "fields"); err == nil && len(rawFields) > 0 {
		fieldsFilter = make(map[string]bool, len(rawFields))
		for _, f := range rawFields {
			fieldsFilter[f] = true
		}
	}

	revIdx := objectgraph.BuildReverseIndex(cat)

	nodes := []any{}
	missing := []any{}
	for _, id := range ids {
		node, ok := cat.Nodes[objectgraph.NodeID(id)]
		if !ok {
			entry := map[string]any{
				"id":          id,
				"suggestions": stringSliceToAny(objectgraph.NearestIDs(cat, id, 3)),
			}
			// The node exists in the full catalog but was pruned by this
			// session's baked scope: say so, instead of pretending the id
			// is a typo. NearestIDs above already only suggests in-scope
			// ids (it ran against the pruned view).
			if cat.Scope != nil && cat.Scope.Excluded[objectgraph.NodeID(id)] {
				entry["out_of_scope"] = true
			}
			missing = append(missing, entry)
			continue
		}
		nodes = append(nodes, graphNodeEnvelope(cat, node, revIdx, fieldsFilter))
	}

	return Result{Data: map[string]any{
		"nodes":   nodes,
		"missing": missing,
	}}, nil
}

// graphNodeEnvelope builds one node's full-envelope wire shape, shared by
// get and (for its summary rows) find.
func graphNodeEnvelope(cat *objectgraph.Catalog, node *objectgraph.Node, revIdx objectgraph.ReverseIndex, fieldsFilter map[string]bool) map[string]any {
	sources := make([]any, 0, len(node.Sources))
	for _, s := range node.Sources {
		sources = append(sources, string(s))
	}

	fields := node.Fields
	if fieldsFilter != nil {
		filtered := make(map[string]any, len(fieldsFilter))
		for k := range fieldsFilter {
			if v, ok := node.Fields[k]; ok {
				filtered[k] = v
			}
		}
		fields = filtered
	}

	edges := map[string]any{}
	if eff, ok := cat.Registry.Effective(node.TypeID); ok {
		for _, decl := range eff.EdgeFields {
			targets := node.EdgeTargets(decl)
			if len(targets) == 0 {
				continue
			}
			ts := make([]any, len(targets))
			for i, t := range targets {
				ts[i] = string(t)
			}
			edges[string(decl.ID)] = ts
		}
	}

	refsIn := make([]any, 0, len(revIdx[node.ID]))
	for _, ref := range revIdx[node.ID] {
		refsIn = append(refsIn, map[string]any{
			"node":       string(ref.Node),
			"edge_field": string(ref.EdgeField),
		})
	}

	return map[string]any{
		"id":         string(node.ID),
		"type_id":    node.TypeID,
		"schema":     string(node.Schema),
		"title":      node.Title,
		"status":     node.Status,
		"visibility": string(node.Visibility),
		"sources":    sources,
		"fields":     fields,
		"edges":      edges,
		"refs_in":    refsIn,
	}
}

// graphNodeSummary builds the compact id/type/status/title row find's
// default (summary) view returns.
func graphNodeSummary(node *objectgraph.Node) map[string]any {
	return map[string]any{
		"id":     string(node.ID),
		"type":   node.TypeID,
		"status": node.Status,
		"title":  node.Title,
	}
}

// graphFindOp: {catalog_path, type?, status?[], visibility?, edge?{field,to},
// no_inbound?{edge}, no_outbound?{edge}, field?{key,equals?,contains?},
// text?, limit?=25, offset?, count_only?} -> {total, rows, truncated}.
// Deterministic ordering: catalog id order (cat.SortedNodeIDs()).
func graphFindOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}

	typeFilter := graphStringArg(args, "type")
	statusFilter, err := graphStringListArg(args, "status")
	if err != nil {
		return Result{}, err
	}
	statusSet := make(map[string]bool, len(statusFilter))
	for _, s := range statusFilter {
		statusSet[s] = true
	}
	visFilter := graphStringArg(args, "visibility")

	edgeFilter, _ := args["edge"].(map[string]any)
	noInbound, _ := args["no_inbound"].(map[string]any)
	noOutbound, _ := args["no_outbound"].(map[string]any)
	fieldFilter, _ := args["field"].(map[string]any)
	textFilter := graphStringArg(args, "text")
	countOnly := graphBoolArg(args, "count_only")

	limit := 25
	if rawLimit, ok := args["limit"]; ok {
		n, err := graphIntArg(rawLimit)
		if err != nil {
			return Result{}, fmt.Errorf("host.graph.find: %q must be an integer: %w", "limit", err)
		}
		limit = n
	}
	if limit < 0 {
		return Result{}, fmt.Errorf("host.graph.find: %q must be >= 0, got %d", "limit", limit)
	}
	offset := 0
	if rawOffset, ok := args["offset"]; ok {
		n, err := graphIntArg(rawOffset)
		if err != nil {
			return Result{}, fmt.Errorf("host.graph.find: %q must be an integer: %w", "offset", err)
		}
		offset = n
	}
	if offset < 0 {
		return Result{}, fmt.Errorf("host.graph.find: %q must be >= 0, got %d", "offset", offset)
	}

	var revIdx objectgraph.ReverseIndex
	if noInbound != nil || noOutbound != nil {
		revIdx = objectgraph.BuildReverseIndex(cat)
	}

	var matches []*objectgraph.Node
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]

		if typeFilter != "" && !cat.Registry.IsA(node.TypeID, typeFilter) {
			continue
		}
		if len(statusSet) > 0 && !statusSet[node.Status] {
			continue
		}
		if visFilter != "" && string(node.Visibility) != visFilter {
			continue
		}
		if edgeFilter != nil && !graphMatchesEdgeFilter(cat, node, edgeFilter) {
			continue
		}
		if noInbound != nil && !graphMatchesNoInbound(revIdx, node, noInbound) {
			continue
		}
		if noOutbound != nil && !graphMatchesNoOutbound(cat, node, noOutbound) {
			continue
		}
		if fieldFilter != nil && !graphMatchesFieldFilter(node, fieldFilter) {
			continue
		}
		if textFilter != "" && !graphMatchesText(node, textFilter) {
			continue
		}
		matches = append(matches, node)
	}

	total := len(matches)
	if countOnly {
		return Result{Data: map[string]any{"total": total, "rows": []any{}, "truncated": false}}, nil
	}

	if offset > total {
		offset = total
	}
	// Compute end without offset+limit arithmetic: both offset and limit are
	// caller-controlled (offset via an opaque-but-unsigned pagination cursor,
	// limit via the tool arg) and can be adversarially large, so a naive
	// `offset + limit` can overflow int and wrap negative, producing an
	// invalid slice expression below. Comparing against `remaining` (which is
	// bounded by total, not attacker input) avoids the overflow entirely.
	remaining := total - offset
	end := total
	truncated := false
	if limit < remaining {
		end = offset + limit
		truncated = true
	}
	page := matches[offset:end]

	rows := make([]any, len(page))
	for i, node := range page {
		rows[i] = graphNodeSummary(node)
	}

	return Result{Data: map[string]any{
		"total":     total,
		"rows":      rows,
		"truncated": truncated,
	}}, nil
}

func graphMatchesEdgeFilter(cat *objectgraph.Catalog, node *objectgraph.Node, filter map[string]any) bool {
	field, _ := filter["field"].(string)
	to, _ := filter["to"].(string)
	if field == "" {
		return false
	}
	eff, ok := cat.Registry.Effective(node.TypeID)
	if !ok {
		return false
	}
	for _, decl := range eff.EdgeFields {
		if string(decl.ID) != field {
			continue
		}
		targets := node.EdgeTargets(decl)
		if to == "" {
			return len(targets) > 0
		}
		for _, t := range targets {
			if string(t) == to {
				return true
			}
		}
		return false
	}
	return false
}

func graphMatchesNoInbound(revIdx objectgraph.ReverseIndex, node *objectgraph.Node, filter map[string]any) bool {
	edge, _ := filter["edge"].(string)
	refs := revIdx[node.ID]
	if edge == "" {
		return len(refs) == 0
	}
	for _, ref := range refs {
		if string(ref.EdgeField) == edge {
			return false
		}
	}
	return true
}

func graphMatchesNoOutbound(cat *objectgraph.Catalog, node *objectgraph.Node, filter map[string]any) bool {
	edge, _ := filter["edge"].(string)
	eff, ok := cat.Registry.Effective(node.TypeID)
	if !ok {
		return true
	}
	for _, decl := range eff.EdgeFields {
		if edge != "" && string(decl.ID) != edge {
			continue
		}
		if len(node.EdgeTargets(decl)) > 0 {
			return false
		}
	}
	return true
}

func graphMatchesFieldFilter(node *objectgraph.Node, filter map[string]any) bool {
	key, _ := filter["key"].(string)
	if key == "" {
		return false
	}
	val, ok := node.Fields[key]
	if !ok {
		return false
	}
	if equals, has := filter["equals"]; has {
		return fmt.Sprint(val) == fmt.Sprint(equals)
	}
	if contains, has := filter["contains"].(string); has {
		return containsSubstring(fmt.Sprint(val), contains)
	}
	return true
}

func graphMatchesText(node *objectgraph.Node, text string) bool {
	if containsSubstring(string(node.ID), text) || containsSubstring(node.Title, text) {
		return true
	}
	for _, v := range node.Fields {
		if containsSubstring(fmt.Sprint(v), text) {
			return true
		}
	}
	return false
}

// graphNeighborsOp: {catalog_path, id, direction?=both, edges?[], depth?1..3=1,
// limit?} -> edge triples + summary rows (internal/graph.Neighbors).
func graphNeighborsOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}
	id := graphStringArg(args, "id")
	if id == "" {
		return Result{}, fmt.Errorf("host.graph.neighbors: missing required arg %q", "id")
	}
	if _, ok := cat.Nodes[objectgraph.NodeID(id)]; !ok {
		if cat.Scope != nil && cat.Scope.Excluded[objectgraph.NodeID(id)] {
			return Result{}, fmt.Errorf("host.graph.neighbors: node %q is out of scope for this session's baked graph scope", id)
		}
		return Result{}, fmt.Errorf("host.graph.neighbors: unknown node %q (nearest: %v)", id, objectgraph.NearestIDs(cat, id, 3))
	}

	direction := objectgraph.NeighborsDirection(graphStringArg(args, "direction"))
	if direction == "" {
		direction = objectgraph.DirectionBoth
	}
	if direction != objectgraph.DirectionIn && direction != objectgraph.DirectionOut && direction != objectgraph.DirectionBoth {
		return Result{}, fmt.Errorf("host.graph.neighbors: %q must be one of in, out, both, got %q", "direction", direction)
	}

	edgeFieldStrs, err := graphStringListArg(args, "edges")
	if err != nil {
		return Result{}, err
	}
	if len(edgeFieldStrs) > 0 {
		known := graphAllEdgeFieldIDs(cat)
		knownSet := make(map[string]bool, len(known))
		for _, k := range known {
			knownSet[k] = true
		}
		for _, e := range edgeFieldStrs {
			if !knownSet[e] {
				return Result{}, fmt.Errorf("host.graph.neighbors: unknown edge %q (known: %v)", e, known)
			}
		}
	}
	edgeKinds := make([]objectgraph.EdgeField, len(edgeFieldStrs))
	for i, e := range edgeFieldStrs {
		edgeKinds[i] = objectgraph.EdgeField(e)
	}

	depth := 1
	if rawDepth, ok := args["depth"]; ok {
		n, err := graphIntArg(rawDepth)
		if err != nil {
			return Result{}, fmt.Errorf("host.graph.neighbors: %q must be an integer: %w", "depth", err)
		}
		depth = n
	}
	if depth < 1 || depth > 3 {
		return Result{}, fmt.Errorf("host.graph.neighbors: %q must be 1..3, got %d", "depth", depth)
	}

	limit := 0
	if rawLimit, ok := args["limit"]; ok {
		n, err := graphIntArg(rawLimit)
		if err != nil {
			return Result{}, fmt.Errorf("host.graph.neighbors: %q must be an integer: %w", "limit", err)
		}
		limit = n
	}

	triples := objectgraph.Neighbors(cat, objectgraph.NodeID(id), direction, edgeKinds, depth, limit)

	wireTriples := make([]any, len(triples))
	seen := map[objectgraph.NodeID]bool{}
	var summaryIDs []objectgraph.NodeID
	for i, tr := range triples {
		wireTriples[i] = map[string]any{
			"from":      string(tr.From),
			"edge":      string(tr.EdgeField),
			"to":        string(tr.To),
			"direction": tr.Direction,
			"depth":     tr.Depth,
		}
		other := tr.To
		if tr.Direction == string(objectgraph.DirectionIn) {
			other = tr.From
		}
		if !seen[other] {
			seen[other] = true
			summaryIDs = append(summaryIDs, other)
		}
	}
	sort.Slice(summaryIDs, func(i, j int) bool { return summaryIDs[i] < summaryIDs[j] })

	rows := make([]any, 0, len(summaryIDs))
	for _, nid := range summaryIDs {
		if node, ok := cat.Nodes[nid]; ok {
			rows = append(rows, graphNodeSummary(node))
		}
	}

	return Result{Data: map[string]any{
		"triples": wireTriples,
		"rows":    rows,
	}}, nil
}

// graphTypeCensusOp: {catalog_path, type_id?} -> the full type decl (+
// subtype-inclusive instance_count) when type_id is given, or a one-line
// census of every registered type otherwise. Backs graph.open/graph.type
// later (P2) — see graph_handlers.go's graphQueryExplainType for the shared
// type-decl shape this reuses.
func graphTypeCensusOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}

	typeID := graphStringArg(args, "type_id")
	if typeID != "" {
		res, err := graphQueryExplainType(cat, typeID)
		if err != nil {
			return Result{}, err
		}
		count := 0
		for _, id := range cat.SortedNodeIDs() {
			if cat.Registry.IsA(cat.Nodes[id].TypeID, typeID) {
				count++
			}
		}
		res.Data["instance_count"] = count
		res.Data["status_breakdown"] = graphStatusBreakdown(cat, typeID)
		return res, nil
	}

	defs := cat.Registry.All()
	rows := make([]any, 0, len(defs))
	for _, def := range defs {
		if def.ID == "core-node" {
			continue
		}
		count := 0
		for _, id := range cat.SortedNodeIDs() {
			if cat.Registry.IsA(cat.Nodes[id].TypeID, def.ID) {
				count++
			}
		}
		rows = append(rows, map[string]any{
			"id":             def.ID,
			"summary":        def.Summary,
			"extends":        nilIfEmpty(def.Extends),
			"instance_count": count,
		})
	}
	return Result{Data: map[string]any{"types": rows}}, nil
}

// graphStatusBreakdown counts, per status value, how many IsA(typeID)
// instances carry it — deterministic key order via a sorted-keys pass at
// the caller/marshal boundary is not needed here since callers consume the
// map by key, not by iterating it for wire order (JSON marshal already
// sorts map[string]any keys).
func graphStatusBreakdown(cat *objectgraph.Catalog, typeID string) map[string]any {
	counts := map[string]any{}
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		if !cat.Registry.IsA(node.TypeID, typeID) {
			continue
		}
		cur, _ := counts[node.Status].(int)
		counts[node.Status] = cur + 1
	}
	return counts
}

// graphChangesetOp: {catalog_path, action: list|get|touching, changeset_id?,
// node_id?} -> list-mode returns every changeset node's lifecycle summary +
// status counts; get-mode returns one changeset's parsed operations;
// touching-mode reverse-indexes changesets whose operations reference
// node_id.
func graphChangesetOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}
	action := graphStringArg(args, "action")

	var changesetIDs []objectgraph.NodeID
	for _, id := range cat.SortedNodeIDs() {
		if cat.Nodes[id].TypeID == "changeset" {
			changesetIDs = append(changesetIDs, id)
		}
	}

	switch action {
	case "list":
		rows := make([]any, 0, len(changesetIDs))
		statusCounts := map[string]any{}
		for _, id := range changesetIDs {
			node := cat.Nodes[id]
			rows = append(rows, map[string]any{
				"id":     string(id),
				"title":  node.Title,
				"status": node.Status,
			})
			cur, _ := statusCounts[node.Status].(int)
			statusCounts[node.Status] = cur + 1
		}
		return Result{Data: map[string]any{
			"changesets":    rows,
			"status_counts": statusCounts,
		}}, nil

	case "get":
		changesetID := graphStringArg(args, "changeset_id")
		if changesetID == "" {
			return Result{}, fmt.Errorf("host.graph.changeset: action %q requires %q", "get", "changeset_id")
		}
		node, ok := cat.Nodes[objectgraph.NodeID(changesetID)]
		if !ok || node.TypeID != "changeset" {
			return Result{}, fmt.Errorf("host.graph.changeset: no changeset %q", changesetID)
		}
		cs, err := objectgraph.ParseChangeset(node)
		if err != nil {
			return Result{}, err
		}
		ops := make([]any, len(cs.Operations))
		for i, op := range cs.Operations {
			ops[i] = graphOperationWire(op)
		}
		return Result{Data: map[string]any{
			"id":         string(node.ID),
			"title":      node.Title,
			"status":     node.Status,
			"operations": ops,
		}}, nil

	case "touching":
		nodeID := graphStringArg(args, "node_id")
		if nodeID == "" {
			return Result{}, fmt.Errorf("host.graph.changeset: action %q requires %q", "touching", "node_id")
		}
		touching := []any{}
		for _, id := range changesetIDs {
			node := cat.Nodes[id]
			cs, err := objectgraph.ParseChangeset(node)
			if err != nil {
				continue
			}
			for _, op := range cs.Operations {
				if graphOperationTouches(op, objectgraph.NodeID(nodeID)) {
					touching = append(touching, map[string]any{
						"id":     string(id),
						"title":  node.Title,
						"status": node.Status,
					})
					break
				}
			}
		}
		return Result{Data: map[string]any{"touching": touching}}, nil

	default:
		return Result{}, fmt.Errorf("host.graph.changeset: unknown action %q (want one of list, get, touching)", action)
	}
}

// graphOperationTouches reports whether op references nodeID — for renamed
// ops, either endpoint counts as "touching".
func graphOperationTouches(op objectgraph.Operation, nodeID objectgraph.NodeID) bool {
	switch op.Kind {
	case objectgraph.OpRenamed:
		return op.From == nodeID || op.To == nodeID
	default:
		return op.Node == nodeID
	}
}

// graphOperationWire builds one changeset operation's wire shape for
// graph.changeset's get action.
func graphOperationWire(op objectgraph.Operation) map[string]any {
	out := map[string]any{"kind": string(op.Kind)}
	if op.Node != "" {
		out["node"] = string(op.Node)
	}
	if op.After != nil {
		out["after"] = op.After
	}
	if op.Before != nil {
		out["before"] = op.Before
	}
	if len(op.Changes) > 0 {
		changes := make([]any, len(op.Changes))
		for i, c := range op.Changes {
			changes[i] = map[string]any{
				"path":   c.Path,
				"before": c.Before,
				"after":  c.After,
			}
		}
		out["changes"] = changes
	}
	if op.From != "" {
		out["from"] = string(op.From)
	}
	if op.To != "" {
		out["to"] = string(op.To)
	}
	if op.FromType != "" {
		out["from_type"] = op.FromType
	}
	if op.ToType != "" {
		out["to_type"] = op.ToType
	}
	return out
}

// ─── small arg-parsing helpers shared by the new ops ───

// graphStringListArg reads args[key] as a list of strings — accepting
// []any (the JSON/starlark-decoded shape) or []string (a direct Go-side
// caller) — returning an error if any element isn't a string.
func graphStringListArg(args map[string]any, key string) ([]string, error) {
	raw, ok := args[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("host.graph: %q[%d] must be a string, got %T", key, i, item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("host.graph: %q must be a list of strings, got %T", key, raw)
	}
}

// graphIntArg coerces a decoded arg value (float64 from JSON, int from a
// direct Go caller) into an int.
func graphIntArg(raw any) (int, error) {
	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("unsupported type %T", raw)
	}
}

func stringSliceToAny(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// containsSubstring is a case-sensitive substring check — text/field-contains
// matching is deliberately simple and deterministic (no fuzzy/LLM matching),
// per the plan's §3.3 read-family contract.
func containsSubstring(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(haystack, needle)
}

// graphOpenOp: {catalog_path} -> a catalog overview composing head
// {rev,dirty}, node_count, per-type census (instance count + status
// breakdown + one-line edge vocabulary), lint {clean,count}, changeset
// lifecycle counts, a feedback {pending} stub, and a short guide string.
// graph-mcp-plan.md §3.3: this is the ONE read tool with no pre-existing
// engine op, so it lands here as a real host.graph.* op (never MCP-handler-
// only logic) per the plan's "all capability lands as engine ops"
// constraint. Byte-budget enforcement (≤2KB) and in-band truncation are the
// MCP layer's job (internal/mcp/graphsrv), matching how graph.lint already
// splits "engine composes full data, MCP tool truncates for the wire" — this
// op always returns the full composition.
func graphOpenOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}
	catalogPath := graphStringArg(args, "catalog_path")

	rev, dirty := graphHeadInfo(catalogPath)

	issues := objectgraph.Lint(cat)
	lint := map[string]any{"clean": len(issues) == 0, "count": len(issues)}

	guide := graphOpenGuide
	data := map[string]any{
		"head":       map[string]any{"rev": rev, "dirty": dirty},
		"node_count": len(cat.Nodes),
		"types":      graphOpenTypeCensus(cat),
		"lint":       lint,
		"changesets": graphChangesetStatusCounts(cat),
		// feedback.pending is a stub until feedback.report/feedback.list
		// (graph-mcp-plan.md §3.6) land the local sink this would read
		// from; kept at 0 rather than omitted so callers can rely on the
		// field's presence/shape now.
		"feedback": map[string]any{"pending": 0},
	}
	if cat.Scope != nil {
		data["scope"] = graphScopeWire(cat.Scope)
		guide += "\n" + fmt.Sprintf("This session is SCOPED: %d of %d catalog nodes are visible; everything else reads as out of scope and cannot be modified. The scope was baked in at server start and no argument changes it.",
			cat.Scope.MemberCount, cat.Scope.TotalNodes)
	}
	data["guide"] = guide
	return Result{Data: data}, nil
}

// graphScopeWire renders the scope block graph.open's overview carries when
// the session was constructed with a baked scope: enough for an agent to
// understand what it can and cannot see, without re-deriving the member set.
func graphScopeWire(info *objectgraph.ScopeInfo) map[string]any {
	out := map[string]any{
		"active":           true,
		"member_count":     info.MemberCount,
		"total_node_count": info.TotalNodes,
		"pruned_edges":     info.PrunedEdges,
	}
	if info.Spec != nil {
		out["spec"] = info.Spec.WireMap()
	}
	return out
}

// graphOpenGuide is graph.open's ~6-line orientation string.
const graphOpenGuide = `This catalog is served by kitsoki mcp-graph (read-only in this session).
Use graph.find to search by type/status/field, graph.get to fetch full
node envelopes by id, graph.neighbors to walk edges from a node.
graph.type explains a type's schema and edge vocabulary; graph.impact
predicts what a retype/remove would break — call it before proposing one.
Stuck or missing something? Call feedback.report — it's non-blocking.`

// graphOpenTypeCensus builds one census row per registered type (excluding
// the synthetic core-node root): instance_count, a status->count breakdown,
// and a one-line edge vocabulary summary. Rows are in Registry.All()'s
// deterministic (sorted-by-ID) order.
func graphOpenTypeCensus(cat *objectgraph.Catalog) []any {
	defs := cat.Registry.All()
	rows := make([]any, 0, len(defs))
	for _, def := range defs {
		if def.ID == "core-node" {
			continue
		}
		count := 0
		statuses := map[string]any{}
		for _, id := range cat.SortedNodeIDs() {
			node := cat.Nodes[id]
			if !cat.Registry.IsA(node.TypeID, def.ID) {
				continue
			}
			count++
			cur, _ := statuses[node.Status].(int)
			statuses[node.Status] = cur + 1
		}
		rows = append(rows, map[string]any{
			"id":             def.ID,
			"instance_count": count,
			"statuses":       statuses,
			"edges":          graphEdgeVocabLine(def),
		})
	}
	return rows
}

// graphAllEdgeFieldIDs returns the sorted, deduplicated union of every edge
// field ID declared across the whole type registry — the vocabulary
// UNKNOWN_EDGE errors list (graph.neighbors' `edges` filter names a field
// that must exist somewhere in the registry, not necessarily on the root
// node's own type, since direction="in"/"both" can walk edges declared on
// other types that merely point at the root).
func graphAllEdgeFieldIDs(cat *objectgraph.Catalog) []string {
	seen := map[string]bool{}
	for _, def := range cat.Registry.All() {
		for _, f := range def.EdgeFields {
			seen[string(f.ID)] = true
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// graphEdgeVocabLine renders a type's declared edge fields as one
// comma-joined "field->target_type(cardinality)" line, e.g.
// "acceptance->usecase(many)". Empty string when the type declares no edge
// fields.
func graphEdgeVocabLine(def objectgraph.TypeDef) string {
	if len(def.EdgeFields) == 0 {
		return ""
	}
	parts := make([]string, len(def.EdgeFields))
	for i, f := range def.EdgeFields {
		parts[i] = fmt.Sprintf("%s->%s(%s)", f.ID, f.TargetType, f.Cardinality)
	}
	return strings.Join(parts, ", ")
}

// graphChangesetStatusCounts counts changeset nodes by status — the
// lifecycle-count half of graph.open, factored out of graphChangesetOp's
// "list" action so both share one counting pass instead of drifting.
func graphChangesetStatusCounts(cat *objectgraph.Catalog) map[string]any {
	counts := map[string]any{}
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		if node.TypeID != "changeset" {
			continue
		}
		cur, _ := counts[node.Status].(int)
		counts[node.Status] = cur + 1
	}
	return counts
}

// graphHeadInfo best-effort resolves the git HEAD short SHA and dirty state
// of the repo containing catalogPath. Both return values are "" / false on
// any failure (not a git repo, git not on PATH, etc.) — head/dirty are
// informational overview fields, never a hard requirement for graph.open to
// succeed.
func graphHeadInfo(catalogPath string) (rev string, dirty bool) {
	dir := catalogPath
	if fi, err := os.Stat(catalogPath); err == nil && !fi.IsDir() {
		dir = filepath.Dir(catalogPath)
	}
	revOut, err := exec.Command("git", "-C", dir, "rev-parse", "--short", "HEAD").Output()
	if err != nil {
		return "", false
	}
	rev = strings.TrimSpace(string(revOut))
	statusOut, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return rev, false
	}
	dirty = strings.TrimSpace(string(statusOut)) != ""
	return rev, dirty
}
