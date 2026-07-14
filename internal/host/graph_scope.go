// host.graph.* scope threading: every graph op accepts an optional `scope`
// argument (the objectgraph.ScopeSpec wire mapping) carrying a session's
// baked catalog subset. Reads apply it as a pruned view at the shared
// loadCatalogArg choke point (graph_handlers.go); the write ops in this
// repo's lifecycle family (propose/apply/authorize/withdraw/rebase) instead
// gate against the FULL catalog here — a pruned catalog must never reach a
// code path that writes it back, since that would delete every out-of-scope
// node. See internal/graph/scope.go and docs/architecture/mcp-graph.md
// ("Scoped sessions").
package host

import (
	"fmt"
	"strings"

	objectgraph "kitsoki/internal/graph"
)

// graphScopeSpecArg parses args["scope"] into a ScopeSpec; nil when absent.
func graphScopeSpecArg(args map[string]any) (*objectgraph.ScopeSpec, error) {
	raw, ok := args["scope"]
	if !ok || raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("host.graph: %q must be a mapping, got %T", "scope", raw)
	}
	return objectgraph.ParseScopeSpec(m)
}

// graphScopeGuardOps enforces a baked scope on graph.propose's raw wire
// operations: no-op when args carries no scope; otherwise resolves the
// member set on the full catalog and rejects the call if any operation
// touches an out-of-scope node. The error text carries "out of scope" —
// the marker the MCP layer classifies as OUT_OF_SCOPE.
func graphScopeGuardOps(opName, catalogPath string, args map[string]any, rawOps []map[string]any) error {
	spec, err := graphScopeSpecArg(args)
	if err != nil || spec == nil {
		return err
	}
	cat, err := objectgraph.LoadCatalog(catalogPath)
	if err != nil {
		return err
	}
	members, err := objectgraph.ResolveScope(cat, spec)
	if err != nil {
		return fmt.Errorf("host.graph.%s: %w", opName, err)
	}
	if v := objectgraph.ScopeWriteViolationsWire(cat, members, rawOps); len(v) > 0 {
		return fmt.Errorf("host.graph.%s: out of scope for this session's baked graph scope: %s", opName, strings.Join(v, "; "))
	}
	return nil
}

// graphScopeGuardChangeset enforces a baked scope on the lifecycle write
// ops that reference an existing changeset (apply/authorize/withdraw/
// rebase): the changeset's parsed operations must only touch in-scope
// nodes. An unknown or unparseable changeset id is left for the engine's
// own validation to report.
func graphScopeGuardChangeset(opName, catalogPath, changesetID string, args map[string]any) error {
	spec, err := graphScopeSpecArg(args)
	if err != nil || spec == nil {
		return err
	}
	cat, err := objectgraph.LoadCatalog(catalogPath)
	if err != nil {
		return err
	}
	members, err := objectgraph.ResolveScope(cat, spec)
	if err != nil {
		return fmt.Errorf("host.graph.%s: %w", opName, err)
	}
	node, ok := cat.Nodes[objectgraph.NodeID(changesetID)]
	if !ok || node.TypeID != "changeset" {
		return nil
	}
	cs, err := objectgraph.ParseChangeset(node)
	if err != nil {
		return nil
	}
	if v := objectgraph.ScopeWriteViolations(cat, members, cs.Operations); len(v) > 0 {
		return fmt.Errorf("host.graph.%s: changeset %q is out of scope for this session's baked graph scope: %s", opName, changesetID, strings.Join(v, "; "))
	}
	return nil
}
