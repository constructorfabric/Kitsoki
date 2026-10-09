// Package host — host.graph.* (S5, .context/kits-implementation-plan.md D1/
// D4): the project object graph engine substrate (internal/graph,
// self-contained envelope/registry/lint/diff/apply) exposed as a generic
// host verb, so kits can build graph-shaped domains (type pack, endpoints,
// UI, conformance) without any Go of their own — the "generic capabilities a
// kit needs from the engine are host verbs" framing (D1).
//
// GraphHandler is registered once, bare, at "host.graph" (not per-op) so the
// registry's longest-prefix fallback (Registry.Get) resolves every
// "host.graph.<op>" call here with <op> injected into args["op"] — the same
// multi-op convention host.git/host.local_files.ticket already use. Six ops:
//
//	load         {catalog_path[, overlay_path]}                 -> raw catalog summary
//	lint         {catalog_path}                                 -> lint issues
//	diff         {catalog_path, overlay_path}                   -> node-level diff classification
//	apply        {catalog_path, changeset_id[, dry_run]}         -> apply result
//	project      {catalog_path[, overlay_path], graph_id}        -> kitsoki.graph/v1 wire graph
//	presentation {...; kit-injected _kit_dir}                    -> starlark-served presentation data
//	open         {catalog_path}                                  -> catalog overview (graph-mcp-plan.md §3.3 graph.open)
//	get          {catalog_path, ids[1..20][, fields]}             -> full node envelopes + refs_in
//	find         {catalog_path[, type, status, visibility, edge,
//	              no_inbound, no_outbound, field, text, limit,
//	              offset, count_only]}                            -> {total, rows, truncated}
//	neighbors    {catalog_path, id[, direction, edges, depth,
//	              limit]}                                         -> edge triples + summary rows
//	type_census  {catalog_path[, type_id]}                        -> type decl+census, or all-types census
//	changeset    {catalog_path, action: list|get|touching[,
//	              changeset_id, node_id]}                         -> changeset lifecycle/reverse-index reads
//	history      {catalog_path[, id, since, limit, cursor]}       -> merged changeset+git timeline (graph-mcp-plan.md §3.5 graph.history)
//
// get/find/neighbors/type_census/changeset are internal/host/graph_read_ops.go
// — graph-mcp-plan.md §3.3's P1 read family (Workstream A). Every one of
// them resolves edges exclusively through Node.EdgeTargets against the
// registry decl, never the raw Edges map, so storage:top_level edge fields
// (e.g. a change node's depends_on) are never silently invisible.
//
// Deleted on extraction: internal/app/graph/objectcatalog.go — its
// ObjectCatalogGraph/ObjectCatalogDiffGraph logic now lives inline in the
// "project" op below (graphProjectOp), which is what "moves behind
// host.graph.project" means in the D4 split table: the WIRE-GRAPH ADAPTER
// logic relocates into the engine's generic host verb; only the
// object-graph-SPECIFIC UI/endpoint declarations move to the kit.
//
// "presentation" is the concrete "endpoint handled by starlark" proof (D2.1):
// it is NOT a Go implementation living in the engine (that would put
// kit-specific domain data — the object-graph kit's layer taxonomy — inside
// engine code, violating D1's "a kit ships data + declarations only, never
// Go"). Instead, graphPresentationOp resolves and runs
// <kit-dir>/scripts/presentation.star via the existing S3a
// StarlarkBindingHandler mechanism. <kit-dir> arrives via args["_kit_dir"],
// which internal/kitendpoint.Dispatcher injects into every kit.<kit>.<iface>.
// <op> call (see kitendpoint/dispatch.go) — a small, deliberately generic
// convention ("kit context is available to any op that wants kit-relative
// paths"), not a mechanism special-cased to object-graph. A direct
// (non-kit-dispatched) call to host.graph.presentation without _kit_dir
// fails with a clear error rather than silently doing nothing.
package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	appgraph "kitsoki/internal/app/graph"
	"kitsoki/internal/clock"
	objectgraph "kitsoki/internal/graph"
)

// getenvGraphClockFixed is a tiny indirection seam (matching
// internal/host/diff_open.go's getenvDifftool pattern) so tests can pin
// KITSOKI_GRAPH_CLOCK_FIXED without depending on the real process
// environment.
var getenvGraphClockFixed = func() string { return os.Getenv("KITSOKI_GRAPH_CLOCK_FIXED") }

