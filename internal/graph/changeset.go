package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// OpKind is the kind of a single changeset operation.
type OpKind string

const (
	OpAdded                OpKind = "added"
	OpModified             OpKind = "modified"
	OpRemoved              OpKind = "removed"
	OpRenamed              OpKind = "renamed"
	OpRetyped              OpKind = "retyped"
	OpRegistryTypeAdded    OpKind = "registry_type_added"
	OpRegistryTypeModified OpKind = "registry_type_modified"
)

// FieldChange is one field-level edit within a "modified" operation. Path
// mirrors Node's own field names: ["title"|"status"|"visibility"], ["sources"],
// ["edges", "<edge-field>"], or ["fields", "<type-specific-key>"]. Before is
// an optimistic stale-guard — apply rejects the whole changeset if the
// catalog's current value doesn't match it.
type FieldChange struct {
	Path   []string
	Before any
	After  any
}

// Operation is one entry in a changeset's ordered operations list.
type Operation struct {
	Kind OpKind

	// Node is the target node id for added/modified/removed/retyped. For renamed,
	// use From/To instead (NodeID is catalog-global, so a rename is a
	// distinct shape, not a field edit). For registry type ops, Node represents the type ID.
	Node NodeID

	After   map[string]any // added/registry_type_added: the full new mapping (must include "id" == Node)
	Before  map[string]any // removed/retyped: the full expected current node mapping (audit + stale-guard)
	Changes []FieldChange  // modified/registry_type_modified

	From NodeID // renamed
	To   NodeID // renamed

	FromType string // retyped
	ToType   string // retyped
}

// Changeset is the parsed, typed form of a `graph/changeset/v1` node's
// operations field. The changeset's lifecycle (ECR/ECO/ECN) rides on the
// node's own Status field — proposed / authorized / notified — not a
// second, changeset-specific field (Shared decision + WM.1-style W2.0
// design session: one source of truth for lifecycle state).
type Changeset struct {
	NodeID     NodeID
	Status     string
	Operations []Operation
}

const (
	ChangesetStatusProposed   = "proposed"
	ChangesetStatusAuthorized = "authorized"
	ChangesetStatusNotified   = "notified"
	// ChangesetStatusWithdrawn is a terminal lifecycle state a reviewer can
	// flip a "proposed" or "authorized" (not yet applied/"notified")
	// changeset to, cleaning it out of the active review queue without
	// deleting the audit record (A1, use-case-loop-plan §3.3's review-queue
	// "withdraw" action). Like authorize, this is a blessed direct write
	// (a lifecycle flip, not content), not a second changeset.
	ChangesetStatusWithdrawn = "withdrawn"
)

// ParseChangeset decodes a changeset node's Fields["operations"] into typed
// Operations. It does not validate the operations against a catalog — see
// ValidateChangeset for that.
func ParseChangeset(node *Node) (*Changeset, error) {
	if node.TypeID != "changeset" {
		return nil, fmt.Errorf("graph: node %q is type %q, not changeset", node.ID, node.TypeID)
	}
	raw, ok := node.Fields["operations"]
	if !ok {
		return nil, fmt.Errorf("graph: changeset %q missing operations", node.ID)
	}
	rawOps, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("graph: changeset %q operations must be a list", node.ID)
	}

	cs := &Changeset{NodeID: node.ID, Status: node.Status}
	for i, rawOp := range rawOps {
		opMap, ok := rawOp.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("graph: changeset %q operation %d must be a mapping", node.ID, i)
		}
		op, err := parseOperation(opMap)
		if err != nil {
			return nil, fmt.Errorf("graph: changeset %q operation %d: %w", node.ID, i, err)
		}
		cs.Operations = append(cs.Operations, op)
	}
	return cs, nil
}

