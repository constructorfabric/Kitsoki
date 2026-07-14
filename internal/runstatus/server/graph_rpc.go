// graph_rpc.go — the bare "graph.propose"/"graph.authorize" JSON-RPC methods
// (A1, use-case-loop-plan §3.3): thin wire adapters over
// internal/graph.Propose/Authorize, wired directly into Server.dispatch
// rather than through internal/kitendpoint (see server.go's dispatch-switch
// comment on the "graph.*" case for why).
package server

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"kitsoki/internal/clock"
	objectgraph "kitsoki/internal/graph"
)

// graphRPCClock resolves the effective clock these bare RPC write ops
// (graph.propose/authorize/withdraw/apply) stamp with: KITSOKI_GRAPH_CLOCK_FIXED
// wins when set (RFC3339, parsed once per call), else clock.Real(). This
// carrier has no ctx/host.Registry actor-injection seam (a known gap: the
// plan flags "third write carrier ... no journal/host.Registry/actor" —
// graph.propose/authorize/apply/withdraw here always stamp with actor ""),
// but the clock-fixed determinism guarantee still applies for gate checks
// that exercise this RPC surface directly.
func graphRPCClock() (clock.Clock, *rpcError) {
	if fixed := os.Getenv("KITSOKI_GRAPH_CLOCK_FIXED"); fixed != "" {
		t, err := time.Parse(time.RFC3339, fixed)
		if err != nil {
			return nil, &rpcError{Code: codeServerError, Message: fmt.Sprintf("KITSOKI_GRAPH_CLOCK_FIXED=%q: invalid RFC3339 timestamp: %v", fixed, err)}
		}
		return clock.NewFake(t.UTC()), nil
	}
	return clock.Real(), nil
}

// graphRejectDetails classifies raw reject-reason strings into the
// structured {code, file, message} shape (internal/graph.RejectDetail) —
// additive alongside the untouched reject_reasons array, so a browser
// client can tell a repo-level needs_canonicalization condition from a
// genuine per-changeset conflict without regexing message text.
func graphRejectDetails(reasons []string) []any {
	details := make([]any, len(reasons))
	for i, r := range reasons {
		d := objectgraph.ClassifyRejectReason(r)
		m := map[string]any{"code": d.Code, "message": d.Message}
		if d.File != "" {
			m["file"] = d.File
		}
		details[i] = m
	}
	return details
}

func graphStringParam(params map[string]any, key string) string {
	s, _ := params[key].(string)
	return s
}

func graphMapParam(params map[string]any, key string) map[string]any {
	m, _ := params[key].(map[string]any)
	return m
}

func graphBoolParam(params map[string]any, key string) bool {
	b, _ := params[key].(bool)
	return b
}

// graphAllowlist builds this server's catalog alias allowlist (F1,
// catalog_allowlist.go) against <materializeRoot>/pog/catalog.yaml — the
// same "home repo" convention materialize.go's repoRoot fallback uses.
// Rebuilt fresh on every call rather than cached; see
// buildCatalogAllowlist's doc comment for why.
func (s *Server) graphAllowlist() *CatalogAllowlist {
	repoRoot := s.materializeRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	return buildCatalogAllowlist(filepath.Join(repoRoot, "pog", "catalog.yaml"), repoRoot)
}