// graphResolveClock resolves the effective clock a host.graph write op
// (propose/authorize/apply/withdraw) stamps with: KITSOKI_GRAPH_CLOCK_FIXED
// (an RFC3339 timestamp), parsed once per call and erroring on an invalid
// value, wins when set; otherwise the ctx-injected clock (ClockFromContext,
// which itself defaults to clock.Real()) is used. This keeps gate checks
// deterministic (plan's "no wall-clock nondeterminism in gates" constraint)
// without requiring every caller to inject a fake clock via ctx.
func graphResolveClock(ctx context.Context) (clock.Clock, error) {
	if fixed := getenvGraphClockFixed(); fixed != "" {
		t, err := time.Parse(time.RFC3339, fixed)
		if err != nil {
			return nil, fmt.Errorf("KITSOKI_GRAPH_CLOCK_FIXED=%q: invalid RFC3339 timestamp: %w", fixed, err)
		}
		return clock.NewFake(t.UTC()), nil
	}
	return ClockFromContext(ctx), nil
}

// GraphHandler implements the host.graph.* multi-op verb (S5).
func GraphHandler(ctx context.Context, args map[string]any) (Result, error) {
	op, _ := args["op"].(string)
	switch op {
	case "load":
		return graphLoadOp(args)
	case "lint":
		return graphLintOp(args)
	case "diff":
		return graphDiffOp(args)
	case "apply":
		return graphApplyOp(ctx, args)
	case "propose":
		return graphProposeOp(ctx, args)
	case "authorize":
		return graphAuthorizeOp(ctx, args)
	case "withdraw":
		return graphWithdrawOp(ctx, args)
	case "rebase":
		return graphRebaseOp(ctx, args)
	case "query":
		return graphQueryOp(args)
	case "project":
		return graphProjectOp(args)
	case "presentation":
		return graphPresentationOp(ctx, args)
	case "open":
		return graphOpenOp(args)
	case "get":
		return graphGetOp(args)
	case "find":
		return graphFindOp(args)
	case "neighbors":
		return graphNeighborsOp(args)
	case "type_census":
		return graphTypeCensusOp(args)
	case "changeset":
		return graphChangesetOp(args)
	case "history":
		return graphHistoryOp(ctx, args)
	default:
		return Result{}, fmt.Errorf("host.graph: unknown op %q (want one of load, lint, diff, apply, propose, authorize, withdraw, rebase, query, project, presentation, open, get, find, neighbors, type_census, changeset, history)", op)
	}
}

func graphStringArg(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

func graphBoolArg(args map[string]any, key string) bool {
	b, _ := args[key].(bool)
	return b
}

// loadCatalogArg loads catalog_path (required), optionally unioning
// overlay_path (LoadCatalogWithOverlay) when present — the shared "load
// current, optionally desired" step every read-only op needs. When args
// carries a `scope` mapping (a session's baked catalog subset, see
// graph_scope.go), the loaded catalog is pruned to that scope's member set
// — the single choke point that makes every read op scope-aware at once.
// Write ops never load through here (they hand catalog_path to
// objectgraph.Propose/Apply/... directly), so a pruned view can never be
// written back to disk.
func loadCatalogArg(args map[string]any) (*objectgraph.Catalog, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	if catalogPath == "" {
		return nil, fmt.Errorf("host.graph: missing required arg %q", "catalog_path")
	}
	var cat *objectgraph.Catalog
	var err error
	if overlay := graphStringArg(args, "overlay_path"); overlay != "" {
		cat, err = objectgraph.LoadCatalogWithOverlay(catalogPath, overlay)
	} else {
		cat, err = objectgraph.LoadCatalog(catalogPath)
	}
	if err != nil {
		return nil, err
	}
	spec, err := graphScopeSpecArg(args)
	if err != nil {
		return nil, err
	}
	if spec != nil {
		return objectgraph.ApplyScope(cat, spec)
	}
	return cat, nil
}

// graphLoadOp: {catalog_path[, overlay_path]} -> a raw catalog summary (node
// count + warnings) — the low-level "does this load cleanly" primitive the
// CLI's `kitsoki graph lint` and conformance flows can build on.
func graphLoadOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}
	ids := cat.SortedNodeIDs()
	nodeIDs := make([]any, len(ids))
	for i, id := range ids {
		nodeIDs[i] = string(id)
	}
	warnings := make([]any, len(cat.Warnings))
	for i, w := range cat.Warnings {
		warnings[i] = w
	}
	return Result{Data: map[string]any{
		"node_count": len(cat.Nodes),
		"node_ids":   nodeIDs,
		"warnings":   warnings,
	}}, nil
}