func parseOperation(m map[string]any) (Operation, error) {
	kindRaw, _ := m["kind"].(string)
	kind := OpKind(kindRaw)
	switch kind {
	case OpAdded:
		after, ok := m["after"].(map[string]any)
		if !ok {
			return Operation{}, fmt.Errorf("added op missing \"after\" mapping")
		}
		idVal, _ := after["id"].(string)
		if idVal == "" {
			return Operation{}, fmt.Errorf("added op's \"after\" is missing \"id\"")
		}
		return Operation{Kind: kind, Node: NodeID(idVal), After: after}, nil
	case OpModified:
		nodeID, _ := m["node"].(string)
		if nodeID == "" {
			return Operation{}, fmt.Errorf("modified op missing \"node\"")
		}
		rawChanges, _ := m["changes"].([]any)
		if len(rawChanges) == 0 {
			return Operation{}, fmt.Errorf("modified op %q missing non-empty \"changes\"", nodeID)
		}
		var changes []FieldChange
		for i, rc := range rawChanges {
			cm, ok := rc.(map[string]any)
			if !ok {
				return Operation{}, fmt.Errorf("modified op %q change %d must be a mapping", nodeID, i)
			}
			rawPath, _ := cm["path"].([]any)
			if len(rawPath) == 0 {
				return Operation{}, fmt.Errorf("modified op %q change %d missing \"path\"", nodeID, i)
			}
			path := make([]string, len(rawPath))
			for j, p := range rawPath {
				s, ok := p.(string)
				if !ok {
					return Operation{}, fmt.Errorf("modified op %q change %d path segment %d must be a string", nodeID, i, j)
				}
				path[j] = s
			}
			changes = append(changes, FieldChange{Path: path, Before: cm["before"], After: cm["after"]})
		}
		return Operation{Kind: kind, Node: NodeID(nodeID), Changes: changes}, nil
	case OpRemoved:
		nodeID, _ := m["node"].(string)
		if nodeID == "" {
			return Operation{}, fmt.Errorf("removed op missing \"node\"")
		}
		// "before" may be omitted — hazard guard #5's server-side
		// guard-fill (fillGuards, called by Propose before
		// ValidateChangeset) fills the live node's full mapping in when
		// absent, so this is no longer a parse-time error.
		before, _ := m["before"].(map[string]any)
		return Operation{Kind: kind, Node: NodeID(nodeID), Before: before}, nil
	case OpRenamed:
		from, _ := m["from"].(string)
		to, _ := m["to"].(string)
		if from == "" || to == "" {
			return Operation{}, fmt.Errorf("renamed op requires both \"from\" and \"to\"")
		}
		return Operation{Kind: kind, From: NodeID(from), To: NodeID(to)}, nil
	case OpRetyped:
		nodeID, _ := m["node"].(string)
		fromType, _ := m["from_type"].(string)
		toType, _ := m["to_type"].(string)
		if nodeID == "" || fromType == "" || toType == "" {
			return Operation{}, fmt.Errorf("retyped op requires \"node\", \"from_type\", and \"to_type\"")
		}
		// "before" may be omitted — hazard guard #5's server-side guard-fill
		// (fillGuards, called by Propose before ValidateChangeset) fills the
		// live node's full mapping in when absent, mirroring OpRemoved.
		before, _ := m["before"].(map[string]any)
		return Operation{Kind: kind, Node: NodeID(nodeID), FromType: fromType, ToType: toType, Before: before}, nil
	case OpRegistryTypeAdded:
		after, ok := m["after"].(map[string]any)
		if !ok {
			return Operation{}, fmt.Errorf("registry_type_added op missing \"after\" mapping")
		}
		idVal, _ := after["id"].(string)
		if idVal == "" {
			return Operation{}, fmt.Errorf("registry_type_added op's \"after\" is missing \"id\"")
		}
		return Operation{Kind: kind, Node: NodeID(idVal), After: after}, nil
	case OpRegistryTypeModified:
		nodeID, _ := m["node"].(string)
		if nodeID == "" {
			return Operation{}, fmt.Errorf("registry_type_modified op missing \"node\"")
		}
		rawChanges, _ := m["changes"].([]any)
		if len(rawChanges) == 0 {
			return Operation{}, fmt.Errorf("registry_type_modified op %q missing non-empty \"changes\"", nodeID)
		}
		var changes []FieldChange
		for i, rc := range rawChanges {
			cm, ok := rc.(map[string]any)
			if !ok {
				return Operation{}, fmt.Errorf("registry_type_modified op %q change %d must be a mapping", nodeID, i)
			}
			rawPath, _ := cm["path"].([]any)
			if len(rawPath) == 0 {
				return Operation{}, fmt.Errorf("registry_type_modified op %q change %d missing \"path\"", nodeID, i)
			}
			path := make([]string, len(rawPath))
			for j, p := range rawPath {
				s, ok := p.(string)
				if !ok {
					return Operation{}, fmt.Errorf("registry_type_modified op %q change %d path segment %d must be a string", nodeID, i, j)
				}
				path[j] = s
			}
			changes = append(changes, FieldChange{Path: path, Before: cm["before"], After: cm["after"]})
		}
		return Operation{Kind: kind, Node: NodeID(nodeID), Changes: changes}, nil
	default:
		return Operation{}, fmt.Errorf("unknown op kind %q", kindRaw)
	}
}