// graphProposeRPC: {catalog_path, title, operations[, visibility, provenance]}
// -> {changeset_id, status, lint, rejected, reject_reasons}. See
// internal/host/graph_handlers.go's graphProposeOp for the sibling
// host.graph.propose adapter this mirrors (kept in sync deliberately —
// same wire shape, different transport).
//
// catalog (optional): a bound allowlist alias (F1, catalog_allowlist.go) —
// a browser-facing caller should always prefer this over catalog_path, a
// raw filesystem path a browser must never be trusted to name. catalog_path
// alone remains fully supported for existing (non-portal) callers.
func (s *Server) graphProposeRPC(params map[string]any) (any, *rpcError) {
	catalogPath, rerr := resolveGraphCatalogParam(s.graphAllowlist(), graphStringParam(params, "catalog"), graphStringParam(params, "catalog_path"), "graph.propose")
	if rerr != nil {
		return nil, rerr
	}
	if catalogPath == "" {
		return nil, &rpcError{Code: codeServerError, Message: "graph.propose: missing 'catalog_path' (or 'catalog')"}
	}
	rawOps, _ := params["operations"].([]any)
	ops := make([]map[string]any, 0, len(rawOps))
	for _, r := range rawOps {
		if m, ok := r.(map[string]any); ok {
			ops = append(ops, m)
		}
	}
	clk, rerr := graphRPCClock()
	if rerr != nil {
		return nil, rerr
	}
	// Provenance strip (hazard guard #1, plan §3.4 red-team amendment #1):
	// this bare RPC carrier has no ctx/actor/trust-injection seam at all
	// (see graphRPCClock's doc comment on the "third write carrier" gap) —
	// so, unlike host.graph.propose which honors an explicit steward-mode
	// context, this surface can never be trusted with caller-supplied
	// provenance and always drops it, regardless of what params contained.
	res, err := objectgraph.Propose(catalogPath, objectgraph.ProposeInput{
		Title:      graphStringParam(params, "title"),
		Visibility: graphStringParam(params, "visibility"),
		Operations: ops,
	}, "", clk)
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: "graph.propose: " + err.Error()}
	}
	lint := make([]any, len(res.Lint))
	for i, iss := range res.Lint {
		lint[i] = iss.Error()
	}
	rejectReasons := make([]any, len(res.RejectReasons))
	for i, r := range res.RejectReasons {
		rejectReasons[i] = r
	}
	return map[string]any{
		"changeset_id":   string(res.ChangesetID),
		"status":         res.Status,
		"lint":           lint,
		"rejected":       len(res.RejectReasons) > 0 || len(res.Lint) > 0,
		"reject_reasons": rejectReasons,
		"reject_details": graphRejectDetails(res.RejectReasons),
	}, nil
}

// graphAuthorizeRPC: {catalog_path, changeset_id} -> {rejected,
// reject_reasons, lint_issues, changed_files} — the same result shape
// graph.apply/host.graph.apply already use, for a consistent client
// contract across the two lifecycle-writing ops.
func (s *Server) graphAuthorizeRPC(params map[string]any) (any, *rpcError) {
	catalogPath, rerr := resolveGraphCatalogParam(s.graphAllowlist(), graphStringParam(params, "catalog"), graphStringParam(params, "catalog_path"), "graph.authorize")
	if rerr != nil {
		return nil, rerr
	}
	changesetID := graphStringParam(params, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return nil, &rpcError{Code: codeServerError, Message: "graph.authorize: requires both 'catalog_path' (or 'catalog') and 'changeset_id'"}
	}
	clk, rerr := graphRPCClock()
	if rerr != nil {
		return nil, rerr
	}
	res, err := objectgraph.Authorize(catalogPath, objectgraph.NodeID(changesetID), "", clk)
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: "graph.authorize: " + err.Error()}
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
	return map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"reject_details": graphRejectDetails(res.RejectReasons),
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}, nil
}

// graphWithdrawRPC: {catalog_path, changeset_id} -> the bare sibling of
// graphAuthorizeRPC for the review queue's "withdraw" action
// (internal/graph.Withdraw).
func (s *Server) graphWithdrawRPC(params map[string]any) (any, *rpcError) {
	catalogPath, rerr := resolveGraphCatalogParam(s.graphAllowlist(), graphStringParam(params, "catalog"), graphStringParam(params, "catalog_path"), "graph.withdraw")
	if rerr != nil {
		return nil, rerr
	}
	changesetID := graphStringParam(params, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return nil, &rpcError{Code: codeServerError, Message: "graph.withdraw: requires both 'catalog_path' (or 'catalog') and 'changeset_id'"}
	}
	clk, rerr := graphRPCClock()
	if rerr != nil {
		return nil, rerr
	}
	res, err := objectgraph.Withdraw(catalogPath, objectgraph.NodeID(changesetID), "", clk)
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: "graph.withdraw: " + err.Error()}
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
	return map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"reject_details": graphRejectDetails(res.RejectReasons),
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}, nil
}

