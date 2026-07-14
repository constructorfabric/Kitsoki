package graph

// Scope: a deterministic subset of a catalog, resolved from a declarative
// ScopeSpec (roots + traversal + type/id includes) and baked into a session
// at server construction time — never applied by prompt. The mcp-graph
// server binds a spec per catalog alias (--scope) and threads it through
// every host.graph.* call; reads then operate on ApplyScope's pruned view,
// and writes are gated by ScopeWriteViolations against the FULL catalog (a
// pruned catalog must never be written back to disk — that would delete
// every out-of-scope node).
//
// Scope is a focus/guardrail mechanism, not a security boundary: an
// out-of-scope id in an error message or history row is acceptable; an
// out-of-scope node's CONTENT is not reachable through a scoped read, and
// an out-of-scope node is not mutable through a scoped write.

import (
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

// changesetTypeID is the type id changeset lifecycle machinery rides on —
// the same hardcoded convention host.graph.changeset filters by. Changeset
// nodes are always in scope (see ResolveScope) so a scoped session can read
// back the lifecycle of its own proposals.
const changesetTypeID = "changeset"

// ScopeSpec is the declarative subset selector. Membership is the union of
// (1) Roots expanded by a Direction/Depth/Edges-bounded BFS, (2) every node
// IsA one of Types, and (3) the explicit Include ids — minus Exclude, which
// both removes nodes and blocks traversal through them. At least one of
// Roots/Types/Include must be non-empty (an all-empty spec would silently
// mean "nothing", which is never what a session wants baked in).
type ScopeSpec struct {
	// Roots are the BFS start nodes. Every root must exist in the catalog.
	Roots []NodeID `yaml:"roots"`
	// Direction is the BFS edge direction: out (default), in, or both.
	Direction NeighborsDirection `yaml:"direction"`
	// Depth bounds the BFS hop count: nil = unlimited, 0 = roots only.
	Depth *int `yaml:"depth"`
	// Edges, when non-empty, restricts traversal to these edge field ids.
	Edges []EdgeField `yaml:"edges"`
	// Types additionally includes every node IsA one of these type ids.
	Types []string `yaml:"types"`
	// Include additionally includes these exact node ids.
	Include []NodeID `yaml:"include"`
	// Exclude removes nodes after expansion AND blocks traversal through
	// them. Unknown Exclude ids are ignored (a scope file must not break
	// when an excluded node is later deleted). Exclude wins over every
	// include mechanism, including the changeset auto-include.
	Exclude []NodeID `yaml:"exclude"`
}

// scopeSpecKeys is the closed key vocabulary ParseScopeSpec accepts —
// unknown keys are an error so a typo'd selector fails loudly at server
// start instead of silently widening the baked scope.
var scopeSpecKeys = map[string]bool{
	"roots": true, "direction": true, "depth": true, "edges": true,
	"types": true, "include": true, "exclude": true,
}

// ParseScopeSpec decodes the wire/YAML mapping form of a scope spec — the
// single parser behind both LoadScopeFile and the host.graph.* ops' `scope`
// argument, so the two doors can never drift.
func ParseScopeSpec(raw map[string]any) (*ScopeSpec, error) {
	for k := range raw {
		if !scopeSpecKeys[k] {
			known := make([]string, 0, len(scopeSpecKeys))
			for kk := range scopeSpecKeys {
				known = append(known, kk)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("graph scope: unknown key %q (known: %v)", k, known)
		}
	}
	spec := &ScopeSpec{}
	var err error
	if spec.Roots, err = scopeNodeIDList(raw, "roots"); err != nil {
		return nil, err
	}
	if spec.Include, err = scopeNodeIDList(raw, "include"); err != nil {
		return nil, err
	}
	if spec.Exclude, err = scopeNodeIDList(raw, "exclude"); err != nil {
		return nil, err
	}
	edges, err := scopeStringList(raw, "edges")
	if err != nil {
		return nil, err
	}
	for _, e := range edges {
		spec.Edges = append(spec.Edges, EdgeField(e))
	}
	if spec.Types, err = scopeStringList(raw, "types"); err != nil {
		return nil, err
	}
	if dirRaw, ok := raw["direction"]; ok && dirRaw != nil {
		dir, ok := dirRaw.(string)
		if !ok {
			return nil, fmt.Errorf("graph scope: direction must be a string, got %T", dirRaw)
		}
		spec.Direction = NeighborsDirection(dir)
	}
	switch spec.Direction {
	case "", DirectionOut, DirectionIn, DirectionBoth:
	default:
		return nil, fmt.Errorf("graph scope: direction must be one of out, in, both, got %q", spec.Direction)
	}
	if depthRaw, ok := raw["depth"]; ok && depthRaw != nil {
		var d int
		switch v := depthRaw.(type) {
		case int:
			d = v
		case int64:
			d = int(v)
		case uint64:
			d = int(v)
		case float64:
			d = int(v)
		default:
			return nil, fmt.Errorf("graph scope: depth must be an integer, got %T", depthRaw)
		}
		if d < 0 {
			return nil, fmt.Errorf("graph scope: depth must be >= 0 (omit depth for unlimited), got %d", d)
		}
		spec.Depth = &d
	}
	if len(spec.Roots) == 0 && len(spec.Types) == 0 && len(spec.Include) == 0 {
		return nil, fmt.Errorf("graph scope: at least one of roots, types, include must be non-empty")
	}
	return spec, nil
}

func scopeStringList(raw map[string]any, key string) ([]string, error) {
	v, ok := raw[key]
	if !ok || v == nil {
		return nil, nil
	}
	switch list := v.(type) {
	case []string:
		return list, nil
	case []any:
		out := make([]string, 0, len(list))
		for i, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("graph scope: %s[%d] must be a string, got %T", key, i, item)
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("graph scope: %s must be a list of strings, got %T", key, v)
	}
}

func scopeNodeIDList(raw map[string]any, key string) ([]NodeID, error) {
	ss, err := scopeStringList(raw, key)
	if err != nil {
		return nil, err
	}
	out := make([]NodeID, len(ss))
	for i, s := range ss {
		out[i] = NodeID(s)
	}
	return out, nil
}

// WireMap re-serializes the spec into the mapping form host.graph.* ops
// accept as args["scope"] — ParseScopeSpec(spec.WireMap()) round-trips.
func (s *ScopeSpec) WireMap() map[string]any {
	out := map[string]any{}
	if len(s.Roots) > 0 {
		out["roots"] = nodeIDsToAny(s.Roots)
	}
	if s.Direction != "" {
		out["direction"] = string(s.Direction)
	}
	if s.Depth != nil {
		out["depth"] = *s.Depth
	}
	if len(s.Edges) > 0 {
		edges := make([]any, len(s.Edges))
		for i, e := range s.Edges {
			edges[i] = string(e)
		}
		out["edges"] = edges
	}
	if len(s.Types) > 0 {
		types := make([]any, len(s.Types))
		for i, t := range s.Types {
			types[i] = t
		}
		out["types"] = types
	}
	if len(s.Include) > 0 {
		out["include"] = nodeIDsToAny(s.Include)
	}
	if len(s.Exclude) > 0 {
		out["exclude"] = nodeIDsToAny(s.Exclude)
	}
	return out
}

func nodeIDsToAny(ids []NodeID) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

// LoadScopeFile parses a scope spec YAML file (bare roots/direction/depth/
// edges/types/include/exclude keys — no wrapper block).
func LoadScopeFile(path string) (*ScopeSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("graph scope: read %s: %w", path, err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("graph scope: parse %s: %w", path, err)
	}
	spec, err := ParseScopeSpec(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return spec, nil
}

// ScopeInfo records how a catalog was pruned — attached to the scoped
// Catalog (Catalog.Scope) so ops downstream of ApplyScope can report scope
// state (graph.open's scope block, graph.get's out_of_scope markers)
// without re-resolving the spec.
type ScopeInfo struct {
	Spec *ScopeSpec
	// TotalNodes is the full catalog's node count before pruning.
	TotalNodes int
	// MemberCount == len(scoped.Nodes).
	MemberCount int
	// PrunedEdges counts edge targets dropped because they pointed at an
	// out-of-scope node (both Edges-storage and top_level-storage fields).
	PrunedEdges int
	// Excluded holds every node id present in the full catalog but not in
	// scope — the "it exists, but not in this session" distinction get/
	// neighbors use to say "out of scope" instead of "unknown node".
	Excluded map[NodeID]bool
}

// ResolveScope computes the deterministic member set for spec against cat.
// Unknown roots, include ids, types, or edge fields are hard errors (a
// scope that silently matches nothing is a misconfiguration, not a view);
// unknown Exclude ids are ignored.
func ResolveScope(cat *Catalog, spec *ScopeSpec) (map[NodeID]bool, error) {
	direction := spec.Direction
	if direction == "" {
		direction = DirectionOut
	}

	if len(spec.Edges) > 0 {
		known := map[EdgeField]bool{}
		for _, def := range cat.Registry.All() {
			for _, f := range def.EdgeFields {
				known[f.ID] = true
			}
		}
		for _, e := range spec.Edges {
			if !known[e] {
				return nil, fmt.Errorf("graph scope: unknown edge field %q", e)
			}
		}
	}
	for _, t := range spec.Types {
		if !cat.Registry.HasTypeDef(t) {
			return nil, fmt.Errorf("graph scope: unknown type %q", t)
		}
	}
	for _, id := range spec.Roots {
		if _, ok := cat.Nodes[id]; !ok {
			return nil, fmt.Errorf("graph scope: unknown root node %q (nearest: %v)", id, NearestIDs(cat, string(id), 3))
		}
	}
	for _, id := range spec.Include {
		if _, ok := cat.Nodes[id]; !ok {
			return nil, fmt.Errorf("graph scope: unknown include node %q (nearest: %v)", id, NearestIDs(cat, string(id), 3))
		}
	}

	excluded := map[NodeID]bool{}
	for _, id := range spec.Exclude {
		excluded[id] = true
	}

	edgeAllow := map[EdgeField]bool{}
	for _, e := range spec.Edges {
		edgeAllow[e] = true
	}

	members := map[NodeID]bool{}
	add := func(id NodeID) bool {
		if excluded[id] || members[id] {
			return false
		}
		members[id] = true
		return true
	}

	// (1) Roots + BFS.
	var revIdx ReverseIndex
	if direction == DirectionIn || direction == DirectionBoth {
		revIdx = BuildReverseIndex(cat)
	}
	maxDepth := -1 // unlimited
	if spec.Depth != nil {
		maxDepth = *spec.Depth
	}
	type frontierEntry struct {
		id    NodeID
		depth int
	}
	var frontier []frontierEntry
	for _, root := range spec.Roots {
		if add(root) {
			frontier = append(frontier, frontierEntry{root, 0})
		}
	}
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		if maxDepth >= 0 && cur.depth >= maxDepth {
			continue
		}
		node := cat.Nodes[cur.id]
		if direction == DirectionOut || direction == DirectionBoth {
			if eff, ok := cat.Registry.Effective(node.TypeID); ok {
				for _, decl := range eff.EdgeFields {
					if len(edgeAllow) > 0 && !edgeAllow[decl.ID] {
						continue
					}
					for _, target := range node.EdgeTargets(decl) {
						if _, exists := cat.Nodes[target]; exists && add(target) {
							frontier = append(frontier, frontierEntry{target, cur.depth + 1})
						}
					}
				}
			}
		}
		if direction == DirectionIn || direction == DirectionBoth {
			for _, ref := range revIdx[cur.id] {
				if len(edgeAllow) > 0 && !edgeAllow[ref.EdgeField] {
					continue
				}
				if add(ref.Node) {
					frontier = append(frontier, frontierEntry{ref.Node, cur.depth + 1})
				}
			}
		}
	}

	// (2) Whole-type includes.
	for _, id := range cat.SortedNodeIDs() {
		node := cat.Nodes[id]
		for _, t := range spec.Types {
			if cat.Registry.IsA(node.TypeID, t) {
				add(id)
				break
			}
		}
	}

	// (3) Explicit includes.
	for _, id := range spec.Include {
		add(id)
	}

	// Changeset lifecycle machinery is always in scope (unless explicitly
	// excluded) so a scoped session can read back its own proposals.
	for _, id := range cat.SortedNodeIDs() {
		if cat.Nodes[id].TypeID == changesetTypeID {
			add(id)
		}
	}

	return members, nil
}

// ApplyScope resolves spec and returns a pruned copy of cat containing only
// member nodes, with every edge target pointing outside the member set
// dropped (both Edges-storage and top_level-storage fields), and Scope set.
// cat itself is never mutated. The result is a READ view: writing it back
// to disk would delete every out-of-scope node, which is why write ops gate
// via ScopeWriteViolations against the full catalog instead.
func ApplyScope(cat *Catalog, spec *ScopeSpec) (*Catalog, error) {
	members, err := ResolveScope(cat, spec)
	if err != nil {
		return nil, err
	}

	scoped := *cat
	scoped.Nodes = make(map[NodeID]*Node, len(members))
	scoped.NodeFile = make(map[NodeID]string, len(members))
	prunedEdges := 0

	for id := range members {
		orig := cat.Nodes[id]
		node := *orig
		copiedFields, copiedEdges := false, false
		if eff, ok := cat.Registry.Effective(node.TypeID); ok {
			for _, decl := range eff.EdgeFields {
				targets := orig.EdgeTargets(decl)
				if len(targets) == 0 {
					continue
				}
				kept := make([]NodeID, 0, len(targets))
				for _, t := range targets {
					if members[t] {
						kept = append(kept, t)
					}
				}
				if len(kept) == len(targets) {
					continue
				}
				prunedEdges += len(targets) - len(kept)
				if decl.Storage == StorageTopLevel {
					if !copiedFields {
						node.Fields = copyAnyMap(orig.Fields)
						copiedFields = true
					}
					node.Fields[string(decl.ID)] = nodeIDsToAny(kept)
				} else {
					if !copiedEdges {
						node.Edges = copyEdgesMap(orig.Edges)
						copiedEdges = true
					}
					node.Edges[decl.ID] = kept
				}
			}
		}
		scoped.Nodes[id] = &node
		if f, ok := cat.NodeFile[id]; ok {
			scoped.NodeFile[id] = f
		}
	}

	excludedSet := map[NodeID]bool{}
	for id := range cat.Nodes {
		if !members[id] {
			excludedSet[id] = true
		}
	}
	scoped.Scope = &ScopeInfo{
		Spec:        spec,
		TotalNodes:  len(cat.Nodes),
		MemberCount: len(scoped.Nodes),
		PrunedEdges: prunedEdges,
		Excluded:    excludedSet,
	}
	return &scoped, nil
}

func copyAnyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func copyEdgesMap(m map[EdgeField][]NodeID) map[EdgeField][]NodeID {
	out := make(map[EdgeField][]NodeID, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// ScopeWriteViolations checks a changeset's typed operations against a
// scope member set (resolved on the FULL catalog) and returns one
// human-readable violation per offending operation. Rules:
//
//   - added: always allowed — creating a node never damages out-of-scope
//     content, and a new node connected to the scope becomes a member on
//     the next resolve.
//   - modified / removed / retyped: the target node, when it exists in the
//     catalog, must be in scope.
//   - renamed: both endpoints, when they exist, must be in scope.
//   - registry_type_added / registry_type_modified: always rejected — the
//     type registry is catalog-wide, so a scoped session may not touch it.
//
// Operations naming nodes that don't exist at all are left for the engine's
// own validation (unknown-node rejections carry better suggestions there).
func ScopeWriteViolations(cat *Catalog, members map[NodeID]bool, ops []Operation) []string {
	var out []string
	blocked := func(id NodeID) bool {
		_, exists := cat.Nodes[id]
		return exists && !members[id]
	}
	for i, op := range ops {
		switch op.Kind {
		case OpRegistryTypeAdded, OpRegistryTypeModified:
			out = append(out, fmt.Sprintf("operations[%d] (%s): the type registry is catalog-wide and cannot be changed from a scoped session", i, op.Kind))
		case OpAdded:
			// Always allowed.
		case OpRenamed:
			if blocked(op.From) {
				out = append(out, fmt.Sprintf("operations[%d] (renamed): node %q is out of scope", i, op.From))
			}
			if blocked(op.To) {
				out = append(out, fmt.Sprintf("operations[%d] (renamed): node %q is out of scope", i, op.To))
			}
		default: // modified, removed, retyped
			if blocked(op.Node) {
				out = append(out, fmt.Sprintf("operations[%d] (%s): node %q is out of scope", i, op.Kind, op.Node))
			}
		}
	}
	return out
}

// ScopeWriteViolationsWire adapts ScopeWriteViolations to graph.propose's
// raw wire operations ([]map[string]any). An operation that fails to parse
// is skipped here — the engine's own Propose validation rejects it with a
// better-shaped error than a scope check could.
func ScopeWriteViolationsWire(cat *Catalog, members map[NodeID]bool, rawOps []map[string]any) []string {
	ops := make([]Operation, 0, len(rawOps))
	for _, m := range rawOps {
		op, err := parseOperation(m)
		if err != nil {
			continue
		}
		ops = append(ops, op)
	}
	return ScopeWriteViolations(cat, members, ops)
}