// ValidateChangeset runs the pre-apply checks a changeset must pass before
// Apply will even attempt to build a candidate: node existence per op kind,
// and the optimistic stale-guard (before values must match the catalog's
// current state). It does not run graph.Lint — that's a post-apply check
// against the candidate catalog, done by Apply.
func ValidateChangeset(cs *Changeset, cat *Catalog) []string {
	var reasons []string
	reg := cloneRegistry(cat.Registry)

	for i, op := range cs.Operations {
		switch op.Kind {
		case OpAdded:
			if _, exists := cat.Nodes[op.Node]; exists {
				reasons = append(reasons, fmt.Sprintf("op %d (added): node %q already exists", i, op.Node))
			}
		case OpModified:
			node, exists := cat.Nodes[op.Node]
			if !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (modified): node %q does not exist", i, op.Node))
				continue
			}
			for _, ch := range op.Changes {
				current := readNodePath(cat, node, ch.Path)
				if ch.Before != nil && !valuesEqual(current, ch.Before) {
					reasons = append(reasons, fmt.Sprintf("op %d (modified): node %q path %v is stale: catalog has %v, changeset expected %v", i, op.Node, ch.Path, current, ch.Before))
				}
			}
		case OpRemoved:
			node, exists := cat.Nodes[op.Node]
			if !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (removed): node %q does not exist", i, op.Node))
				continue
			}
			// Stale-guard (hazard guard #5 extends the existing "modified"
			// guard to "removed"): op.Before, whether caller-supplied or
			// server-filled by fillGuards, must match the live node's full
			// mapping — a removal whose precondition no longer matches the
			// catalog (someone else already changed/removed it) is
			// rejected the same way a stale "modified" Before is.
			if len(op.Before) > 0 {
				if mismatches := nodeMatchesBefore(cat, node, op.Before); len(mismatches) > 0 {
					reasons = append(reasons, fmt.Sprintf("op %d (removed): node %q is stale: %s", i, op.Node, strings.Join(mismatches, "; ")))
				}
			}
		case OpRenamed:
			if _, exists := cat.Nodes[op.From]; !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (renamed): node %q (from) does not exist", i, op.From))
			}
			if _, exists := cat.Nodes[op.To]; exists {
				reasons = append(reasons, fmt.Sprintf("op %d (renamed): node %q (to) already exists", i, op.To))
			}
		case OpRetyped:
			node, exists := cat.Nodes[op.Node]
			if !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q does not exist", i, op.Node))
				continue
			}
			// Stale-guard (hazard guard #5 extends the existing "removed"
			// guard to "retyped"): op.Before, whether caller-supplied or
			// server-filled by fillGuards, must match the live node's full
			// mapping — a retype whose precondition no longer matches the
			// catalog (someone else already changed the node) is rejected
			// the same way a stale "removed" Before is.
			if len(op.Before) > 0 {
				if mismatches := nodeMatchesBefore(cat, node, op.Before); len(mismatches) > 0 {
					reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q is stale: %s", i, op.Node, strings.Join(mismatches, "; ")))
				}
			}
			if node.TypeID != op.FromType {
				reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q type is %q, expected %q", i, op.Node, node.TypeID, op.FromType))
			}
			if !reg.HasTypeDef(op.ToType) {
				reasons = append(reasons, fmt.Sprintf("op %d (retyped): target type %q does not exist in registry", i, op.ToType))
				continue
			}
			effTo, _ := reg.Effective(op.ToType)
			for _, reqField := range effTo.RequiredFields {
				if !isFieldSatisfied(node, reqField) {
					reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q missing required field %q under target type %q", i, op.Node, reqField, op.ToType))
				}
			}
			declaredEdges := map[EdgeField]bool{}
			for _, decl := range effTo.EdgeFields {
				declaredEdges[decl.ID] = true
			}
			effFrom, _ := reg.Effective(op.FromType)
			for _, decl := range effFrom.EdgeFields {
				targets := node.EdgeTargets(decl)
				if len(targets) > 0 && !declaredEdges[decl.ID] {
					reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q carries edge field %q which is not declared by target type %q", i, op.Node, decl.ID, op.ToType))
				}
			}
			for edgeField, targets := range node.Edges {
				if len(targets) > 0 && !declaredEdges[edgeField] {
					reasons = append(reasons, fmt.Sprintf("op %d (retyped): node %q carries edge field %q which is not declared by target type %q", i, op.Node, edgeField, op.ToType))
				}
			}
			for _, otherNode := range cat.Nodes {
				otherEff, ok := reg.Effective(otherNode.TypeID)
				if !ok {
					continue
				}
				for _, decl := range otherEff.EdgeFields {
					for _, target := range otherNode.EdgeTargets(decl) {
						if target == op.Node {
							if decl.TargetType != "" && !reg.IsA(op.ToType, decl.TargetType) {
								reasons = append(reasons, fmt.Sprintf("op %d (retyped): inbound edge %q on node %q (type %q) targets %q, but %q is not a %q",
									i, decl.ID, otherNode.ID, otherNode.TypeID, op.Node, op.ToType, decl.TargetType))
							}
						}
					}
				}
			}
		case OpRegistryTypeAdded:
			if reg.HasTypeDef(string(op.Node)) {
				reasons = append(reasons, fmt.Sprintf("op %d (registry_type_added): type %q already exists", i, op.Node))
			} else {
				var fileDef fileTypeDef
				raw, err := yaml.Marshal(op.After)
				if err == nil {
					_ = yaml.Unmarshal(raw, &fileDef)
				}
				def := TypeDef{
					ID:             fileDef.ID,
					Schema:         SchemaPin(fileDef.Schema),
					Extends:        fileDef.Extends,
					Summary:        fileDef.Summary,
					RequiredFields: fileDef.RequiredFields,
				}
				for _, e := range fileDef.EdgeFields {
					def.EdgeFields = append(def.EdgeFields, EdgeFieldDecl{
						ID:          EdgeField(e.ID),
						TargetType:  e.TargetType,
						Cardinality: Cardinality(e.Cardinality),
						Storage:     EdgeStorage(e.Storage),
						Acyclic:     e.Acyclic,
						Renders:     e.Renders,
						NestsUnder:  e.NestsUnder,
					})
				}
				_ = reg.Register(def)
				_ = reg.Resolve()
			}
		case OpRegistryTypeModified:
			def, exists := reg.TypeDef(string(op.Node))
			if !exists {
				reasons = append(reasons, fmt.Sprintf("op %d (registry_type_modified): type %q does not exist", i, op.Node))
				continue
			}
			for _, ch := range op.Changes {
				current := readTypeDefPath(def, ch.Path)
				if ch.Before != nil && !valuesEqual(current, ch.Before) {
					reasons = append(reasons, fmt.Sprintf("op %d (registry_type_modified): type %q path %v is stale: catalog has %v, changeset expected %v", i, op.Node, ch.Path, current, ch.Before))
				}
			}
			for _, ch := range op.Changes {
				if len(ch.Path) == 1 {
					switch ch.Path[0] {
					case "extends":
						def.Extends, _ = ch.After.(string)
					case "summary":
						def.Summary, _ = ch.After.(string)
					case "required_fields":
						if rawList, ok := ch.After.([]any); ok {
							var list []string
							for _, item := range rawList {
								if s, ok := item.(string); ok {
									list = append(list, s)
								}
							}
							def.RequiredFields = list
						}
					case "edge_fields":
						if rawList, ok := ch.After.([]any); ok {
							var edges []EdgeFieldDecl
							for _, item := range rawList {
								if m, ok := item.(map[string]any); ok {
									id, _ := m["id"].(string)
									tt, _ := m["target_type"].(string)
									card, _ := m["cardinality"].(string)
									st, _ := m["storage"].(string)
									ac, _ := m["acyclic"].(bool)
									ren, _ := m["renders"].(bool)
									nu, _ := m["nests_under"].(bool)
									edges = append(edges, EdgeFieldDecl{
										ID:          EdgeField(id),
										TargetType:  tt,
										Cardinality: Cardinality(card),
										Storage:     EdgeStorage(st),
										Acyclic:     ac,
										Renders:     ren,
										NestsUnder:  nu,
									})
								}
							}
							def.EdgeFields = edges
						}
					}
				}
			}
			reg.defs[string(op.Node)] = def
			_ = reg.Resolve()
		}
	}
	return reasons
}