// graphLintOp: {catalog_path} -> lint issues (internal/graph.Lint).
func graphLintOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}
	issues := objectgraph.Lint(cat)
	out := make([]any, len(issues))
	for i, iss := range issues {
		out[i] = map[string]any{
			"node":     string(iss.Node),
			"kind":     iss.Kind,
			"severity": string(iss.Severity),
			"message":  iss.Message,
		}
	}
	return Result{Data: map[string]any{"issues": out, "issue_count": len(issues), "clean": len(issues) == 0}}, nil
}

// graphDiffOp: {catalog_path, overlay_path} -> per-node gap classification
// (added/modified/removed/unchanged), via internal/graph.DiffNodes.
func graphDiffOp(args map[string]any) (Result, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	overlayPath := graphStringArg(args, "overlay_path")
	if catalogPath == "" || overlayPath == "" {
		return Result{}, fmt.Errorf("host.graph.diff: requires both catalog_path and overlay_path")
	}
	current, err := objectgraph.LoadCatalog(catalogPath)
	if err != nil {
		return Result{}, err
	}
	desired, err := objectgraph.LoadCatalogWithOverlay(catalogPath, overlayPath)
	if err != nil {
		return Result{}, err
	}
	diffs := objectgraph.DiffNodes(current, desired)
	out := make([]any, len(diffs))
	for i, d := range diffs {
		out[i] = map[string]any{"node": string(d.ID), "kind": string(d.Kind)}
	}
	return Result{Data: map[string]any{"nodes": out}}, nil
}

// graphApplyOp: {catalog_path, changeset_id[, dry_run]} -> internal/graph.Apply's
// result: a dry-run-first, all-or-nothing changeset application.
func graphApplyOp(ctx context.Context, args map[string]any) (Result, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	changesetID := graphStringArg(args, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return Result{}, fmt.Errorf("host.graph.apply: requires both catalog_path and changeset_id")
	}
	if err := graphScopeGuardChangeset("apply", catalogPath, changesetID, args); err != nil {
		return Result{}, err
	}
	clk, err := graphResolveClock(ctx)
	if err != nil {
		return Result{}, err
	}
	res, err := objectgraph.Apply(catalogPath, objectgraph.NodeID(changesetID), graphBoolArg(args, "dry_run"), ActorFromContext(ctx), clk)
	if err != nil {
		return Result{}, err
	}
	rejectReasons := make([]any, len(res.RejectReasons))
	for i, r := range res.RejectReasons {
		rejectReasons[i] = r
	}
	lintIssues := make([]any, len(res.LintIssues))
	for i, iss := range res.LintIssues {
		lintIssues[i] = iss.Error()
	}
	changedFiles := make([]any, len(res.ChangedFiles))
	for i, f := range res.ChangedFiles {
		changedFiles[i] = f
	}
	return Result{Data: map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}}, nil
}