// graphRebaseRPC: {catalog_path, changeset_id} -> the bare sibling of
// graphWithdrawRPC for the review queue's "rebase" action
// (internal/graph.Rebase) — refreshes non-conflicting stale Before guards on
// a proposed changeset and re-validates, per §3.3. Same graphRPCClock/actor-""
// carrier gap as every other bare graph.* RPC here (see graphRPCClock's doc
// comment): no ctx/actor seam on this carrier, so actor is always "".
func (s *Server) graphRebaseRPC(params map[string]any) (any, *rpcError) {
	catalogPath, rerr := resolveGraphCatalogParam(s.graphAllowlist(), graphStringParam(params, "catalog"), graphStringParam(params, "catalog_path"), "graph.rebase")
	if rerr != nil {
		return nil, rerr
	}
	changesetID := graphStringParam(params, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return nil, &rpcError{Code: codeServerError, Message: "graph.rebase: requires both 'catalog_path' (or 'catalog') and 'changeset_id'"}
	}
	clk, rerr := graphRPCClock()
	if rerr != nil {
		return nil, rerr
	}
	res, err := objectgraph.Rebase(catalogPath, objectgraph.NodeID(changesetID), "", clk)
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: "graph.rebase: " + err.Error()}
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
	return map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"reject_details": graphRejectDetails(res.RejectReasons),
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}, nil
}

// graphCanonicalizeRPC: {catalog | catalog_path[, dry_run]} ->
// {changed_files, skipped, already_canonical} — the portal's fix-it button
// for a NEEDS_CANONICALIZATION reject: re-serialize the catalog's
// block-scalar-bearing files into canonical re-marshal form
// (internal/graph.Canonicalize) so every queued changeset stops rejecting
// on a repo-level formatting condition. Same allowlist gate as the other
// graph.* verbs: a browser names catalogs by alias, never by raw path.
func (s *Server) graphCanonicalizeRPC(params map[string]any) (any, *rpcError) {
	catalogPath, rerr := resolveGraphCatalogParam(s.graphAllowlist(), graphStringParam(params, "catalog"), graphStringParam(params, "catalog_path"), "graph.canonicalize")
	if rerr != nil {
		return nil, rerr
	}
	if catalogPath == "" {
		return nil, &rpcError{Code: codeServerError, Message: "graph.canonicalize: missing 'catalog_path' (or 'catalog')"}
	}
	res, err := objectgraph.Canonicalize(catalogPath, graphBoolParam(params, "dry_run"))
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: "graph.canonicalize: " + err.Error()}
	}
	changedFiles := make([]any, len(res.ChangedFiles))
	for i, f := range res.ChangedFiles {
		changedFiles[i] = f
	}
	skipped := make([]any, len(res.Skipped))
	for i, s := range res.Skipped {
		skipped[i] = s
	}
	return map[string]any{
		"changed_files":     changedFiles,
		"skipped":           skipped,
		"already_canonical": len(res.ChangedFiles) == 0 && len(res.Skipped) == 0,
	}, nil
}

// graphApplyRPC: {catalog_path, changeset_id[, dry_run]} -> the bare
// "graph.apply" sibling of graph.propose/graph.authorize, so a portal review
// queue can re-validate (dry_run) and commit an authorized changeset without
// going through the kit dispatch surface — same wire shape as
// host.graph.apply (internal/host/graph_handlers.go's graphApplyOp).
func (s *Server) graphApplyRPC(params map[string]any) (any, *rpcError) {
	catalogPath, rerr := resolveGraphCatalogParam(s.graphAllowlist(), graphStringParam(params, "catalog"), graphStringParam(params, "catalog_path"), "graph.apply")
	if rerr != nil {
		return nil, rerr
	}
	changesetID := graphStringParam(params, "changeset_id")
	if catalogPath == "" || changesetID == "" {
		return nil, &rpcError{Code: codeServerError, Message: "graph.apply: requires both 'catalog_path' (or 'catalog') and 'changeset_id'"}
	}
	clk, rerr := graphRPCClock()
	if rerr != nil {
		return nil, rerr
	}
	res, err := objectgraph.Apply(catalogPath, objectgraph.NodeID(changesetID), graphBoolParam(params, "dry_run"), "", clk)
	if err != nil {
		return nil, &rpcError{Code: codeServerError, Message: "graph.apply: " + err.Error()}
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
	return map[string]any{
		"rejected":       res.Rejected(),
		"reject_reasons": rejectReasons,
		"reject_details": graphRejectDetails(res.RejectReasons),
		"lint_issues":    lintIssues,
		"changed_files":  changedFiles,
	}, nil
}