func cloneRegistry(r *Registry) *Registry {
	clone := NewRegistry()
	for id, def := range r.defs {
		clone.defs[id] = def
	}
	_ = clone.Resolve()
	return clone
}

// readNodePath reads the current value at a FieldChange-style path off a
// live Node, for the stale-guard comparison in ValidateChangeset (and
// fillGuards' guard-fill). cat is required to resolve the node's type-
// registry edge-field declarations: an "edges" path MUST be read via
// Node.EdgeTargets against the decl, never node.Edges directly, or a
// storage: top_level edge (e.g. change.depends_on, which lives in
// Fields, not Edges) would silently read back as empty/nil even when
// populated — exactly the top_level-edge conformance hazard the hard
// constraint on edge resolution exists to prevent.
func readNodePath(cat *Catalog, node *Node, path []string) any {
	if len(path) == 0 {
		return nil
	}
	switch path[0] {
	case "title":
		return node.Title
	case "status":
		return node.Status
	case "visibility":
		return string(node.Visibility)
	case "sources":
		return node.Sources
	case "edges":
		if len(path) != 2 {
			return nil
		}
		if decl, ok := edgeFieldDecl(cat, node, path[1]); ok {
			return node.EdgeTargets(decl)
		}
		return node.Edges[EdgeField(path[1])]
	case "fields":
		if len(path) != 2 {
			return nil
		}
		return node.Fields[path[1]]
	default:
		return nil
	}
}