// graphProposeOp: {catalog_path, title, operations[, visibility, provenance,
// validate_only]} -> {changeset_id, status, lint, rejected, reject_reasons,
// guard_fills, validated_only}. operations is the changeset wire shape: a
// list of {"kind": ..., ...} mappings (see internal/graph.ParseChangeset).
// provenance, when present AND the call arrived steward-trusted (see the
// provenance-strip comment below), marks the changeset system-authored for
// the D9 auto-authorize allowlist (internal/graph.Propose). validate_only
// runs full validation + scratch lint and writes nothing (hazard guard #5).
// guard_fills echoes every precondition Propose filled in on the caller's
// behalf because the wire payload omitted it.
func graphProposeOp(ctx context.Context, args map[string]any) (Result, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	if catalogPath == "" {
		return Result{}, fmt.Errorf("host.graph.propose: missing required arg %q", "catalog_path")
	}
	rawOps, _ := args["operations"].([]any)
	ops := make([]map[string]any, 0, len(rawOps))
	for _, r := range rawOps {
		if m, ok := r.(map[string]any); ok {
			ops = append(ops, m)
		}
	}
	if err := graphScopeGuardOps("propose", catalogPath, args, ops); err != nil {
		return Result{}, err
	}
	// Provenance strip (hazard guard #1, plan §3.4 red-team amendment #1):
	// caller-supplied provenance only ever reaches internal/graph.Propose —
	// and therefore only ever feeds the D9 auto-authorize allowlist check —
	// when this call arrived through a context explicitly marked
	// steward-trusted (WithSteward). Every other caller's provenance is
	// silently dropped here, not merely ignored downstream, so an untrusted
	// proposal can never carry provenance that triggers auto-authorize.
	var provenance map[string]any
	if StewardFromContext(ctx) {
		provenance, _ = args["provenance"].(map[string]any)
	}
	clk, err := graphResolveClock(ctx)
	if err != nil {
		return Result{}, err
	}
	res, err := objectgraph.Propose(catalogPath, objectgraph.ProposeInput{
		Title:        graphStringArg(args, "title"),
		Visibility:   graphStringArg(args, "visibility"),
		Operations:   ops,
		Provenance:   provenance,
		ValidateOnly: graphBoolArg(args, "validate_only"),
	}, ActorFromContext(ctx), clk)
	if err != nil {
		return Result{}, err
	}
	lintIssues := make([]any, len(res.Lint))
	for i, iss := range res.Lint {
		lintIssues[i] = iss.Error()
	}
	rejectReasons := make([]any, len(res.RejectReasons))
	for i, r := range res.RejectReasons {
		rejectReasons[i] = r
	}
	guardFills := make([]any, len(res.GuardFills))
	for i, gf := range res.GuardFills {
		entry := map[string]any{"node": string(gf.Node)}
		if len(gf.Path) > 0 {
			path := make([]any, len(gf.Path))
			for j, p := range gf.Path {
				path[j] = p
			}
			entry["path"] = path
			entry["value"] = gf.Value
		}
		if gf.SHA != "" {
			entry["sha"] = gf.SHA
			fields := make([]any, len(gf.Fields))
			for j, f := range gf.Fields {
				fields[j] = f
			}
			entry["fields"] = fields
		}
		guardFills[i] = entry
	}
	return Result{Data: map[string]any{
		"changeset_id":   string(res.ChangesetID),
		"status":         res.Status,
		"lint":           lintIssues,
		"rejected":       len(res.RejectReasons) > 0 || len(res.Lint) > 0,
		"guard_fills":    guardFills,
		"validated_only": res.ValidatedOnly,
		"reject_reasons": rejectReasons,
	}}, nil
}

// graphAuthorizeOp: {catalog_path, changeset_id} -> the proposed->authorized
// lifecycle flip (internal/graph.Authorize). Same rejected/reject_reasons/
// lint_issues/changed_files shape as graphApplyOp for a consistent client
// contract across the two lifecycle-writing ops.
func graphAuthorizeOp(ctx context.Context, args map[string]any) (Result, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	changesetID := graphStringArg(args, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return Result{}, fmt.Errorf("host.graph.authorize: requires both catalog_path and changeset_id")
	}
	if err := graphScopeGuardChangeset("authorize", catalogPath, changesetID, args); err != nil {
		return Result{}, err
	}
	clk, err := graphResolveClock(ctx)
	if err != nil {
		return Result{}, err
	}
	res, err := objectgraph.Authorize(catalogPath, objectgraph.NodeID(changesetID), ActorFromContext(ctx), clk)
	if err != nil {
		return Result{}, err
	}
	rejectReasons := make([]any, len(res.RejectReasons))
	for i, r := range res.RejectReasons {
		rejectReasons[i] = r
	}
	lintIssues := make([]any, len(res.LintIssues))
	for i, iss := range res.LintIssues {
		lintIssues[i] = iss.Error()
	}
	changedFiles := make([]any, len(res.ChangedFiles))
	for i, f := range res.ChangedFiles {
		changedFiles[i] = f
	}
	return Result{Data: map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}}, nil
}