// edgeFieldDecl looks up the EdgeFieldDecl for node's type + the named edge
// field, via cat.Registry's effective type — the single place readNodePath/
// nodeToMap must go through to read an edge field correctly regardless of
// its declared storage (Edges map vs top_level Fields).
func edgeFieldDecl(cat *Catalog, node *Node, field string) (EdgeFieldDecl, bool) {
	if cat == nil || cat.Registry == nil {
		return EdgeFieldDecl{}, false
	}
	eff, ok := cat.Registry.Effective(node.TypeID)
	if !ok {
		return EdgeFieldDecl{}, false
	}
	for _, decl := range eff.EdgeFields {
		if string(decl.ID) == field {
			return decl, true
		}
	}
	return EdgeFieldDecl{}, false
}

// valuesEqual is a pragmatic stale-guard comparison (string rendering, not a
// deep-equal) — good enough to catch "someone else already changed this
// field" without needing a full YAML-value equality library for v1.
func valuesEqual(a, b any) bool {
	return fmt.Sprint(a) == fmt.Sprint(b)
}

// nodeToMap materializes a live Node back into the wire-mapping shape a
// changeset operation's After/Before mappings use (the approximate inverse
// of buildNode) — used by OpRemoved's and OpRetyped's guard-fill/stale-guard.
// Comparison-
// canonical: uses exactly the same field reads (readNodePath/EdgeTargets)
// the stale-guard elsewhere in this file compares against, so a filled
// Before is guaranteed to satisfy valuesEqual against a subsequent
// unmodified read. Edge fields are read via Node.EdgeTargets against the
// registry decl (never the raw Edges map), so a storage: top_level edge
// (e.g. change.depends_on) round-trips correctly.
func nodeToMap(cat *Catalog, node *Node) map[string]any {
	m := map[string]any{
		"id":         string(node.ID),
		"schema":     string(node.Schema),
		"title":      node.Title,
		"status":     node.Status,
		"visibility": string(node.Visibility),
	}
	if len(node.Sources) > 0 {
		srcs := make([]any, len(node.Sources))
		for i, s := range node.Sources {
			srcs[i] = string(s)
		}
		m["sources"] = srcs
	}
	if len(node.Fields) > 0 {
		fields := make(map[string]any, len(node.Fields))
		for k, v := range node.Fields {
			fields[k] = v
		}
		m["fields"] = fields
	}
	if eff, ok := cat.Registry.Effective(node.TypeID); ok && len(eff.EdgeFields) > 0 {
		edges := map[string]any{}
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
		if len(edges) > 0 {
			m["edges"] = edges
		}
	}
	return m
}