// graphWithdrawOp: {catalog_path, changeset_id} -> the review queue's
// "clean up a rejected proposal" action (internal/graph.Withdraw). Same
// result shape as graphAuthorizeOp.
func graphWithdrawOp(ctx context.Context, args map[string]any) (Result, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	changesetID := graphStringArg(args, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return Result{}, fmt.Errorf("host.graph.withdraw: requires both catalog_path and changeset_id")
	}
	if err := graphScopeGuardChangeset("withdraw", catalogPath, changesetID, args); err != nil {
		return Result{}, err
	}
	clk, err := graphResolveClock(ctx)
	if err != nil {
		return Result{}, err
	}
	res, err := objectgraph.Withdraw(catalogPath, objectgraph.NodeID(changesetID), ActorFromContext(ctx), clk)
	if err != nil {
		return Result{}, err
	}
	rejectReasons := make([]any, len(res.RejectReasons))
	for i, r := range res.RejectReasons {
		rejectReasons[i] = r
	}
	lintIssues := make([]any, len(res.LintIssues))
	for i, iss := range res.LintIssues {
		lintIssues[i] = iss.Error()
	}
	changedFiles := make([]any, len(res.ChangedFiles))
	for i, f := range res.ChangedFiles {
		changedFiles[i] = f
	}
	return Result{Data: map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}}, nil
}

// graphRebaseOp: {catalog_path, changeset_id} -> the review queue's "rebase"
// action (internal/graph.Rebase). Same result shape as graphAuthorizeOp;
// same ctx-resolved actor/clock threading as graphAuthorizeOp/graphWithdrawOp
// (actor is accepted for seam consistency — Rebase itself stamps nothing).
func graphRebaseOp(ctx context.Context, args map[string]any) (Result, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	changesetID := graphStringArg(args, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return Result{}, fmt.Errorf("host.graph.rebase: requires both catalog_path and changeset_id")
	}
	if err := graphScopeGuardChangeset("rebase", catalogPath, changesetID, args); err != nil {
		return Result{}, err
	}
	clk, err := graphResolveClock(ctx)
	if err != nil {
		return Result{}, err
	}
	res, err := objectgraph.Rebase(catalogPath, objectgraph.NodeID(changesetID), ActorFromContext(ctx), clk)
	if err != nil {
		return Result{}, err
	}
	rejectReasons := make([]any, len(res.RejectReasons))
	for i, r := range res.RejectReasons {
		rejectReasons[i] = r
	}
	lintIssues := make([]any, len(res.LintIssues))
	for i, iss := range res.LintIssues {
		lintIssues[i] = iss.Error()
	}
	changedFiles := make([]any, len(res.ChangedFiles))
	for i, f := range res.ChangedFiles {
		changedFiles[i] = f
	}
	return Result{Data: map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}}, nil
}

// graphProjectOp: {catalog_path[, overlay_path], graph_id} -> the
// renderer-neutral kitsoki.graph/v1 wire graph a viewer consumes. This is
// where internal/app/graph/objectcatalog.go's deleted ObjectCatalogGraph /
// ObjectCatalogDiffGraph logic now lives (D4: "moves behind
// host.graph.project"). overlay_path present => diff-mode rendering.
func graphProjectOp(args map[string]any) (Result, error) {
	catalogPath := graphStringArg(args, "catalog_path")
	if catalogPath == "" {
		return Result{}, fmt.Errorf("host.graph.project: missing required arg %q", "catalog_path")
	}
	graphID := graphStringArg(args, "graph_id")
	if graphID == "" {
		graphID = "objectgraph:" + catalogPath
	}

	overlayPath := graphStringArg(args, "overlay_path")
	var wire appgraph.KitsokiGraph
	var registryCat *objectgraph.Catalog
	if overlayPath != "" {
		current, err := objectgraph.LoadCatalog(catalogPath)
		if err != nil {
			return Result{}, err
		}
		desired, err := objectgraph.LoadCatalogWithOverlay(catalogPath, overlayPath)
		if err != nil {
			return Result{}, err
		}
		wire = objectCatalogDiffGraph(current, desired, graphID)
		registryCat = desired
	} else {
		cat, err := objectgraph.LoadCatalog(catalogPath)
		if err != nil {
			return Result{}, err
		}
		wire = objectCatalogGraph(cat, graphID)
		registryCat = cat
	}
	return Result{Data: map[string]any{"graph": wire, "registry": registryWire(registryCat)}}, nil
}

// registryWire builds the type-registry passthrough kit-mode clients need to
// render artifact:/materialize: declarations (node-artifact-materialization
// plan, and the use-case-loop plan's C1 Materialize button) — before this,
// only the POG portal's Vite-dev-only /api/catalog splice route
// (vite.config.ts) forwarded this, so a real kitsoki-served kit build never
// saw a type's artifact/materialize contract at all (registry was silently
// absent, not merely different). Field-for-field mirrors
// vite.config.ts's own registry mapping so both topologies render
// identically, minus vite's audience-kind filter (host.graph.project has no
// audience concept — kit-mode callers that need public-only projection
// already do it client-side, see App.vue's projectAudience).
func registryWire(cat *objectgraph.Catalog) []map[string]any {
	defs := cat.Registry.All()
	out := make([]map[string]any, 0, len(defs))
	for _, def := range defs {
		if def.ID == "core-node" {
			continue
		}
		edgeFields := make([]map[string]any, 0, len(def.EdgeFields))
		for _, f := range def.EdgeFields {
			edgeFields = append(edgeFields, map[string]any{
				"id":      string(f.ID),
				"targets": []string{f.TargetType},
				"summary": "",
			})
		}
		entry := map[string]any{
			"id":          def.ID,
			"summary":     def.Summary,
			"extends":     nilIfEmpty(def.Extends),
			"edge_fields": edgeFields,
		}
		if def.Artifact != nil {
			artifact := map[string]any{
				"schema":       string(def.Artifact.Schema),
				"format":       def.Artifact.Format,
				"presentation": def.Artifact.Presentation,
			}
			if def.Materialize != nil {
				params := make([]map[string]any, 0, len(def.Materialize.Params))
				for _, p := range def.Materialize.Params {
					params = append(params, map[string]any{
						"id":           p.ID,
						"type":         p.Type,
						"default":      p.Default,
						"values":       p.Values,
						"required":     p.Required,
						"source_field": p.SourceField,
						"source_edge":  string(p.SourceEdge),
					})
				}
				contextEdges := make([]string, 0, len(def.Materialize.ContextEdges))
				for _, e := range def.Materialize.ContextEdges {
					contextEdges = append(contextEdges, string(e))
				}
				checks := make([]map[string]any, 0, len(def.Materialize.Checks))
				for _, c := range def.Materialize.Checks {
					checks = append(checks, map[string]any{
						"id":           c.ID,
						"script":       c.Script,
						"script_field": c.ScriptField,
						"inputs":       c.Inputs,
						"inputs_field": c.InputsField,
						"capabilities": c.Capabilities,
					})
				}
				artifact["materialize"] = map[string]any{
					"story":         def.Materialize.Story,
					"context_edges": contextEdges,
					"params":        params,
					"gates":         def.Materialize.Gates,
					"checks":        checks,
				}
			}
			entry["artifact"] = artifact
		}
		out = append(out, entry)
	}
	return out
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ─── wire-graph projection (moved from the deleted
// internal/app/graph/objectcatalog.go — D4: "moves behind host.graph.project") ───

// objectCatalogGraph adapts a loaded project object graph catalog
// (internal/graph) into the renderer-neutral kitsoki.graph/v1 wire shape
// internal/app/graph already defines for story room graphs, so the same
// viewer that renders story room graphs can render project object graph
// catalogs too.
func objectCatalogGraph(cat *objectgraph.Catalog, graphID string) appgraph.KitsokiGraph {
	g := appgraph.KitsokiGraph{
		Schema:   appgraph.SchemaV1,
		GraphID:  graphID,
		Kind:     "object-graph",
		Directed: true,
	}
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		g.Nodes = append(g.Nodes, catalogGraphNode(cat, node))
		g.Edges = append(g.Edges, catalogGraphEdges(cat, node)...)
	}
	return g
}

// objectCatalogDiffGraph adapts a (current, desired) catalog pair into one
// kitsoki.graph/v1 graph for diff-mode rendering: every node carries a
// "diff_kind" attr ("added" | "modified" | "removed" | "unchanged") from
// objectgraph.DiffNodes, so a diff-mode UI badges nodes without recomputing
// the classification itself. Desired's data wins for added/modified/
// unchanged nodes; nodes present only in current (removed) are appended from
// current so they stay visible in diff mode.
func objectCatalogDiffGraph(current, desired *objectgraph.Catalog, graphID string) appgraph.KitsokiGraph {
	g := objectCatalogGraph(desired, graphID)
	g.Kind = "object-graph-diff"

	kindByID := make(map[string]objectgraph.GapKind, len(desired.Nodes))
	var removed []objectgraph.NodeDiff
	for _, d := range objectgraph.DiffNodes(current, desired) {
		kindByID[string(d.ID)] = d.Kind
		if d.Kind == objectgraph.GapRemoved {
			removed = append(removed, d)
		}
	}

	for i := range g.Nodes {
		kind, ok := kindByID[g.Nodes[i].ID]
		if !ok {
			kind = "unchanged"
		}
		g.Nodes[i].Attrs["diff_kind"] = string(kind)
	}

	for _, d := range removed {
		node := current.Nodes[d.ID]
		gn := catalogGraphNode(current, node)
		gn.Attrs["diff_kind"] = string(objectgraph.GapRemoved)
		g.Nodes = append(g.Nodes, gn)
		g.Edges = append(g.Edges, catalogGraphEdges(current, node)...)
	}
	return g
}

// catalogGraphNode builds one node's wire representation — shared by
// objectCatalogGraph's normal pass and objectCatalogDiffGraph's synthesis of
// removed nodes from the current catalog.
func catalogGraphNode(cat *objectgraph.Catalog, node *objectgraph.Node) appgraph.GraphNode {
	sources := make([]string, 0, len(node.Sources))
	for _, s := range node.Sources {
		sources = append(sources, string(s))
	}
	attrs := map[string]any{
		"visibility": string(node.Visibility),
		"sources":    sources,
		// fields carries every type-specific value the shared envelope
		// doesn't promote (summary, statement, goal, content_fields,
		// media, ...) so catalog-style clients can render node detail
		// generically, without the Go side hardcoding a per-type field
		// list.
		"fields": node.Fields,
	}
	if eff, ok := cat.Registry.Effective(node.TypeID); ok {
		attrs["type_chain"] = eff.Ancestry
	}
	return appgraph.GraphNode{
		ID:     string(node.ID),
		Kind:   node.TypeID,
		Label:  node.Title,
		Ref:    appgraph.GraphRef{Kind: "object-graph-node", Ref: string(node.ID)},
		Status: node.Status,
		Attrs:  attrs,
	}
}

// catalogGraphEdges builds one node's outgoing wire edges — shared the same
// way catalogGraphNode is.
func catalogGraphEdges(cat *objectgraph.Catalog, node *objectgraph.Node) []appgraph.GraphEdge {
	eff, ok := cat.Registry.Effective(node.TypeID)
	if !ok {
		return nil
	}
	var edges []appgraph.GraphEdge
	for _, decl := range eff.EdgeFields {
		var edgeAttrs map[string]any
		if decl.NestsUnder {
			// Threads the type registry's nests_under marker onto the wire
			// edge so a generic list/detail UI projection (unlike a graph
			// canvas, where the edge itself already draws the relationship)
			// can nest a source node under its target without a
			// hand-maintained kind->edge table that silently drifts from
			// the type registry.
			edgeAttrs = map[string]any{"nests_under": true}
		}
		for _, target := range node.EdgeTargets(decl) {
			edges = append(edges, appgraph.GraphEdge{
				ID:     string(node.ID) + ":" + string(decl.ID) + ":" + string(target),
				Kind:   string(decl.ID),
				Source: string(node.ID),
				Target: string(target),
				Attrs:  edgeAttrs,
			})
		}
	}
	return edges
}

// graphPresentationOp resolves <_kit_dir>/scripts/presentation.star and
// delegates to it via StarlarkBindingHandler — see the package doc comment
// for why this, not embedded Go, serves the layer taxonomy. _kit_dir is
// injected by internal/kitendpoint.Dispatcher.Call for every
// kit.<kit>.<iface>.<op> invocation; a bare host.graph.presentation call with
// no kit context fails closed rather than silently returning nothing.
func graphPresentationOp(ctx context.Context, args map[string]any) (Result, error) {
	kitDir := graphStringArg(args, "_kit_dir")
	if kitDir == "" {
		return Result{}, fmt.Errorf("host.graph.presentation: requires kit context (_kit_dir) — only callable through the kit.<kit>.<iface>.presentation dispatch surface")
	}
	scriptPath := filepath.Join(kitDir, "scripts", "presentation.star")
	return StarlarkBindingHandler(scriptPath)(ctx, args)
}

func graphQueryOp(args map[string]any) (Result, error) {
	cat, err := loadCatalogArg(args)
	if err != nil {
		return Result{}, err
	}
	mode := graphStringArg(args, "mode")
	target := graphStringArg(args, "target")
	if mode == "" || target == "" {
		return Result{}, fmt.Errorf("host.graph.query: requires both mode and target")
	}

	switch mode {
	case "refs-to":
		return graphQueryRefsTo(cat, target)
	case "explain-type":
		return graphQueryExplainType(cat, target)
	case "impact":
		toType := graphStringArg(args, "to_type")
		return graphQueryImpact(cat, target, toType)
	default:
		return Result{}, fmt.Errorf("host.graph.query: unknown mode %q", mode)
	}
}

func graphQueryRefsTo(cat *objectgraph.Catalog, targetID string) (Result, error) {
	var refs []any
	targetNodeID := objectgraph.NodeID(targetID)
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		eff, ok := cat.Registry.Effective(node.TypeID)
		if !ok {
			continue
		}
		for _, decl := range eff.EdgeFields {
			for _, t := range node.EdgeTargets(decl) {
				if t == targetNodeID {
					refs = append(refs, map[string]any{
						"node":       string(id),
						"edge_field": string(decl.ID),
					})
				}
			}
		}
	}
	return Result{Data: map[string]any{"references": refs}}, nil
}