// nodeMatchesBefore compares a live node against a caller- or
// fillGuards-supplied "before" mapping (OpRemoved's/OpRetyped's stale-guard), field by
// field, via the same comparison-canonical reads nodeToMap uses. Returns a
// human-readable mismatch description per differing key; empty means the
// node still matches.
func nodeMatchesBefore(cat *Catalog, node *Node, before map[string]any) []string {
	current := nodeToMap(cat, node)
	var mismatches []string
	for key, want := range before {
		got := current[key]
		if !valuesEqual(got, want) {
			mismatches = append(mismatches, fmt.Sprintf("field %q: catalog has %v, changeset expected %v", key, got, want))
		}
	}
	sort.Strings(mismatches)
	return mismatches
}

// GuardFill records one precondition Propose filled in on the caller's
// behalf (hazard guard #5, plan §3.4 red-team amendment #5) because the
// wire payload omitted it. A "modified" fill echoes the actual field path
// and comparison-canonical value asserted; a "removed" or "retyped" fill
// echoes only a digest of the full node mapping filled into the
// operation's Before (sha256 + sorted field-name list), not the full
// content, to keep the echo small.
type GuardFill struct {
	Node   NodeID
	Path   []string // set for a "modified" fill; nil for a "removed" fill
	Value  any      // "modified" fill: the filled value
	SHA    string   // "removed" fill: sha256 of the filled full-node mapping
	Fields []string // "removed" fill: sorted keys present in the filled mapping
}

// fillGuards mutates cs.Operations in place, filling any omitted
// precondition from the live catalog:
//   - "modified" changes with a nil Before get it filled from
//     readNodePath(cat, node, path) — echoed as a GuardFill{Node,Path,Value}.
//   - "removed" and "retyped" ops with an empty Before get the live node's
//     full mapping filled via nodeToMap — echoed as a
//     GuardFill{Node,SHA,Fields} digest, not the full content.
//
// A node that does not exist is left alone (ValidateChangeset will reject
// it with a clearer "does not exist" reason). Filled values are
// comparison-canonical: exactly what valuesEqual/nodeMatchesBefore compare,
// so a filled Before always matches an unmodified read of the same node.
func fillGuards(cs *Changeset, cat *Catalog) []GuardFill {
	var fills []GuardFill
	for i := range cs.Operations {
		op := &cs.Operations[i]
		switch op.Kind {
		case OpModified:
			node, ok := cat.Nodes[op.Node]
			if !ok {
				continue
			}
			for j := range op.Changes {
				ch := &op.Changes[j]
				if ch.Before != nil {
					continue
				}
				val := readNodePath(cat, node, ch.Path)
				ch.Before = val
				fills = append(fills, GuardFill{Node: op.Node, Path: append([]string{}, ch.Path...), Value: val})
			}
		case OpRemoved, OpRetyped:
			node, ok := cat.Nodes[op.Node]
			if !ok || len(op.Before) > 0 {
				continue
			}
			full := nodeToMap(cat, node)
			op.Before = full
			sha, fields := digestNodeMap(full)
			fills = append(fills, GuardFill{Node: op.Node, SHA: sha, Fields: fields})
		}
	}
	return fills
}