func graphQueryExplainType(cat *objectgraph.Catalog, typeID string) (Result, error) {
	eff, ok := cat.Registry.Effective(typeID)
	if !ok {
		return Result{}, fmt.Errorf("unknown type %q", typeID)
	}

	type Edge struct {
		ID          string `json:"id"`
		TargetType  string `json:"target_type"`
		Cardinality string `json:"cardinality"`
		Storage     string `json:"storage"`
		Acyclic     bool   `json:"acyclic"`
		Renders     bool   `json:"renders"`
		NestsUnder  bool   `json:"nests_under"`
	}
	edges := make([]Edge, len(eff.EdgeFields))
	for i, decl := range eff.EdgeFields {
		edges[i] = Edge{
			ID:          string(decl.ID),
			TargetType:  decl.TargetType,
			Cardinality: string(decl.Cardinality),
			Storage:     string(decl.Storage),
			Acyclic:     decl.Acyclic,
			Renders:     decl.Renders,
			NestsUnder:  decl.NestsUnder,
		}
	}

	return Result{Data: map[string]any{
		"type_id":         eff.ID,
		"schema":          string(eff.Schema),
		"extends":         eff.Extends,
		"summary":         eff.Summary,
		"required_fields": eff.RequiredFields,
		"edge_fields":     edges,
		"ancestry":        eff.Ancestry,
	}}, nil
}

func graphQueryImpact(cat *objectgraph.Catalog, targetID string, toType string) (Result, error) {
	node, exists := cat.Nodes[objectgraph.NodeID(targetID)]
	if !exists {
		return Result{}, fmt.Errorf("node %q not found", targetID)
	}

	typeRes, err := graphQueryExplainType(cat, node.TypeID)
	var typeData any
	if err == nil {
		typeData = typeRes.Data
	}

	refsRes, err := graphQueryRefsTo(cat, targetID)
	var refs any
	if err == nil {
		refs = refsRes.Data["references"]
	}

	var incompatibleRefs []any
	if toType != "" {
		if !cat.Registry.HasTypeDef(toType) {
			return Result{}, fmt.Errorf("target type %q not found in registry", toType)
		}
		for _, id := range cat.SortedNodeIDs() {
			otherNode := cat.Nodes[id]
			eff, ok := cat.Registry.Effective(otherNode.TypeID)
			if !ok {
				continue
			}
			for _, decl := range eff.EdgeFields {
				for _, t := range otherNode.EdgeTargets(decl) {
					if t == objectgraph.NodeID(targetID) {
						if decl.TargetType != "" && !cat.Registry.IsA(toType, decl.TargetType) {
							incompatibleRefs = append(incompatibleRefs, map[string]any{
								"node":        string(id),
								"edge_field":  string(decl.ID),
								"target_type": decl.TargetType,
							})
						}
					}
				}
			}
		}
	}

	return Result{Data: map[string]any{
		"node_id":           targetID,
		"current_type":      node.TypeID,
		"explain_type":      typeData,
		"references":        refs,
		"incompatible_refs": incompatibleRefs,
	}}, nil
}