// refreshStaleGuards mutates cs.Operations in place, refreshing every
// "modified" operation's field-level Before guard that no longer matches
// the live catalog (readNodePath(cat, node, ch.Path)) to the live value —
// Rebase's (propose.go) actual job (§3.3's review-queue "rebase" action):
// someone else edited a DIFFERENT field while this changeset sat in the
// queue, and the changeset's own field edit is still valid, only its
// optimistic-concurrency guard on that other field is stale. Reports
// whether it changed anything.
//
// This is fillGuards' sibling, not a replacement: fillGuards only fills a
// MISSING Before (nil), while this only overwrites a PRESENT-but-stale one
// (non-nil and mismatched) — the two conditions never overlap, so a
// changeset could in principle want both, though Propose/Rebase each only
// call one. Both share readNodePath/valuesEqual, the same comparison-
// canonical reads ValidateChangeset's own stale-guard check uses, so a
// refreshed Before is guaranteed to satisfy that check immediately after.
//
// Like fillGuards, this intentionally only touches "modified" op field
// guards — never "removed"/"retyped" whole-node Before mappings (those
// would need nodeToMap, not readNodePath) or any other op kind, which
// fad888bf7's original Rebase doc comment already scoped out as unneeded
// for the review-queue scenario this exists to fix.
func refreshStaleGuards(cs *Changeset, cat *Catalog) bool {
	changed := false
	for i := range cs.Operations {
		op := &cs.Operations[i]
		if op.Kind != OpModified {
			continue
		}
		node, ok := cat.Nodes[op.Node]
		if !ok {
			continue
		}
		for j := range op.Changes {
			ch := &op.Changes[j]
			if ch.Before == nil {
				continue
			}
			current := readNodePath(cat, node, ch.Path)
			if !valuesEqual(current, ch.Before) {
				ch.Before = current
				changed = true
			}
		}
	}
	return changed
}

// digestNodeMap produces the {sha, field list} echo GuardFill uses for a
// "removed" or "retyped" op's server-filled full-node Before mapping,
// keeping the echo small (a digest, not the full content) per hazard
// guard #5's spec.
func digestNodeMap(m map[string]any) (sha string, fields []string) {
	fields = make([]string, 0, len(m))
	for k := range m {
		fields = append(fields, k)
	}
	sort.Strings(fields)
	h := sha256.New()
	for _, k := range fields {
		fmt.Fprintf(h, "%s=%v\n", k, m[k])
	}
	return hex.EncodeToString(h.Sum(nil)), fields
}

// opsToRaw converts typed Operations back into the wire-map shape
// ParseChangeset/parseOperation decode (the inverse conversion) — needed so
// Propose can persist guard-filled operations (fillGuards mutates the typed
// Before values in place) into the changeset node's stored `operations`
// field, not just the caller's original un-filled wire payload.
func opsToRaw(ops []Operation) []map[string]any {
	out := make([]map[string]any, len(ops))
	for i, op := range ops {
		m := map[string]any{"kind": string(op.Kind)}
		switch op.Kind {
		case OpAdded, OpRegistryTypeAdded:
			m["after"] = op.After
		case OpModified, OpRegistryTypeModified:
			m["node"] = string(op.Node)
			changes := make([]any, len(op.Changes))
			for j, ch := range op.Changes {
				path := make([]any, len(ch.Path))
				for k, p := range ch.Path {
					path[k] = p
				}
				cm := map[string]any{"path": path, "after": ch.After}
				if ch.Before != nil {
					cm["before"] = ch.Before
				}
				changes[j] = cm
			}
			m["changes"] = changes
		case OpRemoved:
			m["node"] = string(op.Node)
			if op.Before != nil {
				m["before"] = op.Before
			}
		case OpRenamed:
			m["from"] = string(op.From)
			m["to"] = string(op.To)
		case OpRetyped:
			m["node"] = string(op.Node)
			m["from_type"] = op.FromType
			m["to_type"] = op.ToType
			if op.Before != nil {
				m["before"] = op.Before
			}
		}
		out[i] = m
	}
	return out
}

func isFieldSatisfied(node *Node, fieldName string) bool {
	switch fieldName {
	case "id":
		return node.ID != ""
	case "schema":
		return node.Schema != ""
	case "title":
		return node.Title != ""
	case "status":
		return node.Status != ""
	case "visibility":
		return node.Visibility != ""
	case "sources":
		return len(node.Sources) > 0
	default:
		val, exists := node.Fields[fieldName]
		if !exists || val == nil {
			return false
		}
		if s, ok := val.(string); ok && s == "" {
			return false
		}
		return true
	}
}

func readTypeDefPath(def TypeDef, path []string) any {
	if len(path) == 0 {
		return nil
	}
	switch path[0] {
	case "schema":
		return string(def.Schema)
	case "extends":
		return def.Extends
	case "summary":
		return def.Summary
	case "required_fields":
		return def.RequiredFields
	case "edge_fields":
		return def.EdgeFields
	default:
		return nil
	}
}
